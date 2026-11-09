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

// maxPlannerAttempts is the automatic plan_check retry budget (ARCHITECTURE
// §3: initial attempt + 1 retry). Transport failures use the applicant retry
// path instead and do not consume this budget.
const maxPlannerAttempts = 2

// plannerProgress carries the plan_check budget and the latest rejection across
// an applicant retry, so re-entering the planner loop never resets them. An
// outage consumes an attempt number but not a plan_check attempt.
type plannerProgress struct {
	planChecks  int
	feedback    []PlanCheckError
	nextAttempt int
}

// interviewerUnavailableMessage is the only text the applicant sees for a
// temporary interviewer failure. The provider's error stays in the server log.
const interviewerUnavailableMessage = "The interviewer is temporarily unavailable. Please try again."

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

	busy         bool
	waitingRetry bool
	done         bool
	retry        func(context.Context)
	executed     map[string]bool
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
	m := &SessionManager{
		cfg:      cfg,
		store:    st,
		bus:      bus,
		llm:      client,
		models:   models,
		registry: registry,
		logger:   logger,
		states:   make(map[string]*liveSession),
	}
	// The sweeper finishes idle sessions; when this process still owns the
	// session it emits session.ended through the runner so clients see it.
	registry.SetFinishHook(m.finishExternally)
	return m
}

