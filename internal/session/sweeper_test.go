package session

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func seedSession(t *testing.T, st store.Store, s domain.Session) {
	t.Helper()
	if err := st.CreateSession(context.Background(), s); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func newSweeper(t *testing.T, st store.Store) *Sweeper {
	t.Helper()
	return NewSweeper(st, 15*time.Minute, slog.New(slog.NewTextHandler(discardWriter{}, nil)))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestSweepExpiresInvitedPastTTL(t *testing.T) {
	st := store.NewMemory()
	seedSession(t, st, domain.Session{
		ID:            "s-expired",
		ApplicationID: "a1",
		TokenHash:     "hash-expired",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(-time.Hour),
	})
	seedSession(t, st, domain.Session{
		ID:            "s-future",
		ApplicationID: "a1",
		TokenHash:     "hash-future",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(time.Hour),
	})

	expired, abandoned, err := newSweeper(t, st).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if expired != 1 {
		t.Errorf("expired = %d, want 1", expired)
	}
	if abandoned != 0 {
		t.Errorf("abandoned = %d, want 0", abandoned)
	}

	got, _ := st.GetSession(context.Background(), "s-expired")
	if got.Status != domain.SessionStatusExpired {
		t.Errorf("s-expired status = %q, want expired", got.Status)
	}
	still, _ := st.GetSession(context.Background(), "s-future")
	if still.Status != domain.SessionStatusInvited {
		t.Errorf("s-future status = %q, want invited", still.Status)
	}
}

func TestSweepAbandonsActivePastDeadline(t *testing.T) {
	st := store.NewMemory()
	started := time.Now().Add(-16 * time.Minute)
	seedSession(t, st, domain.Session{
		ID:            "s-stale",
		ApplicationID: "a1",
		TokenHash:     "hash-stale",
		Status:        domain.SessionStatusActive,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		StartedAt:     &started,
	})
	recent := time.Now().Add(-5 * time.Minute)
	seedSession(t, st, domain.Session{
		ID:            "s-recent",
		ApplicationID: "a1",
		TokenHash:     "hash-recent",
		Status:        domain.SessionStatusActive,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		StartedAt:     &recent,
	})

	_, abandoned, err := newSweeper(t, st).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if abandoned != 1 {
		t.Errorf("abandoned = %d, want 1", abandoned)
	}

	got, _ := st.GetSession(context.Background(), "s-stale")
	if got.Status != domain.SessionStatusCompleted {
		t.Errorf("s-stale status = %q, want completed", got.Status)
	}
	if got.FinishReason == nil || *got.FinishReason != domain.FinishReasonAbandoned {
		t.Errorf("s-stale finish_reason = %v, want abandoned", got.FinishReason)
	}
	if got.EndedAt == nil {
		t.Errorf("s-stale ended_at is nil")
	}
	still, _ := st.GetSession(context.Background(), "s-recent")
	if still.Status != domain.SessionStatusActive {
		t.Errorf("s-recent status = %q, want active", still.Status)
	}
}

func TestSweepIgnoresCompleted(t *testing.T) {
	st := store.NewMemory()
	ended := time.Now()
	seedSession(t, st, domain.Session{
		ID:            "s-done",
		ApplicationID: "a1",
		TokenHash:     "hash-done",
		Status:        domain.SessionStatusCompleted,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		StartedAt:     func() *time.Time { x := time.Now().Add(-2 * time.Hour); return &x }(),
		EndedAt:       &ended,
	})

	expired, abandoned, err := newSweeper(t, st).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if expired != 0 || abandoned != 0 {
		t.Errorf("expired=%d abandoned=%d, want 0/0", expired, abandoned)
	}
}
