package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func TestMemoryCountApplicationsByJob(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	jobID := domain.NewID()

	for i, status := range []domain.ApplicationStatus{
		domain.ApplicationStatusInvited,
		domain.ApplicationStatusReview,
		domain.ApplicationStatusReview,
	} {
		app := domain.Application{
			ID:     domain.NewID(),
			JobID:  jobID,
			Name:   "Applicant",
			Email:  "applicant" + string(rune('a'+i)) + "@example.com",
			Status: status,
		}
		if err := s.CreateApplication(ctx, app); err != nil {
			t.Fatalf("CreateApplication %d: %v", i, err)
		}
	}
	other := domain.Application{
		ID:     domain.NewID(),
		JobID:  domain.NewID(),
		Name:   "Other",
		Email:  "other@example.com",
		Status: domain.ApplicationStatusInvited,
	}
	if err := s.CreateApplication(ctx, other); err != nil {
		t.Fatalf("CreateApplication other: %v", err)
	}

	got, err := s.CountApplicationsByJob(ctx, jobID)
	if err != nil {
		t.Fatalf("CountApplicationsByJob: %v", err)
	}
	if got != 3 {
		t.Fatalf("CountApplicationsByJob = %d, want 3", got)
	}

	got, err = s.CountApplicationsByJob(ctx, domain.NewID())
	if err != nil {
		t.Fatalf("CountApplicationsByJob empty: %v", err)
	}
	if got != 0 {
		t.Fatalf("CountApplicationsByJob empty = %d, want 0", got)
	}
}

func TestMemoryResendInviteRevokesInvitedSession(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	jobID := domain.NewID()
	app := domain.Application{
		ID:     domain.NewID(),
		JobID:  jobID,
		Name:   "Budi",
		Email:  "budi@example.com",
		Status: domain.ApplicationStatusInvited,
	}
	old := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     "old-hash",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
	}
	if err := s.CreateApplicationWithSession(ctx, app, old); err != nil {
		t.Fatalf("CreateApplicationWithSession: %v", err)
	}

	next := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     "new-hash",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(2 * time.Hour).UTC(),
	}
	if err := s.ResendInvite(ctx, &old.ID, next); err != nil {
		t.Fatalf("ResendInvite: %v", err)
	}

	gotOld, err := s.GetSession(ctx, old.ID)
	if err != nil {
		t.Fatalf("GetSession old: %v", err)
	}
	if gotOld.Status != domain.SessionStatusRevoked {
		t.Fatalf("old session status = %q, want revoked", gotOld.Status)
	}
	gotNext, err := s.GetSession(ctx, next.ID)
	if err != nil {
		t.Fatalf("GetSession next: %v", err)
	}
	if gotNext.Status != domain.SessionStatusInvited || gotNext.TokenHash != "new-hash" {
		t.Fatalf("new session = %+v", gotNext)
	}
	gotApp, err := s.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if gotApp.Status != domain.ApplicationStatusInvited {
		t.Fatalf("application status = %q, want invited", gotApp.Status)
	}
}

func TestMemoryResendInviteKeepsTerminalSession(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	jobID := domain.NewID()
	app := domain.Application{
		ID:     domain.NewID(),
		JobID:  jobID,
		Name:   "Budi",
		Email:  "budi@example.com",
		Status: domain.ApplicationStatusFailed,
	}
	old := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     "old-hash",
		Status:        domain.SessionStatusCompleted,
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
	}
	if err := s.CreateApplicationWithSession(ctx, app, old); err != nil {
		t.Fatalf("CreateApplicationWithSession: %v", err)
	}

	next := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     "new-hash",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(2 * time.Hour).UTC(),
	}
	if err := s.ResendInvite(ctx, nil, next); err != nil {
		t.Fatalf("ResendInvite: %v", err)
	}

	gotOld, err := s.GetSession(ctx, old.ID)
	if err != nil {
		t.Fatalf("GetSession old: %v", err)
	}
	if gotOld.Status != domain.SessionStatusCompleted {
		t.Fatalf("old session status = %q, want completed", gotOld.Status)
	}
	gotApp, err := s.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if gotApp.Status != domain.ApplicationStatusInvited {
		t.Fatalf("application status = %q, want invited", gotApp.Status)
	}
}

func TestMemoryResendInviteConflictDoesNotInsert(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	jobID := domain.NewID()
	app := domain.Application{
		ID:     domain.NewID(),
		JobID:  jobID,
		Name:   "Budi",
		Email:  "budi@example.com",
		Status: domain.ApplicationStatusInvited,
	}
	old := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     "old-hash",
		Status:        domain.SessionStatusActive,
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
	}
	if err := s.CreateApplicationWithSession(ctx, app, old); err != nil {
		t.Fatalf("CreateApplicationWithSession: %v", err)
	}

	next := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     "new-hash",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(2 * time.Hour).UTC(),
	}
	if err := s.ResendInvite(ctx, &old.ID, next); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("ResendInvite = %v, want ErrConflict", err)
	}
	if _, err := s.GetSession(ctx, next.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("new session should not exist, err = %v", err)
	}
	gotOld, _ := s.GetSession(ctx, old.ID)
	if gotOld.Status != domain.SessionStatusActive {
		t.Fatalf("old session status = %q, want active", gotOld.Status)
	}
}

// A resend without a session to revoke is the failed-application path. Once
// the application has left failed (a concurrent resend got there first), a
// second one must conflict instead of minting another live invite.
func TestMemoryResendInviteForFailedApplicationOnlyOnce(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	app := domain.Application{
		ID:     domain.NewID(),
		JobID:  domain.NewID(),
		Name:   "Budi",
		Email:  "budi@example.com",
		Status: domain.ApplicationStatusFailed,
	}
	old := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     "old-hash",
		Status:        domain.SessionStatusCompleted,
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
	}
	if err := s.CreateApplicationWithSession(ctx, app, old); err != nil {
		t.Fatalf("CreateApplicationWithSession: %v", err)
	}
	newInvite := func(hash string) domain.Session {
		return domain.Session{
			ID:            domain.NewID(),
			ApplicationID: app.ID,
			TokenHash:     hash,
			Status:        domain.SessionStatusInvited,
			ExpiresAt:     time.Now().Add(2 * time.Hour).UTC(),
		}
	}
	if err := s.ResendInvite(ctx, nil, newInvite("first-hash")); err != nil {
		t.Fatalf("first ResendInvite: %v", err)
	}
	second := newInvite("second-hash")
	if err := s.ResendInvite(ctx, nil, second); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second ResendInvite = %v, want ErrConflict", err)
	}
	if _, err := s.GetSession(ctx, second.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second session was stored (GetSession err = %v)", err)
	}
}
