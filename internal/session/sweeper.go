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

const sessionEndedEvent = "session.ended"

type sessionEndedPayload struct {
	FinishReason string `json:"finish_reason"`
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

		var changed bool
		if reason == domain.FinishReasonTimeLimit {
			changed, err = s.store.FinishSession(ctx, sess.ID, reason, now)
			if err != nil {
				return expired, abandoned, timedOut, err
			}
			if changed {
				timedOut++
			}
		} else {
			changed, err = s.store.AbandonSession(ctx, sess.ID, now)
			if err != nil {
				return expired, abandoned, timedOut, err
			}
			if changed {
				abandoned++
			}
		}
		if !changed {
			continue
		}

		s.finishApplication(ctx, sess, reason)
		s.emitEnded(ctx, sess, reason)
	}
	return expired, abandoned, timedOut, nil
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
