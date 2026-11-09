package graph

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// ErrInvalidState means the requested action does not fit the session's current
// graph state (AI still replying, or nothing waiting for a retry).
var ErrInvalidState = errors.New("invalid state")

var errPlanInvalid = errors.New("planner produced an invalid plan after retries")

// SessionManager owns the live graph state for the sessions running in this
// process. Handlers drive it with Start/SubmitAnswer/Retry; the sweeper uses the
// same Registry to leave live sessions alone.
type SessionManager struct {
	cfg      config.Config
	store    store.Store
	bus      *Bus
	llm      llm.Client
	models   config.ModelsConfig
	registry *session.Registry
	logger   *slog.Logger

	mu     sync.Mutex
	states map[string]*liveSession
}

type liveSession struct {
	sessionID string
	runID     string
	runner    *Runner

	busy          bool
	waitingRetry  bool
	manualRetries int
	done          bool
	retry         func(context.Context)
	executed      map[string]bool
}

// NewSessionManager builds a manager. A nil registry is replaced with a fresh
// one so callers that do not run a sweeper still work in tests.
func NewSessionManager(cfg config.Config, st store.Store, bus *Bus, client llm.Client, models config.ModelsConfig, registry *session.Registry, logger *slog.Logger) *SessionManager {
	if registry == nil {
		registry = session.NewRegistry()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionManager{
		cfg:      cfg,
		store:    st,
		bus:      bus,
		llm:      client,
		models:   models,
		registry: registry,
		logger:   logger,
		states:   make(map[string]*liveSession),
	}
}

// Start creates the graph run and launches the opening step (planner →
// plan_check → interviewer) in the background.
func (m *SessionManager) Start(ctx context.Context, sess domain.Session) error {
	if m == nil || m.store == nil || m.llm == nil {
		return ErrInvalidState
	}
	runID := domain.NewID()
	if err := m.store.CreateGraphRun(ctx, domain.GraphRun{ID: runID, SessionID: sess.ID, Status: domain.GraphRunStatusRunning}); err != nil {
		return err
	}
	st := &liveSession{
		sessionID: sess.ID,
		runID:     runID,
		runner:    NewRunner(m.store, m.bus, m.llm, m.models, runID, sess.ID),
		busy:      true,
		executed:  make(map[string]bool),
	}
	m.mu.Lock()
	m.states[sess.ID] = st
	m.mu.Unlock()
	m.registry.Add(sess.ID)
	m.bus.BindSession(runID, sess.ID)

	go m.startRun(context.Background(), sess, st)
	return nil
}

// SubmitAnswer stores the applicant's answer and launches the next question.
func (m *SessionManager) SubmitAnswer(ctx context.Context, sess domain.Session, content string) (domain.Message, error) {
	m.mu.Lock()
	st, ok := m.states[sess.ID]
	if !ok || st.busy || st.waitingRetry || st.done {
		m.mu.Unlock()
		return domain.Message{}, ErrInvalidState
	}
	st.busy = true
	m.mu.Unlock()

	topic, err := m.activeTopic(ctx, sess.ID)
	if err == nil {
		err = m.store.CreateMessage(ctx, domain.Message{
			ID:        domain.NewID(),
			SessionID: sess.ID,
			TopicID:   topicID(topic),
			Role:      domain.MessageRoleApplicant,
			Kind:      domain.MessageKindAnswer,
			Content:   content,
		})
	}
	if err != nil {
		m.idle(st)
		return domain.Message{}, err
	}

	messages, err := m.store.ListMessagesBySession(ctx, sess.ID, false)
	if err != nil {
		m.idle(st)
		return domain.Message{}, err
	}
	answer := messages[len(messages)-1]
	if err := st.runner.EmitMessageCompleted(ctx, "interviewer", 1, answer); err != nil {
		m.idle(st)
		return domain.Message{}, err
	}
	fresh, _ := m.store.GetSession(ctx, sess.ID)
	m.emitSessionUpdated(ctx, st, fresh, topic)

	go m.continueAfterAnswer(context.Background(), sess, st, topic)
	return answer, nil
}

// Retry re-runs the failed step when it emitted a retryable error. It returns
// ErrInvalidState when nothing is waiting for a retry.
func (m *SessionManager) Retry(ctx context.Context, sess domain.Session) error {
	m.mu.Lock()
	st, ok := m.states[sess.ID]
	if !ok || !st.waitingRetry || st.done || st.retry == nil {
		m.mu.Unlock()
		return ErrInvalidState
	}
	st.waitingRetry = false
	st.busy = true
	st.manualRetries++
	fn := st.retry
	st.retry = nil
	m.mu.Unlock()

	go fn(context.Background())
	return nil
}

// Wait blocks until the session has no step in flight. It is primarily used by
// tests to observe an asynchronous run deterministically.
func (m *SessionManager) Wait(ctx context.Context, sessionID string) error {
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		st := m.states[sessionID]
		idle := st != nil && !st.busy
		m.mu.Unlock()
		if idle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *SessionManager) startRun(ctx context.Context, sess domain.Session, st *liveSession) {
	if err := st.runner.Start(ctx); err != nil {
		m.failRun(ctx, sess, st, "run_start_failed")
		return
	}
	fresh, _ := m.store.GetSession(ctx, sess.ID)
	m.emitSessionUpdated(ctx, st, fresh, nil)

	plan, attempt, err := m.runPlanner(ctx, sess, st)
	if err != nil {
		reason := err.Error()
		if errors.Is(err, errPlanInvalid) {
			reason = "planner_invalid_after_retries"
		}
		m.failRun(ctx, sess, st, reason)
		return
	}

	topics := make([]domain.Topic, 0, len(plan))
	for i, planned := range plan {
		status := domain.TopicStatusPending
		if i == 0 {
			status = domain.TopicStatusActive
		}
		topics = append(topics, domain.Topic{
			ID:           domain.NewID(),
			SessionID:    sess.ID,
			Position:     i + 1,
			Title:        planned.Title,
			Goal:         planned.Goal,
			RubricKeys:   planned.RubricKeys,
			SeedQuestion: planned.SeedQuestion,
			Status:       status,
		})
	}
	if err := m.store.CreateTopics(ctx, topics); err != nil {
		m.failRun(ctx, sess, st, "create_topics_failed")
		return
	}

	first := topics[0]
	if err := st.runner.EmitTopicChanged(ctx, "plan_check", attempt, first.ID, first.Position, first.Title, string(first.Status)); err != nil {
		m.failRun(ctx, sess, st, "topic_changed_failed")
		return
	}
	fresh, _ = m.store.GetSession(ctx, sess.ID)
	m.emitSessionUpdated(ctx, st, fresh, &first)
	if err := st.runner.EmitEdge(ctx, "plan_check", "interviewer", "valid"); err != nil {
		m.failRun(ctx, sess, st, "edge_failed")
		return
	}

	m.runInterviewer(ctx, sess, st, first, domain.MessageKindOpening, 1)
}

func (m *SessionManager) continueAfterAnswer(ctx context.Context, sess domain.Session, st *liveSession, topic *domain.Topic) {
	if topic == nil {
		m.failRun(ctx, sess, st, "no_active_topic")
		return
	}
	// server#10 inserts judge → guard here; until then the interviewer asks a
	// follow-up on the same topic.
	m.runInterviewer(ctx, sess, st, *topic, domain.MessageKindFollowUp, 1)
}

func (m *SessionManager) runPlanner(ctx context.Context, sess domain.Session, st *liveSession) ([]PlannedTopic, int, error) {
	app, err := m.store.GetApplication(ctx, sess.ApplicationID)
	if err != nil {
		return nil, 0, err
	}
	job, err := m.store.GetJob(ctx, app.JobID)
	if err != nil {
		return nil, 0, err
	}

	var feedback []PlanCheckError
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt == 2 {
			if err := st.runner.EmitLoopRetry(ctx, "planner", attempt, 2, "invalid_plan", planFeedback(feedback)); err != nil {
				return nil, attempt, err
			}
			if err := st.runner.EmitEdge(ctx, "plan_check", "planner", "invalid"); err != nil {
				return nil, attempt, err
			}
		}

		st.executed["planner"] = true
		resp, err := st.runner.RunLLMNode(ctx, "planner", attempt, llm.Request{
			Messages:       plannerMessages(job, app.CVText, feedback),
			ResponseFormat: plannerResponseFormat(),
		}, "job+rubric+cv", nil)
		if err != nil {
			return nil, attempt, err
		}
		if err := st.runner.EmitEdge(ctx, "planner", "plan_check", "planned"); err != nil {
			return nil, attempt, err
		}

		st.executed["plan_check"] = true
		start := time.Now()
		if err := st.runner.emitNodeStarted(ctx, "plan_check", attempt, nil, "validating plan"); err != nil {
			return nil, attempt, err
		}
		plan, parseErr := parsePlan(resp.Content)
		var checkErrs []PlanCheckError
		if parseErr != nil {
			checkErrs = []PlanCheckError{{Reason: "invalid_json: " + parseErr.Error()}}
		} else {
			checkErrs = CheckPlan(plan, job.Rubric)
		}
		summary := "valid"
		if len(checkErrs) > 0 {
			summary = "invalid"
		}
		if err := st.runner.emitNodeCompleted(ctx, "plan_check", attempt, nil, summary, 0, 0, time.Since(start)); err != nil {
			return nil, attempt, err
		}

		if len(checkErrs) == 0 {
			if err := st.runner.EmitCheckPassed(ctx, "plan_check", attempt, "plan_topics", map[string]any{"topics": len(plan.Topics)}); err != nil {
				return nil, attempt, err
			}
			return plan.Topics, attempt, nil
		}
		if err := st.runner.EmitCheckFailed(ctx, "plan_check", attempt, "plan_topics", planCheckPayload(checkErrs)); err != nil {
			return nil, attempt, err
		}
		feedback = checkErrs
	}
	return nil, 2, errPlanInvalid
}

