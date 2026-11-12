package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/conformance"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
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

func TestListMessagesMatchesContract(t *testing.T) {
	router, raw := sessionRouter(t)
	validator := conformance.New(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+raw+"/messages", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())
}

func TestSessionEventsMatchesContract(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	jobID := "7c9e6679-0000-4000-8000-000000000031"
	appID := "7c9e6679-0000-4000-8000-000000000032"
	sessionID := "7c9e6679-0000-4000-8000-000000000033"
	runID := "7c9e6679-0000-4000-8000-000000000034"
	if err := st.CreateJob(ctx, domain.Job{
		ID:          jobID,
		Title:       "Senior Frontend Engineer",
		Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
		Status:      domain.JobStatusOpen,
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := st.CreateApplication(ctx, domain.Application{
		ID: appID, JobID: jobID, Name: "Budi", Email: "budi@example.com", Status: domain.ApplicationStatusInSession,
	}); err != nil {
		t.Fatalf("seed application: %v", err)
	}
	raw, hash, err := session.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	started := time.Now().UTC()
	if err := st.CreateSession(ctx, domain.Session{
		ID: sessionID, ApplicationID: appID, TokenHash: hash,
		Status: domain.SessionStatusActive, ExpiresAt: time.Now().Add(24 * time.Hour), StartedAt: &started,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := st.CreateGraphRun(ctx, domain.GraphRun{ID: runID, SessionID: sessionID, Status: domain.GraphRunStatusFailed}); err != nil {
		t.Fatalf("seed graph run: %v", err)
	}
	if _, err := st.AppendNodeEvent(ctx, domain.NodeEvent{
		RunID: runID, Type: graph.EventSessionUpdated, Payload: []byte(`{"status":"active","question_count":1,"max_questions":15,"current_topic":null}`),
	}); err != nil {
		t.Fatalf("append session.updated: %v", err)
	}
	if _, err := st.AppendNodeEvent(ctx, domain.NodeEvent{
		RunID: runID, Type: graph.EventSessionEnded, Payload: []byte(`{"finish_reason":"failed"}`),
	}); err != nil {
		t.Fatalf("append session.ended: %v", err)
	}

	cfg := graphRunsConfig()
	router := httpapi.NewRouter(cfg, httpapi.Deps{Store: st, LLM: llm.NewFake(graph.FakeReplies)})
	validator := conformance.New(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+raw+"/events", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())
	body := rec.Body.String()
	for _, want := range []string{"event: session.updated", "event: session.ended"} {
		if !strings.Contains(body, want) {
			t.Errorf("session SSE body missing %q:\n%s", want, body)
		}
	}
}
