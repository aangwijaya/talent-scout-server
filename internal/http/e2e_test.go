package httpapi_test

import (
	"context"
	"encoding/json"
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
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

type e2eFixture struct {
	store  *store.Memory
	router http.Handler
	cfg    config.Config
	mailer *recordingMailer
}

func testGraphModels() config.ModelsConfig {
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

func newE2EFixture(t *testing.T) *e2eFixture {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateJob(context.Background(), domain.Job{
		ID:          applyJobID,
		Title:       "Senior Frontend Engineer",
		Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
		Status:      domain.JobStatusOpen,
		Rubric: []domain.RubricCriterion{
			{Key: "frontend_depth", Label: "Frontend depth", Description: "Svelte and React", Weight: 3},
			{Key: "system_design", Label: "System design", Description: "Component boundaries", Weight: 2},
			{Key: "testing_quality", Label: "Testing and quality", Description: "Tests and accessibility", Weight: 2},
			{Key: "communication", Label: "Communication", Description: "Clarity", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	cfg := config.Config{
		Version:             "0.1.0",
		LLMMode:             "fake",
		EmailMode:           "log",
		PublicAppURL:        "https://talentscout.vercel.app",
		RecruiterPassword:   "correct horse",
		AuthTokenSecret:     "0123456789abcdef0123456789abcdef",
		SessionTTLHours:     168,
		SessionMaxMinutes:   15,
		SessionMaxQuestions: 15,
	}
	mailer := &recordingMailer{}
	router := httpapi.NewRouter(cfg, httpapi.Deps{
		Store:     st,
		Storage:   storage.NewFake(),
		Extractor: fakeExtractor{text: "Senior Svelte engineer with state management experience."},
		Mailer:    mailer,
		LLM:       llm.NewFake(graph.FakeReplies),
		Models:    testGraphModels(),
		Registry:  session.NewRegistry(),
	})
	return &e2eFixture{store: st, router: router, cfg: cfg, mailer: mailer}
}

func (f *e2eFixture) do(t *testing.T, method, target, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func waitUntil(t *testing.T, timeout time.Duration, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func assistantMessages(t *testing.T, st *store.Memory, sessionID string) int {
	t.Helper()
	messages, err := st.ListMessagesBySession(context.Background(), sessionID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	count := 0
	for _, message := range messages {
		if message.Role == domain.MessageRoleAssistant {
			count++
		}
	}
	return count
}

func TestBackendEndToEndApplyStartChatScoreDecision(t *testing.T) {
	f := newE2EFixture(t)
	validator := conformance.New(t)

	apply := applyRequest(t, "budi@example.com")
	applyRec := httptest.NewRecorder()
	f.router.ServeHTTP(applyRec, apply)
	if applyRec.Code != http.StatusCreated {
		t.Fatalf("apply status = %d, want 201 (body: %s)", applyRec.Code, applyRec.Body.String())
	}
	validator.ValidateResponse(t, apply, applyRec.Result())
	var applied struct {
		ApplicationID string `json:"application_id"`
	}
	if err := json.Unmarshal(applyRec.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply: %v", err)
	}
	if f.mailer.link == "" {
		t.Fatal("apply did not send an invite link")
	}
	token := strings.TrimPrefix(f.mailer.link, f.cfg.PublicAppURL+"/s/")
	if token == f.mailer.link {
		t.Fatalf("invite link %q is not a /s/{token} URL", f.mailer.link)
	}

	sess, err := f.store.GetSessionByTokenHash(context.Background(), session.HashToken(token))
	if err != nil {
		t.Fatalf("GetSessionByTokenHash: %v", err)
	}

	start := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+token+"/start", nil)
	startRec := httptest.NewRecorder()
	f.router.ServeHTTP(startRec, start)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body: %s)", startRec.Code, startRec.Body.String())
	}
	validator.ValidateResponse(t, start, startRec.Result())

	waitUntil(t, 5*time.Second, "the opening question", func() bool {
		return assistantMessages(t, f.store, sess.ID) >= 1
	})

	retry := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+token+"/retry", nil)
	retryRec := httptest.NewRecorder()
	f.router.ServeHTTP(retryRec, retry)
	if retryRec.Code != http.StatusConflict {
		t.Fatalf("retry with nothing queued status = %d, want 409", retryRec.Code)
	}
	validator.ValidateResponse(t, retry, retryRec.Result())

	answers := []string{
		"I moved the state into a separate store so each page stayed small and easy to test.",
		"Props were crossing four layers, and I explained the tradeoff to the team before we changed it.",
	}
	const totalAnswers = 9
	for i := 0; i < totalAnswers; i++ {
		answer := answers[i%len(answers)]
		rec := submitAnswerEventually(t, f, token, answer)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("answer %d status = %d, want 202 (body: %s)", i+1, rec.Code, rec.Body.String())
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+token+"/messages", strings.NewReader(`{"content":"`+answer+`"}`))
		validator.ValidateResponse(t, req, rec.Result())

		if i < totalAnswers-1 {
			want := i + 2
			waitUntil(t, 5*time.Second, "the next interviewer question", func() bool {
				return assistantMessages(t, f.store, sess.ID) >= want
			})
		}
	}

	waitUntil(t, 5*time.Second, "the application to reach review", func() bool {
		app, err := f.store.GetApplication(context.Background(), applied.ApplicationID)
		return err == nil && app.Status == domain.ApplicationStatusReview
	})

	scores, err := f.store.ListScoresByApplication(context.Background(), applied.ApplicationID)
	if err != nil {
		t.Fatalf("ListScoresByApplication: %v", err)
	}
	if len(scores) != 4 {
		t.Fatalf("scores = %d, want 4", len(scores))
	}
	for _, score := range scores {
		if score.Status != domain.ScoreStatusValid || score.Score == nil || len(score.Evidence) == 0 {
			t.Errorf("%s = %+v, want a valid score with evidence", score.RubricKey, score)
		}
	}

	run, err := f.store.GetGraphRunBySession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetGraphRunBySession: %v", err)
	}
	if run.Status != domain.GraphRunStatusSucceeded {
		t.Fatalf("run status = %q, want succeeded", run.Status)
	}
	if run.TotalTokensIn <= 0 || run.TotalTokensOut <= 0 || run.CostUSD <= 0 {
		t.Errorf("run totals = %d/%d/%v, want positive", run.TotalTokensIn, run.TotalTokensOut, run.CostUSD)
	}
	assertNormativeRunPrefix(t, run.ID, f.store)

	access := login(t, f.router)
	snapshotReq := httptest.NewRequest(http.MethodGet, "/api/v1/graph-runs/"+run.ID, nil)
	snapshotReq.Header.Set("Authorization", "Bearer "+access)
	snapshotRec := httptest.NewRecorder()
	f.router.ServeHTTP(snapshotRec, snapshotReq)
	if snapshotRec.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200 (body: %s)", snapshotRec.Code, snapshotRec.Body.String())
	}
	validator.ValidateResponse(t, snapshotReq, snapshotRec.Result())
	var snapshot struct {
		LastSeq int64 `json:"last_seq"`
		Nodes   []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(snapshotRec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.LastSeq == 0 {
		t.Error("snapshot last_seq = 0, want the completed run's seq")
	}
	statuses := make(map[string]string, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		statuses[node.ID] = node.Status
	}
	for _, nodeID := range []string{"planner", "plan_check", "interviewer", "judge", "guard", "scorer", "evidence_check"} {
		if statuses[nodeID] != "succeeded" {
			t.Errorf("%s status = %q, want succeeded", nodeID, statuses[nodeID])
		}
	}
	if statuses["recruiter_gate"] != "pending" {
		t.Errorf("recruiter_gate status = %q, want pending (the decision is still open)", statuses["recruiter_gate"])
	}

	decision := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+applied.ApplicationID+"/decision",
		strings.NewReader(`{"decision":"shortlist","note":"strong evidence"}`))
	decision.Header.Set("Authorization", "Bearer "+access)
	decisionRec := httptest.NewRecorder()
	f.router.ServeHTTP(decisionRec, decision)
	if decisionRec.Code != http.StatusOK {
		t.Fatalf("decision status = %d, want 200 (body: %s)", decisionRec.Code, decisionRec.Body.String())
	}
	validator.ValidateResponse(t, decision, decisionRec.Result())

	app, err := f.store.GetApplication(context.Background(), applied.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.Status != domain.ApplicationStatusShortlisted {
		t.Errorf("application status = %q, want shortlisted", app.Status)
	}
}

func submitAnswerEventually(t *testing.T, f *e2eFixture, token, answer string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := f.do(t, http.MethodPost, "/api/v1/sessions/"+token+"/messages", `{"content":"`+answer+`"}`, "")
		if rec.Code != http.StatusConflict || time.Now().After(deadline) {
			return rec
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertNormativeRunPrefix(t *testing.T, runID string, st *store.Memory) {
	t.Helper()
	events, err := st.ListNodeEvents(context.Background(), runID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	got := make([]string, 0, len(events))
	for _, event := range events {
		if strings.HasPrefix(event.Type, "session.") || event.Type == graph.EventMessageDelta {
			continue
		}
		got = append(got, event.Type)
	}
	want := []string{
		graph.EventRunStarted,
		graph.EventNodeStarted, graph.EventNodeCompleted,
		graph.EventEdgeTraversed,
		graph.EventNodeStarted, graph.EventNodeCompleted,
		graph.EventCheckPassed,
		graph.EventTopicChanged,
		graph.EventEdgeTraversed,
		graph.EventNodeStarted, graph.EventMessageCompleted, graph.EventNodeCompleted,
	}
	if len(got) < len(want) {
		t.Fatalf("run events = %v, want prefix %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("run event[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got[len(got)-1] != graph.EventRunCompleted {
		t.Errorf("last run event = %q, want run.completed", got[len(got)-1])
	}
}
