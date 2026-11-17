package session

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// AbandonAfter is how long an active session may go without an applicant
// answer before the sweeper finishes it as abandoned (ARCHITECTURE §3).
const AbandonAfter = 10 * time.Minute

// DefaultMaxSession is the fallback session duration when the caller does not
// pass SESSION_MAX_MINUTES.
const DefaultMaxSession = 15 * time.Minute

const (
	sessionEndedEvent = "session.ended"
	runFailedEvent    = "run.failed"
	nodeSkippedEvent  = "node.skipped"
)

// machineNodeIDs is the ordered machine-node list (docs/EVENTS.md §3).
// internal/session cannot import internal/graph because graph imports session.
var machineNodeIDs = []string{
	"planner", "plan_check", "interviewer", "judge", "guard", "scorer", "evidence_check",
}

// plannerFailedRunReason is the run.failed reason for a session the sweeper
// ends before a plan existed: the planner never produced topics.
const plannerFailedRunReason = "planner_failed"

type sessionEndedPayload struct {
	FinishReason string `json:"finish_reason"`
}

type runFailedPayload struct {
	Error string `json:"error"`
}

type nodeSkippedPayload struct {
	Reason string `json:"reason"`
}

// Sweeper periodically expires invited sessions past their TTL and finishes
// active sessions that no graph step owns: idle for AbandonAfter since the last
// applicant answer, or past the session's time limit. It leaves a session alone
// while a step is in flight, because that step's runner may still finish it.
type Sweeper struct {
	store      store.Store
	registry   *Registry
	logger     *slog.Logger
	interval   time.Duration
	maxSession time.Duration
}

func NewSweeper(st store.Store, registry *Registry, maxSession time.Duration, logger *slog.Logger) *Sweeper {
	if maxSession <= 0 {
		maxSession = DefaultMaxSession
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Sweeper{
		store:      st,
		registry:   registry,
		logger:     logger,
		interval:   time.Minute,
		maxSession: maxSession,
	}
}

// Run sweeps once immediately and then on each interval until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	s.sweep(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// SweepOnce performs a single sweep and returns how many sessions it expired,
// abandoned, and ended at the time limit.
func (s *Sweeper) SweepOnce(ctx context.Context) (expired, abandoned, timedOut int, err error) {
	now := time.Now().UTC()

	invited, err := s.store.ListSessionsByStatus(ctx, domain.SessionStatusInvited)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, sess := range invited {
		if sess.ExpiresAt.After(now) {
			continue
		}
		changed, err := s.store.ExpireSession(ctx, sess.ID, now)
		if err != nil {
			return expired, abandoned, timedOut, err
		}
		if changed {
			expired++
		}
	}

	active, err := s.store.ListSessionsByStatus(ctx, domain.SessionStatusActive)
	if err != nil {
		return expired, abandoned, timedOut, err
	}
	for _, sess := range active {
		if s.registry.Has(sess.ID) {
			continue
		}
		reason, due, err := s.finishReason(ctx, sess, now)
		if err != nil {
			return expired, abandoned, timedOut, err
		}
		if !due {
			continue
		}

		// A session the sweeper ends before the planner produced topics has no
		// transcript to score. It is a failed run, and the application must be
		// failed so resend-invite can recover the applicant.
		topics, err := s.store.ListTopicsBySession(ctx, sess.ID)
		if err != nil {
			return expired, abandoned, timedOut, err
		}
		if len(topics) == 0 {
			reason = domain.FinishReasonFailed
		}

		var changed bool
		switch reason {
		case domain.FinishReasonFailed, domain.FinishReasonTimeLimit:
			changed, err = s.store.FinishSession(ctx, sess.ID, reason, now)
		default:
			changed, err = s.store.AbandonSession(ctx, sess.ID, now)
		}
		if err != nil {
			return expired, abandoned, timedOut, err
		}
		if !changed {
			continue
		}
		if reason == domain.FinishReasonFailed {
			// A session with no topics is a failed run, never an abandonment,
			// whether the sweeper closes the run itself or it was already over.
			s.finishFailed(ctx, sess)
			continue
		}
		if reason == domain.FinishReasonTimeLimit {
			timedOut++
		} else if reason == domain.FinishReasonAbandoned {
			abandoned++
		}
		s.finishApplication(ctx, sess, reason)
		s.emitEnded(ctx, sess, reason)
	}
	return expired, abandoned, timedOut, nil
}

// finishFailed closes a run the sweeper ended before a plan existed. A live
// manager emits the run and session events itself; without one the sweeper
// persists run.failed before session.ended so replay stays ordered. It reports
// whether it closed a running run itself, as opposed to ending a session whose
// run had already ended.
func (s *Sweeper) finishFailed(ctx context.Context, sess domain.Session) bool {
	if s.registry.NotifyFinished(sess.ID, domain.FinishReasonFailed) {
		return false
	}
	run, err := s.store.GetGraphRunBySession(ctx, sess.ID)
	if err != nil {
		return false
	}
	closedRun := run.Status == domain.GraphRunStatusRunning
	if closedRun {
		s.skipUnexecutedNodes(ctx, run.ID)
		if payload, err := json.Marshal(runFailedPayload{Error: plannerFailedRunReason}); err == nil {
			_, _ = s.store.AppendNodeEvent(ctx, domain.NodeEvent{RunID: run.ID, Type: runFailedEvent, Payload: payload})
		}
		ended := time.Now().UTC()
		errMsg := plannerFailedRunReason
		_ = s.store.UpdateGraphRun(ctx, domain.GraphRun{
			ID: run.ID, Status: domain.GraphRunStatusFailed, EndedAt: &ended, Error: &errMsg,
		})
	}
	if payload, err := json.Marshal(sessionEndedPayload{FinishReason: string(domain.FinishReasonFailed)}); err == nil {
		_, _ = s.store.AppendNodeEvent(ctx, domain.NodeEvent{RunID: run.ID, Type: sessionEndedEvent, Payload: payload})
	}
	return closedRun
}

// skipUnexecutedNodes emits node.skipped(run_failed) for every machine node
// that has no event yet, matching the EVENTS.md §6.3 failure sequence.
//
// A node with any event is treated as executed, which would leave an open
// attempt open. That is safe: finishFailed only reaches this function without a
// live manager, and in a single process every started session keeps manager
// state (finishFailed returns through NotifyFinished) while boot reconciliation
// closes runs a previous process left running. A no-topics run that reaches
// here therefore only has terminated attempts (the planner never started, or
// has node.failed). A multi-process deployment would need to close open
// attempts here too, with an EVENTS.md §4 error value.
func (s *Sweeper) skipUnexecutedNodes(ctx context.Context, runID string) {
	events, err := s.store.ListNodeEvents(ctx, runID, 0)
	if err != nil {
		return
	}
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		if event.NodeID != nil {
			seen[*event.NodeID] = struct{}{}
		}
	}
	payload, err := json.Marshal(nodeSkippedPayload{Reason: "run_failed"})
	if err != nil {
		return
	}
	for _, nodeID := range machineNodeIDs {
		if _, ok := seen[nodeID]; ok {
			continue
		}
		_, _ = s.store.AppendNodeEvent(ctx, domain.NodeEvent{
			RunID: runID, NodeID: domain.StrPtr(nodeID), Type: nodeSkippedEvent, Payload: payload,
		})
	}
}

