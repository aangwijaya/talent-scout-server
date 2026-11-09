package session

import (
	"context"
	"log/slog"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// AbandonAfter is how long an active session may go without an applicant
// answer before the sweeper finishes it as abandoned (ARCHITECTURE §3).
const AbandonAfter = 10 * time.Minute

// Sweeper periodically expires invited sessions past their TTL and abandons
// active sessions that no runner owns and that have been idle for AbandonAfter.
// It never touches a session with a live runner in this process: that runner is
// responsible for finishing it (e.g. the guard's time limit).
type Sweeper struct {
	store    store.Store
	registry *Registry
	logger   *slog.Logger
	interval time.Duration
}

func NewSweeper(st store.Store, registry *Registry, logger *slog.Logger) *Sweeper {
	return &Sweeper{
		store:    st,
		registry: registry,
		logger:   logger,
		interval: time.Minute,
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

// SweepOnce performs a single sweep and returns how many sessions it expired
// and abandoned.
func (s *Sweeper) SweepOnce(ctx context.Context) (expired, abandoned int, err error) {
	now := time.Now().UTC()

	invited, err := s.store.ListSessionsByStatus(ctx, domain.SessionStatusInvited)
	if err != nil {
		return 0, 0, err
	}
	for _, sess := range invited {
		if sess.ExpiresAt.After(now) {
			continue
		}
		changed, err := s.store.ExpireSession(ctx, sess.ID, now)
		if err != nil {
			return expired, abandoned, err
		}
		if changed {
			expired++
		}
	}

	active, err := s.store.ListSessionsByStatus(ctx, domain.SessionStatusActive)
	if err != nil {
		return expired, abandoned, err
	}
	for _, sess := range active {
		if s.registry.Has(sess.ID) {
			continue
		}
		lastActivity, ok, err := s.lastApplicantActivity(ctx, sess)
		if err != nil {
			return expired, abandoned, err
		}
		if !ok || lastActivity.Add(AbandonAfter).After(now) {
			continue
		}
		changed, err := s.store.AbandonSession(ctx, sess.ID, now)
		if err != nil {
			return expired, abandoned, err
		}
		if changed {
			abandoned++
		}
	}
	return expired, abandoned, nil
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
	expired, abandoned, err := s.SweepOnce(ctx)
	if err != nil {
		s.logger.Error("session sweep failed", "error", err)
		return
	}
	if expired > 0 || abandoned > 0 {
		s.logger.Info("session sweep", "expired", expired, "abandoned", abandoned)
	}
}
