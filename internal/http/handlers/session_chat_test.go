package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func chatModels() config.ModelsConfig {
	return config.ModelsConfig{
		Default: config.Defaults{Temperature: 0.3, TimeoutSeconds: 30, MaxRetriesOnHTTPError: 1},
		Nodes: map[string]config.Node{
			"planner":     {Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 800},
			"interviewer": {Model: "z-ai/glm-5.3-flash", MaxTokens: 300, Stream: true},
			"judge":       {Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 200},
			"scorer":      {Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 1200},
		},
		Prices: map[string]config.Price{
			"deepseek/deepseek-v4.1-flash": {In: 0.045, Out: 0.5},
			"z-ai/glm-5.3-flash":           {In: 0.15, Out: 0.5},
		},
	}
}

type chatFixture struct {
	store   *store.Memory
	handler *handlers.Sessions
	manager *graph.SessionManager
	bus     *graph.Bus
	raw     string
	session domain.Session
	job     domain.Job
}

func newChatFixture(t *testing.T, start bool) *chatFixture {
	t.Helper()
	return newChatFixtureWrapped(t, start, nil)
}

func newChatFixtureWrapped(t *testing.T, start bool, wrap func(store.Store) store.Store) *chatFixture {
	t.Helper()
	ctx := context.Background()
	base := store.NewMemory()
	st := store.Store(base)
	job := domain.Job{
		ID:          "7c9e6679-0000-4000-8000-000000000201",
		Title:       "Senior Frontend Engineer",
		Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
		Status:      domain.JobStatusOpen,
		Rubric: []domain.RubricCriterion{
			{Key: "frontend_depth", Label: "Frontend depth", Weight: 3},
			{Key: "system_design", Label: "System design", Weight: 2},
			{Key: "testing_quality", Label: "Testing and quality", Weight: 2},
			{Key: "communication", Label: "Communication", Weight: 1},
		},
	}
	if err := st.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	app := domain.Application{
		ID: "7c9e6679-0000-4000-8000-000000000202", JobID: job.ID,
		Name: "Budi", Email: "budi@example.com", CVText: "Senior Svelte engineer.",
		Status: domain.ApplicationStatusInvited,
	}
	if err := st.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	raw, hash, err := session.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	sess := domain.Session{
		ID: "7c9e6679-0000-4000-8000-000000000203", ApplicationID: app.ID,
		TokenHash: hash, Status: domain.SessionStatusInvited, ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if start {
		sess, err = st.StartSession(ctx, sess.ID, time.Now().UTC())
		if err != nil {
			t.Fatalf("StartSession: %v", err)
		}
	}

	if wrap != nil {
		st = wrap(st)
	}

	cfg := config.Config{SessionMaxMinutes: 15, SessionMaxQuestions: 15}
	bus := graph.NewBus(st)
	manager := graph.NewSessionManager(cfg, st, bus, llm.NewFake(graph.FakeReplies), chatModels(), session.NewRegistry(), nil)
	h := handlers.NewSessions(cfg, st)
	h.SetChat(manager, bus)
	return &chatFixture{store: base, handler: h, manager: manager, bus: bus, raw: raw, session: sess, job: job}
}

func chatRequest(method, token, body string) *http.Request {
	r := httptest.NewRequest(method, "/api/v1/sessions/"+token, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func waitManager(t *testing.T, manager *graph.SessionManager, sessionID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Wait(ctx, sessionID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestStartRunsFakePlannerAndInterviewer(t *testing.T) {
	f := newChatFixture(t, false)

	rec := httptest.NewRecorder()
	f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	waitManager(t, f.manager, f.session.ID)

	topics, err := f.store.ListTopicsBySession(context.Background(), f.session.ID)
	if err != nil {
		t.Fatalf("ListTopicsBySession: %v", err)
	}
	if len(topics) != 3 {
		t.Errorf("topics = %d, want 3 (fake planner must pass plan_check)", len(topics))
	}

	rec = httptest.NewRecorder()
	f.handler.Messages(rec, chatRequest(http.MethodGet, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("messages status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []domain.Message `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Role != domain.MessageRoleAssistant || body.Items[0].Kind != domain.MessageKindOpening {
		t.Errorf("items = %+v, want one opening assistant message", body.Items)
	}
}

func TestMessagesExcludeSystem(t *testing.T) {
	f := newChatFixture(t, true)
	ctx := context.Background()
	for _, message := range []domain.Message{
		{ID: "m-a", SessionID: f.session.ID, Role: domain.MessageRoleAssistant, Kind: domain.MessageKindOpening, Content: "hi"},
		{ID: "m-u", SessionID: f.session.ID, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer, Content: "answer"},
		{ID: "m-s", SessionID: f.session.ID, Role: domain.MessageRoleSystem, Kind: domain.MessageKindQuestion, Content: "hidden"},
	} {
		if err := f.store.CreateMessage(ctx, message); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	rec := httptest.NewRecorder()
	f.handler.Messages(rec, chatRequest(http.MethodGet, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Items []domain.Message `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 {
		t.Errorf("items = %d, want 2 (system excluded)", len(body.Items))
	}
}

func TestSendMessageValidation(t *testing.T) {
	f := newChatFixture(t, true)

	for name, content := range map[string]string{
		"empty":    "",
		"too long": strings.Repeat("a", 2001),
	} {
		t.Run(name, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]string{"content": content})
			rec := httptest.NewRecorder()
			f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			code, _, _ := decodeError(t, rec)
			if code != "validation_failed" {
				t.Errorf("code = %q, want validation_failed", code)
			}
		})
	}
}

func TestSendMessageBeforeStartIsInvalidState(t *testing.T) {
	f := newChatFixture(t, false)
	payload, _ := json.Marshal(map[string]string{"content": "hello"})
	rec := httptest.NewRecorder()
	f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	code, _, _ := decodeError(t, rec)
	if code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", code)
	}
}

func TestSendMessageAcceptsAndAsksNext(t *testing.T) {
	f := newChatFixture(t, false)
	rec := httptest.NewRecorder()
	f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200", rec.Code)
	}
	waitManager(t, f.manager, f.session.ID)

	payload, _ := json.Marshal(map[string]string{"content": "I optimized rendering."})
	rec = httptest.NewRecorder()
	f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Message  domain.Message `json:"message"`
		Awaiting string         `json:"awaiting"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Message.Role != domain.MessageRoleApplicant || body.Message.Kind != domain.MessageKindAnswer {
		t.Errorf("message = %+v, want applicant answer", body.Message)
	}
	if body.Awaiting != "assistant" {
		t.Errorf("awaiting = %q, want assistant", body.Awaiting)
	}
	waitManager(t, f.manager, f.session.ID)

	messages, _ := f.store.ListMessagesBySession(context.Background(), f.session.ID, false)
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want opening, answer, follow_up", len(messages))
	}
	if messages[2].Kind != domain.MessageKindFollowUp {
		t.Errorf("last kind = %q, want follow_up", messages[2].Kind)
	}
}

func TestRetryWithoutFailureIsInvalidState(t *testing.T) {
	f := newChatFixture(t, false)
	rec := httptest.NewRecorder()
	f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200", rec.Code)
	}
	waitManager(t, f.manager, f.session.ID)

	rec = httptest.NewRecorder()
	f.handler.Retry(rec, chatRequest(http.MethodPost, f.raw, ""))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	code, _, _ := decodeError(t, rec)
	if code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", code)
	}
}

func TestSessionEventsReplayFilterAndClose(t *testing.T) {
	f := newChatFixture(t, true)
	ctx := context.Background()
	runID := domain.NewID()
	if err := f.store.CreateGraphRun(ctx, domain.GraphRun{ID: runID, SessionID: f.session.ID, Status: domain.GraphRunStatusRunning}); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	emit := func(eventType string, payload any) {
		t.Helper()
		if _, err := f.bus.Emit(ctx, runID, graph.Event{Type: eventType, Payload: payload}); err != nil {
			t.Fatalf("Emit %s: %v", eventType, err)
		}
	}
	emit(graph.EventSessionUpdated, graph.SessionUpdated{Status: "active", MaxQuestions: 15})
	emit(graph.EventNodeStarted, graph.NodeStarted{Model: domain.StrPtr("m"), InputSummary: "x"})
	emit(graph.EventMessageCompleted, graph.MessageCompleted{Message: domain.Message{ID: "m1", SessionID: f.session.ID, Role: domain.MessageRoleAssistant, Kind: domain.MessageKindOpening, Content: "hi"}})
	emit(graph.EventError, graph.ErrorEvent{Code: "llm_unavailable", Message: "x", Retryable: true})
	emit(graph.EventMessageDelta, graph.MessageDelta{MessageID: "m1", Delta: "ignored"})
	emit(graph.EventSessionEnded, graph.SessionEnded{FinishReason: "completed"})

	rec := httptest.NewRecorder()
	f.handler.Events(rec, chatRequest(http.MethodGet, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"event: session.updated", "event: message.completed", "event: error", "event: session.ended"} {
		if !strings.Contains(body, want) {
			t.Errorf("replay missing %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"event: node.started", "event: message.delta"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("replay must not contain %q:\n%s", unwanted, body)
		}
	}
}

func TestSessionEventsAcceptsInvited(t *testing.T) {
	f := newChatFixture(t, false)
	r := chatRequest(http.MethodGet, f.raw, "")
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		f.handler.Events(rec, r)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Events did not return after the client disconnected")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an invited session", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
}
