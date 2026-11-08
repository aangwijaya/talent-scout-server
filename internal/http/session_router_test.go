package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/conformance"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func sessionRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateJob(context.Background(), domain.Job{
		ID:          "7c9e6679-0000-4000-8000-000000000021",
		Title:       "Senior Frontend Engineer",
		Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
		Status:      domain.JobStatusOpen,
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := st.CreateApplication(context.Background(), domain.Application{
		ID:     "7c9e6679-0000-4000-8000-000000000022",
		JobID:  "7c9e6679-0000-4000-8000-000000000021",
		Name:   "Budi",
		Email:  "budi@example.com",
		Status: domain.ApplicationStatusInvited,
	}); err != nil {
		t.Fatalf("seed application: %v", err)
	}
	raw, hash, err := session.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	if err := st.CreateSession(context.Background(), domain.Session{
		ID:            "7c9e6679-0000-4000-8000-000000000023",
		ApplicationID: "7c9e6679-0000-4000-8000-000000000022",
		TokenHash:     hash,
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	cfg := config.Config{
		Version:             "0.1.0",
		LLMMode:             "fake",
		EmailMode:           "log",
		PublicAppURL:        "https://talentscout.vercel.app",
		SessionTTLHours:     168,
		SessionMaxMinutes:   15,
		SessionMaxQuestions: 15,
	}
	return httpapi.NewRouter(cfg, httpapi.Deps{Store: st}), raw
}

func TestGetSessionMatchesContract(t *testing.T) {
	router, raw := sessionRouter(t)
	validator := conformance.New(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+raw, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())
}

func TestStartSessionMatchesContract(t *testing.T) {
	router, raw := sessionRouter(t)
	validator := conformance.New(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+raw+"/start", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())
}