// Start creates the graph run and launches the opening step (planner →
// plan_check → interviewer) in the background.
func (m *SessionManager) Start(ctx context.Context, sess domain.Session) error {
	if m == nil || m.store == nil || m.llm == nil {
		return ErrInvalidState
	}
	runID := domain.NewID()
	if err := m.store.CreateGraphRun(ctx, domain.GraphRun{ID: runID, SessionID: sess.ID, Status: domain.GraphRunStatusRunning}); err != nil {
		// The session is already active (StartSession ran); do not strand it
		// active with no run. Failing it lets the recruiter issue a new invite.
		m.abortStart(ctx, sess)
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
	m.registry.Add(sess.ID)

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
	fn := st.retry
	st.retry = nil
	m.mu.Unlock()

	m.registry.Add(sess.ID)
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

	plan, attempt, progress, err := m.runPlanner(ctx, sess, st, plannerProgress{nextAttempt: 1})
	if err != nil {
		m.plannerFailed(ctx, sess, st, progress, err, false)
		return
	}

	m.launchInterviewer(ctx, sess, st, plan, attempt)
}

// plannerFailed routes a failed planner loop: a transport outage offers the
// applicant a retry, anything else (including an exhausted plan_check budget)
// ends the run.
func (m *SessionManager) plannerFailed(ctx context.Context, sess domain.Session, st *liveSession, progress plannerProgress, err error, alreadyRetried bool) {
	if errors.Is(err, llm.ErrUnavailable) {
		m.handlePlannerError(ctx, sess, st, progress, err, alreadyRetried)
		return
	}
	reason := err.Error()
	if errors.Is(err, errPlanInvalid) {
		reason = "planner_invalid_after_retries"
	}
	m.failRun(ctx, sess, st, reason)
}

func (m *SessionManager) continueAfterAnswer(ctx context.Context, sess domain.Session, st *liveSession, topic *domain.Topic) {
	if topic == nil {
		m.failRun(ctx, sess, st, "no_active_topic")
		return
	}
	m.judgeAndGuard(ctx, sess, st, *topic)
}

// runPlanner runs the planner loop while the plan_check budget is not spent,
// feeding the latest rejection back into the next attempt. It returns the
// attempt number of the plan_check that accepted the plan (or of the last
// attempt before a failure) and the progress to continue from after an
// applicant retry. Only plan_check runs consume the budget; an outage leaves it
// untouched. A loop.retry/edge pair is emitted only when the loop continues
// from a plan_check rejection, because an applicant retry already emitted its
// own loop.retry/edge.
func (m *SessionManager) runPlanner(ctx context.Context, sess domain.Session, st *liveSession, progress plannerProgress) ([]PlannedTopic, int, plannerProgress, error) {
	job, app, err := m.loadPlannerInputs(ctx, sess)
	if err != nil {
		return nil, progress.nextAttempt, progress, err
	}

	retryFromCheck := false
	for progress.planChecks < maxPlannerAttempts {
		attempt := progress.nextAttempt
		if retryFromCheck {
			// The last automatic attempt the plan_check budget allows.
			maxAttempts := attempt + (maxPlannerAttempts - progress.planChecks) - 1
			if err := st.runner.EmitLoopRetry(ctx, "planner", attempt, maxAttempts, "invalid_plan", planFeedback(progress.feedback, job.Rubric)); err != nil {
				return nil, attempt, progress, err
			}
			if err := st.runner.EmitEdge(ctx, "plan_check", "planner", "invalid"); err != nil {
				return nil, attempt, progress, err
			}
		}

		plan, checkErrs, emitted, err := m.runPlannerOnce(ctx, sess, st, job, app, attempt, progress.feedback)
		progress.nextAttempt = emitted + 1
		if err != nil {
			return nil, attempt, progress, err
		}
		if len(checkErrs) == 0 {
			return plan.Topics, attempt, progress, nil
		}
		progress.planChecks++
		progress.feedback = checkErrs
		retryFromCheck = true
	}
	return nil, progress.nextAttempt - 1, progress, errPlanInvalid
}

func (m *SessionManager) loadPlannerInputs(ctx context.Context, sess domain.Session) (domain.Job, domain.Application, error) {
	app, err := m.store.GetApplication(ctx, sess.ApplicationID)
	if err != nil {
		return domain.Job{}, domain.Application{}, err
	}
	job, err := m.store.GetJob(ctx, app.JobID)
	if err != nil {
		return domain.Job{}, domain.Application{}, err
	}
	return job, app, nil
}

// runPlannerOnce runs one planner attempt and its plan_check. A plan_check
// failure is returned as check errors so the caller's automatic retry loop can
// feed them back; a transport failure is returned as err so the caller can
// offer the applicant a manual retry instead of failing the run.
func (m *SessionManager) runPlannerOnce(ctx context.Context, sess domain.Session, st *liveSession, job domain.Job, app domain.Application, attempt int, feedback []PlanCheckError) (Plan, []PlanCheckError, int, error) {
	st.executed["planner"] = true
	resp, emitted, err := st.runner.runLLMNode(ctx, "planner", attempt, llm.Request{
		Messages:       plannerMessages(job, app.CVText, feedback),
		ResponseFormat: plannerResponseFormat(),
	}, "job+rubric+cv", nil)
	if err != nil {
		return Plan{}, nil, emitted, err
	}
	if err := st.runner.EmitEdge(ctx, "planner", "plan_check", "planned"); err != nil {
		return Plan{}, nil, emitted, err
	}

	st.executed["plan_check"] = true
	start := time.Now()
	if err := st.runner.emitNodeStarted(ctx, "plan_check", attempt, nil, "validating plan"); err != nil {
		return Plan{}, nil, emitted, err
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
		return Plan{}, nil, emitted, err
	}

	if len(checkErrs) == 0 {
		if err := st.runner.EmitCheckPassed(ctx, "plan_check", attempt, "plan_topics", map[string]any{"topics": len(plan.Topics)}); err != nil {
			return Plan{}, nil, emitted, err
		}
		return plan, nil, emitted, nil
	}
	if err := st.runner.EmitCheckFailed(ctx, "plan_check", attempt, "plan_topics", planCheckPayload(checkErrs)); err != nil {
		return Plan{}, nil, emitted, err
	}
	return Plan{}, checkErrs, emitted, nil
}

// launchInterviewer persists the plan's topics, reports the first topic and
// enters the normal plan_check → interviewer path.
func (m *SessionManager) launchInterviewer(ctx context.Context, sess domain.Session, st *liveSession, plan []PlannedTopic, attempt int) {
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
	fresh, _ := m.store.GetSession(ctx, sess.ID)
	m.emitSessionUpdated(ctx, st, fresh, &first)
	if err := st.runner.EmitEdge(ctx, "plan_check", "interviewer", "valid"); err != nil {
		m.failRun(ctx, sess, st, "edge_failed")
		return
	}

	m.runInterviewer(ctx, sess, st, first, domain.MessageKindOpening, 1, nil)
}

// handlePlannerError gives the applicant one manual retry when the planner's
// model is unavailable. alreadyRetried is true when the retry of the same step
// failed, so a second failure ends the run. The retry re-enters the planner
// loop with the preserved plan_check budget and feedback; a plan_check failure
// keeps its own automatic retry budget.
func (m *SessionManager) handlePlannerError(ctx context.Context, sess domain.Session, st *liveSession, progress plannerProgress, err error, alreadyRetried bool) {
	m.logger.Error("planner step failed", "session_id", sess.ID, "attempt", progress.nextAttempt-1, "error", err)
	m.mu.Lock()
	canRetry := errors.Is(err, llm.ErrUnavailable) && !alreadyRetried && !st.done
	if canRetry {
		nextAttempt := progress.nextAttempt
		maxAttempts := nextAttempt + m.models.MaxRetries()
		st.waitingRetry = true
		st.busy = false
		st.retry = func(retryCtx context.Context) {
			_ = st.runner.EmitLoopRetry(retryCtx, "planner", nextAttempt, maxAttempts, "llm_unavailable", err.Error())
			_ = st.runner.EmitEdge(retryCtx, "planner", "planner", "retry")
			plan, attempt, nextProgress, planErr := m.runPlanner(retryCtx, sess, st, progress)
			if planErr != nil {
				m.plannerFailed(retryCtx, sess, st, nextProgress, planErr, true)
				return
			}
			m.launchInterviewer(retryCtx, sess, st, plan, attempt)
		}
		m.mu.Unlock()
		// No step is in flight while the applicant decides to retry, so the
		// sweeper may abandon the session if they never come back.
		m.registry.Remove(sess.ID)
		_, _ = m.bus.Emit(ctx, st.runID, Event{
			Type:    EventError,
			Payload: ErrorEvent{Code: "llm_unavailable", Message: interviewerUnavailableMessage, Retryable: true},
		})
		return
	}
	m.mu.Unlock()
	m.failRun(ctx, sess, st, "planner_failed")
}

func (m *SessionManager) runInterviewer(ctx context.Context, sess domain.Session, st *liveSession, topic domain.Topic, kind domain.MessageKind, attempt int, feedback *JudgeVerdict) {
	transcript, err := m.store.ListMessagesBySession(ctx, sess.ID, false)
	if err != nil {
		m.failRun(ctx, sess, st, "load_transcript_failed")
		return
	}
	st.executed["interviewer"] = true
	inputSummary := topic.Title + " (" + string(kind) + ")"
	messageID := domain.NewID()
	limiter := newWordLimiter(maxInterviewerWords)
	res, err := st.runner.streamInterviewer(ctx, attempt, interviewerMessages(topic, transcript, kind, feedback), inputSummary, messageID, limiter)
	if err != nil {
		m.handleInterviewerError(ctx, sess, st, topic, kind, feedback, res.Attempt, err, false)
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

// handleInterviewerError gives the applicant one manual retry per failed
// interviewer step. alreadyRetried is true when this failure is the retry of
// the same step, so a second failure ends the session instead of offering
// another retry; a later turn starts with a fresh budget.
func (m *SessionManager) handleInterviewerError(ctx context.Context, sess domain.Session, st *liveSession, topic domain.Topic, kind domain.MessageKind, feedback *JudgeVerdict, lastAttempt int, err error, alreadyRetried bool) {
	m.logger.Error("interviewer step failed", "session_id", sess.ID, "attempt", lastAttempt, "error", err)
	m.mu.Lock()
	canRetry := errors.Is(err, llm.ErrUnavailable) && !alreadyRetried && !st.done
	if canRetry {
		// The retry continues the attempt counter after the last attempt the
		// transport layer emitted, so node.started numbers never repeat. The
		// retry itself may still use the HTTP retries of a fresh LLM call.
		nextAttempt := lastAttempt + 1
		maxAttempts := nextAttempt + m.models.MaxRetries()
		st.waitingRetry = true
		st.busy = false
		st.retry = func(retryCtx context.Context) {
			_ = st.runner.EmitLoopRetry(retryCtx, "interviewer", nextAttempt, maxAttempts, "llm_unavailable", err.Error())
			_ = st.runner.EmitEdge(retryCtx, "interviewer", "interviewer", "retry")
			st.executed["interviewer"] = true
			transcript, loadErr := m.store.ListMessagesBySession(retryCtx, sess.ID, false)
			if loadErr != nil {
				m.failRun(retryCtx, sess, st, "load_transcript_failed")
				return
			}
			inputSummary := topic.Title + " (" + string(kind) + ")"
			messageID := domain.NewID()
			limiter := newWordLimiter(maxInterviewerWords)
			res, streamErr := st.runner.streamInterviewer(retryCtx, nextAttempt, interviewerMessages(topic, transcript, kind, feedback), inputSummary, messageID, limiter)
			if streamErr != nil {
				m.handleInterviewerError(retryCtx, sess, st, topic, kind, feedback, res.Attempt, streamErr, true)
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
		// No step is in flight while the applicant decides to retry, so the
		// sweeper may abandon the session if they never come back.
		m.registry.Remove(sess.ID)
		_, _ = m.bus.Emit(ctx, st.runID, Event{
			Type:    EventError,
			Payload: ErrorEvent{Code: "llm_unavailable", Message: interviewerUnavailableMessage, Retryable: true},
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

	// Update the session and application before session.ended so a client that
	// reads them after seeing the event cannot observe an active session.
	now := time.Now().UTC()
	changed, _ := m.store.FinishSession(ctx, sess.ID, domain.FinishReasonFailed, now)
	if changed {
		if app, err := m.store.GetApplication(ctx, sess.ApplicationID); err == nil && app.Status == domain.ApplicationStatusInSession {
			_ = m.store.UpdateApplicationStatus(ctx, sess.ApplicationID, domain.ApplicationStatusFailed)
		}
	}
	_, _ = m.bus.Emit(ctx, st.runID, Event{Type: EventSessionEnded, Payload: SessionEnded{FinishReason: string(domain.FinishReasonFailed)}})
	m.registry.Remove(sess.ID)

	m.mu.Lock()
	st.done = true
	st.busy = false
	st.waitingRetry = false
	st.retry = nil
	m.mu.Unlock()
}

// abortStart finishes a session whose graph run could not be created so it is
// not left active without a runner. A failed application can get a new invite.
func (m *SessionManager) abortStart(ctx context.Context, sess domain.Session) {
	now := time.Now().UTC()
	changed, _ := m.store.FinishSession(ctx, sess.ID, domain.FinishReasonFailed, now)
	if !changed {
		return
	}
	if app, err := m.store.GetApplication(ctx, sess.ApplicationID); err == nil && app.Status == domain.ApplicationStatusInSession {
		_ = m.store.UpdateApplicationStatus(ctx, app.ID, domain.ApplicationStatusFailed)
	}
}

// finishExternally reports a session.ended the sweeper already committed to the
// store. It returns false when this process has no live state for the session,
// in which case the sweeper persists the event directly. The scorer still runs
// on the transcript of a session the sweeper finished (ARCHITECTURE §3).
func (m *SessionManager) finishExternally(sessionID string, reason domain.FinishReason) bool {
	m.mu.Lock()
	st, ok := m.states[sessionID]
	if !ok {
		m.mu.Unlock()
		return false
	}
	if st.done {
		m.mu.Unlock()
		return true
	}
	st.done = true
	st.busy = false
	st.waitingRetry = false
	st.retry = nil
	runID := st.runID
	m.mu.Unlock()

	m.registry.Remove(sessionID)
	_, _ = m.bus.Emit(context.Background(), runID, Event{Type: EventSessionEnded, Payload: SessionEnded{FinishReason: string(reason)}})
	if sess, err := m.store.GetSession(context.Background(), sessionID); err == nil {
		if err := m.runScoring(context.Background(), sess, st, reason); err != nil {
			m.failScoring(context.Background(), sess, st, err)
		}
	}
	return true
}

// failScoring ends a run whose scoring could not finish. The session already
// ended, so only the run and application state change here.
func (m *SessionManager) failScoring(ctx context.Context, sess domain.Session, st *liveSession, err error) {
	m.logger.Error("scoring failed", "session_id", sess.ID, "error", err)
	_ = st.runner.Fail(ctx, "scoring_failed")
	if app, appErr := m.store.GetApplication(ctx, sess.ApplicationID); appErr == nil && app.Status == domain.ApplicationStatusScoring {
		_ = m.store.UpdateApplicationStatus(ctx, app.ID, domain.ApplicationStatusFailed)
	}
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
	m.registry.Remove(st.sessionID)
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