func (m *SessionManager) runInterviewer(ctx context.Context, sess domain.Session, st *liveSession, topic domain.Topic, kind domain.MessageKind, attempt int) {
	transcript, err := m.store.ListMessagesBySession(ctx, sess.ID, false)
	if err != nil {
		m.failRun(ctx, sess, st, "load_transcript_failed")
		return
	}
	st.executed["interviewer"] = true
	inputSummary := topic.Title + " (" + string(kind) + ")"
	messageID := domain.NewID()
	limiter := newWordLimiter(maxInterviewerWords)
	res, err := st.runner.streamInterviewer(ctx, attempt, interviewerMessages(topic, transcript, kind), inputSummary, messageID, limiter)
	if err != nil {
		m.handleInterviewerError(ctx, sess, st, topic, kind, err)
		return
	}

	message := domain.Message{
		ID:        messageID,
		SessionID: sess.ID,
		TopicID:   &topic.ID,
		Role:      domain.MessageRoleAssistant,
		Kind:      kind,
		Content:   res.Text,
	}
	if err := m.store.CreateMessage(ctx, message); err != nil {
		m.failRun(ctx, sess, st, "create_message_failed")
		return
	}
	if err := st.runner.EmitMessageCompleted(ctx, "interviewer", res.Attempt, message); err != nil {
		m.failRun(ctx, sess, st, "message_event_failed")
		return
	}
	if err := st.runner.emitNodeCompleted(ctx, "interviewer", res.Attempt, &res.Model, summarize(res.Text, 80), res.Usage.PromptTokens, res.Usage.CompletionTokens, time.Since(res.StartedAt)); err != nil {
		m.failRun(ctx, sess, st, "node_event_failed")
		return
	}

	fresh, err := m.recordQuestion(ctx, sess.ID)
	if err == nil {
		m.emitSessionUpdated(ctx, st, fresh, &topic)
	}
	m.idle(st)
}

