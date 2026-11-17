package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/conformance"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
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
