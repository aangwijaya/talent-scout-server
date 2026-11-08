package session

import (
	"context"
	"log/slog"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// Sweeper periodically expires invited sessions past their TTL and abandons
// active sessions left past their deadline (e.g. after a server restart, when
// the runner goroutine is gone).
type Sweeper struct {
	store      store.Store
	maxMinutes time.Duration
	logger     *slog.Logger
	interval   time.Duration
}

func NewSweeper(st store.Store, maxMinutes time.Duration, logger *slog.Logger) *Sweeper {
	return &Sweeper{
		store:      st,
		maxMinutes: maxMinutes,
		logger:     logger,
		interval:   time.Minute,
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
		if sess.StartedAt == nil || sess.StartedAt.Add(s.maxMinutes).After(now) {
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
