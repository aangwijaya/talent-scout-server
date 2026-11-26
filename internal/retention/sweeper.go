// Package retention deletes audio recordings once their retention window ends.
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const (
	// RecordingRetention is how long a recording attached to an answer is kept
	// after upload (ARCHITECTURE §5).
	RecordingRetention = 30 * 24 * time.Hour
	// UnsentUploadRetention is how long an upload never attached to an answer
	// is kept after upload (ARCHITECTURE §5).
	UnsentUploadRetention = 24 * time.Hour

	sweepInterval = 24 * time.Hour
)

// Sweeper deletes recordings whose retention window has ended. It deletes the
// storage object first and only then marks the upload row deleted, so a storage
// failure leaves the row for the next run.
type Sweeper struct {
	store    store.Store
	storage  storage.Storage
	logger   *slog.Logger
	interval time.Duration
}

func NewSweeper(st store.Store, audio storage.Storage, logger *slog.Logger) *Sweeper {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sweeper{
		store:    st,
		storage:  audio,
		logger:   logger,
		interval: sweepInterval,
	}
}

// Run sweeps once immediately and then once a day until ctx is cancelled.
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

// SweepOnce deletes every due recording and returns how many it removed.
func (s *Sweeper) SweepOnce(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	uploads, err := s.store.ListAudioUploadsDue(ctx, now.Add(-RecordingRetention), now.Add(-UnsentUploadRetention))
	if err != nil {
		return 0, err
	}

	deleted := 0
	for _, upload := range uploads {
		if err := s.storage.Delete(ctx, upload.ObjectPath); err != nil {
			s.logger.Error("retention: delete recording failed",
				"upload_id", upload.ID, "session_id", upload.SessionID, "error", err)
			continue
		}
		if err := s.store.MarkAudioUploadDeleted(ctx, upload.ID, now); err != nil {
			s.logger.Error("retention: mark upload deleted failed",
				"upload_id", upload.ID, "session_id", upload.SessionID, "error", err)
			continue
		}
		deleted++
	}
	return deleted, nil
}

func (s *Sweeper) sweep(ctx context.Context) {
	deleted, err := s.SweepOnce(ctx)
	if err != nil {
		s.logger.Error("retention sweep failed", "error", err)
		return
	}
	if deleted > 0 {
		s.logger.Info("retention sweep", "deleted", deleted)
	}
}