func (m *SessionManager) handleInterviewerError(ctx context.Context, sess domain.Session, st *liveSession, topic domain.Topic, kind domain.MessageKind, err error) {
	m.mu.Lock()
	canRetry := errors.Is(err, llm.ErrUnavailable) && st.manualRetries == 0 && !st.done
	if canRetry {
		st.waitingRetry = true
		st.busy = false
		st.retry = func(retryCtx context.Context) {
			_ = st.runner.EmitLoopRetry(retryCtx, "interviewer", 2, 2, "llm_unavailable", err.Error())
			st.executed["interviewer"] = true
			transcript, loadErr := m.store.ListMessagesBySession(retryCtx, sess.ID, false)
			if loadErr != nil {
				m.failRun(retryCtx, sess, st, "load_transcript_failed")
				return
			}
			inputSummary := topic.Title + " (" + string(kind) + ")"
			messageID := domain.NewID()
			limiter := newWordLimiter(maxInterviewerWords)
			res, streamErr := st.runner.streamInterviewer(retryCtx, 2, interviewerMessages(topic, transcript, kind), inputSummary, messageID, limiter)
			if streamErr != nil {
				m.handleInterviewerError(retryCtx, sess, st, topic, kind, streamErr)
				return
			}
			message := domain.Message{ID: messageID, SessionID: sess.ID, TopicID: &topic.ID, Role: domain.MessageRoleAssistant, Kind: kind, Content: res.Text}
			if storeErr := m.store.CreateMessage(retryCtx, message); storeErr != nil {
				m.failRun(retryCtx, sess, st, "create_message_failed")
				return
			}
			if eventErr := st.runner.EmitMessageCompleted(retryCtx, "interviewer", res.Attempt, message); eventErr != nil {
				m.failRun(retryCtx, sess, st, "message_event_failed")
				return
			}
			if eventErr := st.runner.emitNodeCompleted(retryCtx, "interviewer", res.Attempt, &res.Model, summarize(res.Text, 80), res.Usage.PromptTokens, res.Usage.CompletionTokens, time.Since(res.StartedAt)); eventErr != nil {
				m.failRun(retryCtx, sess, st, "node_event_failed")
				return
			}
			if fresh, loadErr := m.recordQuestion(retryCtx, sess.ID); loadErr == nil {
				m.emitSessionUpdated(retryCtx, st, fresh, &topic)
			}
			m.idle(st)
		}
		m.mu.Unlock()
		_, _ = m.bus.Emit(ctx, st.runID, Event{
			Type:    EventError,
			Payload: ErrorEvent{Code: "llm_unavailable", Message: err.Error(), Retryable: true},
		})
		return
	}
	m.mu.Unlock()
	m.failRun(ctx, sess, st, "interviewer_failed")
}