// finishReason decides whether an active session the sweeper may touch is due
// to finish. The time limit wins over idleness: a live session that reaches its
// limit is time_limit, never abandoned.
func (s *Sweeper) finishReason(ctx context.Context, sess domain.Session, now time.Time) (domain.FinishReason, bool, error) {
	if sess.StartedAt == nil {
		return "", false, nil
	}
	if !now.Before(sess.StartedAt.Add(s.maxSession)) {
		return domain.FinishReasonTimeLimit, true, nil
	}
	lastActivity, ok, err := s.lastApplicantActivity(ctx, sess)
	if err != nil {
		return "", false, err
	}
	if !ok || lastActivity.Add(AbandonAfter).After(now) {
		return "", false, nil
	}
	return domain.FinishReasonAbandoned, true, nil
}

// finishApplication moves the application out of in_session the same way the
// runner does: a permanent failure becomes failed, any other finish continues
// to scoring.
func (s *Sweeper) finishApplication(ctx context.Context, sess domain.Session, reason domain.FinishReason) {
	app, err := s.store.GetApplication(ctx, sess.ApplicationID)
	if err != nil || app.Status != domain.ApplicationStatusInSession {
		return
	}
	next := domain.ApplicationStatusScoring
	if reason == domain.FinishReasonFailed {
		next = domain.ApplicationStatusFailed
	}
	_ = s.store.UpdateApplicationStatus(ctx, app.ID, next)
}

// emitEnded tells an in-process manager to emit session.ended (so connected and
// reconnecting clients see it with the run's seq), or persists the event itself
// when the runner is gone (e.g. after a restart).
func (s *Sweeper) emitEnded(ctx context.Context, sess domain.Session, reason domain.FinishReason) {
	if s.registry.NotifyFinished(sess.ID, reason) {
		return
	}
	run, err := s.store.GetGraphRunBySession(ctx, sess.ID)
	if err != nil {
		return
	}
	payload, err := json.Marshal(sessionEndedPayload{FinishReason: string(reason)})
	if err != nil {
		return
	}
	_, _ = s.store.AppendNodeEvent(ctx, domain.NodeEvent{RunID: run.ID, Type: sessionEndedEvent, Payload: payload})
}

// lastApplicantActivity returns the timestamp of the latest applicant message,
// falling back to started_at when the applicant has not answered yet.
func (s *Sweeper) lastApplicantActivity(ctx context.Context, sess domain.Session) (time.Time, bool, error) {
	last := sess.StartedAt
	if last == nil {
		return time.Time{}, false, nil
	}
	messages, err := s.store.ListMessagesBySession(ctx, sess.ID, false)
	if err != nil {
		return time.Time{}, false, err
	}
	for _, message := range messages {
		if message.Role == domain.MessageRoleApplicant && message.CreatedAt.After(*last) {
			created := message.CreatedAt
			last = &created
		}
	}
	return *last, true, nil
}

func (s *Sweeper) sweep(ctx context.Context) {
	expired, abandoned, timedOut, err := s.SweepOnce(ctx)
	if err != nil {
		s.logger.Error("session sweep failed", "error", err)
		return
	}
	if expired > 0 || abandoned > 0 || timedOut > 0 {
		s.logger.Info("session sweep", "expired", expired, "abandoned", abandoned, "timed_out", timedOut)
	}
}
