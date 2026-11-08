package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func sessionCfg() config.Config {
	return config.Config{
		PublicAppURL:        "https://talentscout.vercel.app",
		SessionTTLHours:     168,
		SessionMaxMinutes:   15,
		SessionMaxQuestions: 15,
	}
}

func sessionStore(t *testing.T) (*store.Memory, domain.Session) {
	t.Helper()
	st := store.NewMemory()
	job := domain.Job{ID: "7c9e6679-0000-4000-8000-000000000011", Title: "Senior Frontend Engineer", Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.", Status: domain.JobStatusOpen}
	if err := st.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	app := domain.Application{ID: "7c9e6679-0000-4000-8000-000000000012", JobID: job.ID, Name: "Budi", Email: "budi@example.com", CVText: "text"}
	if err := st.CreateApplication(context.Background(), app); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	return st, domain.Session{ID: "7c9e6679-0000-4000-8000-000000000013", ApplicationID: app.ID, Status: domain.SessionStatusInvited, ExpiresAt: time.Now().Add(24 * time.Hour)}
}

func newSessionHandler(t *testing.T, st store.Store) *handlers.Sessions {
	t.Helper()
	return handlers.NewSessions(sessionCfg(), st)
}

func sessionReq(method, target, token string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func decodeSessionDetail(t *testing.T, rec *httptest.ResponseRecorder) (status string, detail map[string]any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode session detail: %v", err)
	}
	return detail["status"].(string), detail
}

func TestGetSessionInvited(t *testing.T) {
	st, base := sessionStore(t)
	raw, hash, _ := session.NewToken()
	base.TokenHash = hash
	if err := st.CreateSession(context.Background(), base); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := newSessionHandler(t, st)
	rec := httptest.NewRecorder()
	h.Get(rec, sessionReq(http.MethodGet, "/api/v1/sessions/"+raw, raw))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	status, detail := decodeSessionDetail(t, rec)
	if status != "invited" {
		t.Errorf("status = %q, want invited", status)
	}
	if detail["job_title"] != "Senior Frontend Engineer" {
		t.Errorf("job_title = %v", detail["job_title"])
	}
	if detail["applicant_name"] != "Budi" {
		t.Errorf("applicant_name = %v", detail["applicant_name"])
	}
	if detail["started_at"] != nil {
		t.Errorf("started_at = %v, want null", detail["started_at"])
	}
	if detail["deadline_at"] != nil {
		t.Errorf("deadline_at = %v, want null", detail["deadline_at"])
	}
	if detail["topic_count"] != nil {
		t.Errorf("topic_count = %v, want null", detail["topic_count"])
	}
	if detail["question_count"] != float64(0) {
		t.Errorf("question_count = %v, want 0", detail["question_count"])
	}
	if detail["max_questions"] != float64(15) {
		t.Errorf("max_questions = %v, want 15", detail["max_questions"])
	}
	if detail["current_topic"] != nil {
		t.Errorf("current_topic = %v, want null", detail["current_topic"])
	}
}

func TestGetSessionUnknown(t *testing.T) {
	st, _ := sessionStore(t)
	h := newSessionHandler(t, st)

	rec := httptest.NewRecorder()
	h.Get(rec, sessionReq(http.MethodGet, "/api/v1/sessions/unknown", "unknown"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "session_not_found" {
		t.Errorf("code = %q, want session_not_found", code)
	}
}

func TestGetSessionRevoked(t *testing.T) {
	st, base := sessionStore(t)
	raw, hash, _ := session.NewToken()
	base.TokenHash = hash
	base.Status = domain.SessionStatusRevoked
	if err := st.CreateSession(context.Background(), base); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := newSessionHandler(t, st)
	rec := httptest.NewRecorder()
	h.Get(rec, sessionReq(http.MethodGet, "/api/v1/sessions/"+raw, raw))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "session_not_found" {
		t.Errorf("code = %q, want session_not_found", code)
	}
}

func TestGetSessionExpired(t *testing.T) {
	st, base := sessionStore(t)
	raw, hash, _ := session.NewToken()
	base.TokenHash = hash
	base.Status = domain.SessionStatusInvited
	base.ExpiresAt = time.Now().Add(-time.Hour)
	if err := st.CreateSession(context.Background(), base); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := newSessionHandler(t, st)
	rec := httptest.NewRecorder()
	h.Get(rec, sessionReq(http.MethodGet, "/api/v1/sessions/"+raw, raw))

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "session_expired" {
		t.Errorf("code = %q, want session_expired", code)
	}
}

func TestGetSessionCompleted(t *testing.T) {
	st, base := sessionStore(t)
	raw, hash, _ := session.NewToken()
	base.TokenHash = hash
	base.Status = domain.SessionStatusCompleted
	if err := st.CreateSession(context.Background(), base); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := newSessionHandler(t, st)
	rec := httptest.NewRecorder()
	h.Get(rec, sessionReq(http.MethodGet, "/api/v1/sessions/"+raw, raw))

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "session_completed" {
		t.Errorf("code = %q, want session_completed", code)
	}
}

func TestStartSession(t *testing.T) {
	st, base := sessionStore(t)
	raw, hash, _ := session.NewToken()
	base.TokenHash = hash
	if err := st.CreateSession(context.Background(), base); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := newSessionHandler(t, st)
	rec := httptest.NewRecorder()
	h.Start(rec, sessionReq(http.MethodPost, "/api/v1/sessions/"+raw+"/start", raw))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	status, detail := decodeSessionDetail(t, rec)
	if status != "active" {
		t.Errorf("status = %q, want active", status)
	}
	if detail["started_at"] == nil {
		t.Errorf("started_at = nil, want set")
	}
	if detail["deadline_at"] == nil {
		t.Errorf("deadline_at = nil, want set")
	}

	stored, _ := st.GetSession(context.Background(), base.ID)
	if stored.Status != domain.SessionStatusActive {
		t.Errorf("stored status = %q, want active", stored.Status)
	}
	if stored.StartedAt == nil {
		t.Errorf("stored started_at is nil")
	}
}

func TestStartSessionTwice(t *testing.T) {
	st, base := sessionStore(t)
	raw, hash, _ := session.NewToken()
	base.TokenHash = hash
	if err := st.CreateSession(context.Background(), base); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := newSessionHandler(t, st)
	first := httptest.NewRecorder()
	h.Start(first, sessionReq(http.MethodPost, "/api/v1/sessions/"+raw+"/start", raw))
	if first.Code != http.StatusOK {
		t.Fatalf("first start = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	h.Start(second, sessionReq(http.MethodPost, "/api/v1/sessions/"+raw+"/start", raw))
	if second.Code != http.StatusConflict {
		t.Fatalf("second start = %d, want 409 (body: %s)", second.Code, second.Body.String())
	}
	code, _, _ := decodeError(t, second)
	if code != "session_already_started" {
		t.Errorf("code = %q, want session_already_started", code)
	}
}

func TestStartSessionConcurrent(t *testing.T) {
	st, base := sessionStore(t)
	raw, hash, _ := session.NewToken()
	base.TokenHash = hash
	if err := st.CreateSession(context.Background(), base); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := newSessionHandler(t, st)
	const n = 16
	codes := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			rec := httptest.NewRecorder()
			h.Start(rec, sessionReq(http.MethodPost, "/api/v1/sessions/"+raw+"/start", raw))
			codes <- rec.Code
		}()
	}

	var ok, conflict int
	for i := 0; i < n; i++ {
		switch <-codes {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("unexpected start status")
		}
	}
	if ok != 1 || conflict != n-1 {
		t.Fatalf("start statuses: %d ok, %d conflict; want 1 ok, %d conflict", ok, conflict, n-1)
	}
}

func TestStartSessionExpired(t *testing.T) {
	st, base := sessionStore(t)
	raw, hash, _ := session.NewToken()
	base.TokenHash = hash
	base.ExpiresAt = time.Now().Add(-time.Hour)
	if err := st.CreateSession(context.Background(), base); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := newSessionHandler(t, st)
	rec := httptest.NewRecorder()
	h.Start(rec, sessionReq(http.MethodPost, "/api/v1/sessions/"+raw+"/start", raw))

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "session_expired" {
		t.Errorf("code = %q, want session_expired", code)
	}
}

func TestStartSessionCompleted(t *testing.T) {
	st, base := sessionStore(t)
	raw, hash, _ := session.NewToken()
	base.TokenHash = hash
	base.Status = domain.SessionStatusCompleted
	if err := st.CreateSession(context.Background(), base); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := newSessionHandler(t, st)
	rec := httptest.NewRecorder()
	h.Start(rec, sessionReq(http.MethodPost, "/api/v1/sessions/"+raw+"/start", raw))

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "session_completed" {
		t.Errorf("code = %q, want session_completed", code)
	}
}