func (m *SessionManager) failRun(ctx context.Context, sess domain.Session, st *liveSession, reason string) {
	for _, nodeID := range NodeIDs {
		if nodeID == "recruiter_gate" || st.executed[nodeID] {
			continue
		}
		_ = st.runner.EmitNodeSkipped(ctx, nodeID, "run_failed")
	}
	_ = st.runner.Fail(ctx, reason)
	_, _ = m.bus.Emit(ctx, st.runID, Event{Type: EventSessionEnded, Payload: SessionEnded{FinishReason: string(domain.FinishReasonFailed)}})

	now := time.Now().UTC()
	if fresh, err := m.store.GetSession(ctx, sess.ID); err == nil {
		reasonValue := domain.FinishReasonFailed
		fresh.Status = domain.SessionStatusCompleted
		fresh.FinishReason = &reasonValue
		fresh.EndedAt = &now
		_ = m.store.UpdateSession(ctx, fresh)
	}
	_ = m.store.UpdateApplicationStatus(ctx, sess.ApplicationID, domain.ApplicationStatusFailed)
	m.registry.Remove(sess.ID)

	m.mu.Lock()
	st.done = true
	st.busy = false
	st.waitingRetry = false
	st.retry = nil
	m.mu.Unlock()
}

func (m *SessionManager) recordQuestion(ctx context.Context, sessionID string) (domain.Session, error) {
	fresh, err := m.store.GetSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	fresh.QuestionCount++
	if err := m.store.UpdateSession(ctx, fresh); err != nil {
		return domain.Session{}, err
	}
	return fresh, nil
}

func (m *SessionManager) activeTopic(ctx context.Context, sessionID string) (*domain.Topic, error) {
	topics, err := m.store.ListTopicsBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for i := range topics {
		if topics[i].Status == domain.TopicStatusActive {
			return &topics[i], nil
		}
	}
	if len(topics) == 0 {
		return nil, nil
	}
	return &topics[0], nil
}

func (m *SessionManager) emitSessionUpdated(ctx context.Context, st *liveSession, sess domain.Session, topic *domain.Topic) {
	if st == nil || st.runner == nil {
		return
	}
	var current *CurrentTopic
	if topic == nil {
		if active, err := m.activeTopic(ctx, sess.ID); err == nil {
			topic = active
		}
	}
	if topic != nil {
		total := 0
		if topics, err := m.store.ListTopicsBySession(ctx, sess.ID); err == nil {
			total = len(topics)
		}
		if total > 0 {
			current = &CurrentTopic{Position: topic.Position, Title: topic.Title, Total: total}
		}
	}
	var deadline *string
	if sess.StartedAt != nil {
		value := sess.StartedAt.Add(time.Duration(m.cfg.SessionMaxMinutes) * time.Minute).UTC().Format(time.RFC3339)
		deadline = &value
	}
	_, _ = m.bus.Emit(ctx, st.runID, Event{
		Type: EventSessionUpdated,
		Payload: SessionUpdated{
			Status:        string(sess.Status),
			DeadlineAt:    deadline,
			QuestionCount: sess.QuestionCount,
			MaxQuestions:  m.cfg.SessionMaxQuestions,
			CurrentTopic:  current,
		},
	})
}

func (m *SessionManager) idle(st *liveSession) {
	m.mu.Lock()
	st.busy = false
	m.mu.Unlock()
}

func topicID(topic *domain.Topic) *string {
	if topic == nil {
		return nil
	}
	return &topic.ID
}
