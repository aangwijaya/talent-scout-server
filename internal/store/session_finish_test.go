package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func seedActive(t *testing.T, s *store.Memory, appStatus domain.ApplicationStatus) domain.Session {
	t.Helper()
	ctx := context.Background()
	app := domain.Application{
		ID: domain.NewID(), JobID: domain.NewID(), Name: "Budi", Email: "budi@example.com",
		Status: appStatus,
	}
	if err := s.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	started := time.Now().Add(-time.Minute).UTC()
	sess := domain.Session{
		ID: domain.NewID(), ApplicationID: app.ID, TokenHash: domain.NewID(),
		Status: domain.SessionStatusActive, ExpiresAt: time.Now().Add(time.Hour), StartedAt: &started,
	}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

func TestMemoryAbandonSessionMovesApplicationToScoring(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	sess := seedActive(t, s, domain.ApplicationStatusInSession)

	changed, err := s.AbandonSession(ctx, sess.ID, time.Now().UTC())
	if err != nil || !changed {
		t.Fatalf("AbandonSession = %v, %v; want true, nil", changed, err)
	}
	app, err := s.GetApplication(ctx, sess.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.Status != domain.ApplicationStatusScoring {
		t.Errorf("application status = %q, want scoring in the same transition", app.Status)
	}
}

func TestMemoryFinishSessionFailedMovesApplicationToFailed(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	sess := seedActive(t, s, domain.ApplicationStatusInSession)

	changed, err := s.FinishSession(ctx, sess.ID, domain.FinishReasonFailed, time.Now().UTC())
	if err != nil || !changed {
		t.Fatalf("FinishSession = %v, %v; want true, nil", changed, err)
	}
	app, err := s.GetApplication(ctx, sess.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.Status != domain.ApplicationStatusFailed {
		t.Errorf("application status = %q, want failed", app.Status)
	}
}

func TestMemoryListGraphRunsByStatus(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	running := domain.GraphRun{ID: domain.NewID(), SessionID: domain.NewID(), Status: domain.GraphRunStatusRunning}
	failed := domain.GraphRun{ID: domain.NewID(), SessionID: domain.NewID(), Status: domain.GraphRunStatusFailed}
	for _, run := range []domain.GraphRun{running, failed} {
		if err := s.CreateGraphRun(ctx, run); err != nil {
			t.Fatalf("CreateGraphRun: %v", err)
		}
	}
	got, err := s.ListGraphRunsByStatus(ctx, domain.GraphRunStatusRunning)
	if err != nil {
		t.Fatalf("ListGraphRunsByStatus: %v", err)
	}
	if len(got) != 1 || got[0].ID != running.ID {
		t.Errorf("running runs = %+v, want only %s", got, running.ID)
	}
}
