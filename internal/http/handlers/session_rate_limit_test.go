package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/conformance"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/session"
)

func TestSendMessageRateLimitPerSession(t *testing.T) {
	cfg := config.Config{
		SessionMaxMinutes:    15,
		SessionMaxQuestions:  15,
		MessageMaxPerWindow:  1,
		MessageWindowMinutes: 10,
	}
	f := newChatFixtureWithConfig(t, false, nil, llm.NewFake(graph.FakeReplies), cfg)

	rec := httptest.NewRecorder()
	f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	waitManager(t, f.manager, f.session.ID)

	rec = postAnswer(t, f, "I optimized rendering.")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first message status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}
	waitManager(t, f.manager, f.session.ID)

	payload, err := json.Marshal(map[string]string{"content": "I split the state into a store."})
	if err != nil {
		t.Fatalf("marshal answer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+f.raw+"/messages", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", f.raw)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec = httptest.NewRecorder()
	f.handler.SendMessage(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second message status = %d, want 429 (body: %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details any    `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != "rate_limited" {
		t.Errorf("error.code = %q, want rate_limited", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Error("error.message is empty")
	}

	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < 1 || retry > 10*60 {
		t.Errorf("Retry-After = %q, want 1..600 seconds", rec.Header().Get("Retry-After"))
	}

	validator := conformance.New(t)
	validator.ValidateResponse(t, req, rec.Result())
}

// The message budget belongs to one session: a second session keeps its own.
func TestSendMessageRateLimitIsKeyedBySession(t *testing.T) {
	cfg := config.Config{
		SessionMaxMinutes:    15,
		SessionMaxQuestions:  15,
		MessageMaxPerWindow:  1,
		MessageWindowMinutes: 10,
	}
	f := newChatFixtureWithConfig(t, false, nil, llm.NewFake(graph.FakeReplies), cfg)
	ctx := context.Background()

	app := domain.Application{
		ID: "7c9e6679-0000-4000-8000-000000000302", JobID: f.job.ID,
		Name: "Sari", Email: "sari@example.com", CVText: "Senior React engineer.",
		Status: domain.ApplicationStatusInvited,
	}
	if err := f.store.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	rawB, hashB, err := session.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	sessB := domain.Session{
		ID: "7c9e6679-0000-4000-8000-000000000303", ApplicationID: app.ID,
		TokenHash: hashB, Status: domain.SessionStatusInvited, ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := f.store.CreateSession(ctx, sessB); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	for _, s := range []struct{ raw, id string }{{f.raw, f.session.ID}, {rawB, sessB.ID}} {
		rec := httptest.NewRecorder()
		f.handler.Start(rec, chatRequest(http.MethodPost, s.raw, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("start %s status = %d, want 200 (body: %s)", s.id, rec.Code, rec.Body.String())
		}
		waitManager(t, f.manager, s.id)
	}

	send := func(raw, content string) *httptest.ResponseRecorder {
		payload, err := json.Marshal(map[string]string{"content": content})
		if err != nil {
			t.Fatalf("marshal answer: %v", err)
		}
		rec := httptest.NewRecorder()
		f.handler.SendMessage(rec, chatRequest(http.MethodPost, raw, string(payload)))
		return rec
	}

	if rec := send(f.raw, "I optimized rendering."); rec.Code != http.StatusAccepted {
		t.Fatalf("session A first message status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}
	waitManager(t, f.manager, f.session.ID)
	if rec := send(f.raw, "I split the state into a store."); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("session A second message status = %d, want 429 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := send(rawB, "I wrote the component tests first."); rec.Code != http.StatusAccepted {
		t.Fatalf("session B first message status = %d, want 202: the limit must be per session (body: %s)", rec.Code, rec.Body.String())
	}
	waitManager(t, f.manager, sessB.ID)
}
