// Package e2e drives the server's HTTP API through a full screening:
// apply with a sample CV, open the session link, start, answer, score, and
// record the recruiter decision. The fake-mode test runs in plain `go test`;
// the live OpenRouter variant is behind the `live` build tag.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/cv"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// defaultAnswers are substantive, made-up answers that cover all four rubric
// criteria of the seed job. The runner reads them in order.
var defaultAnswers = []string{
	"I rebuilt the dashboard in SvelteKit and moved shared state into small stores. I measured first load with Lighthouse before and after, improving it from 4.2 seconds to 1.1 seconds.",
	"I split the page into components by ownership and kept server data in a loader while UI state stayed local. I documented the API contract and the error states with the backend team.",
	"For an interactive form I wrote Vitest unit tests for validation and Playwright tests for the main flow. I also checked keyboard navigation and screen reader labels in an accessibility pass.",
	"When two teams disagreed about where state should live, I wrote a one-page trade-off note, compared the options, and we chose the smaller store because it was easier to test.",
	"To reduce prop drilling I lifted only the shared filter state into a store and kept the rest local. That made each component boundary clearer and cut unnecessary re-renders.",
	"I profiled with the browser performance panel, found that a large list re-rendered on every keystroke, and added memoized rows plus a debounced query.",
	"The API returned inconsistent error shapes, so I added a small client adapter and contract tests shared by web and mobile. That kept the UI code independent of backend changes.",
	"I explain decisions in design reviews with a short problem statement, the options I considered, and the measured outcome. I also mentor teammates through pairing and code reviews.",
}

// thinAnswers are deliberately vague: a real scorer may fail to find a verbatim
// quote, which gives the evidence-check retry a chance to appear in a live run.
var thinAnswers = []string{
	"It was fine, mostly.",
	"We talked about it and it worked out.",
	"I cannot remember the details.",
	"Someone else handled that part.",
	"It depends on the project.",
}

type recordingMailer struct {
	link string
	to   string
	name string
}

func (m *recordingMailer) SendInvite(_ context.Context, to, name, link string) error {
	m.to = to
	m.name = name
	m.link = link
	return nil
}

// Harness runs the real router in-process against an in-memory store.
type Harness struct {
	Root   string
	Cfg    config.Config
	Store  *store.Memory
	Router http.Handler
	Mailer *recordingMailer
}

type HarnessOptions struct {
	Live    bool
	BaseURL string
	APIKey  string
	// Client overrides the OpenRouter HTTP client (live tests use a recorder).
	Client *http.Client
}

// Result is everything the flow produced, for assertions and fixture capture.
type Result struct {
	ApplicationID string
	SessionID     string
	RunID         string
	CVPath        string
	Token         string
	Messages      []domain.Message
	Topics        []domain.Topic
	Scores        []domain.Score
	Run           domain.GraphRun
	Events        []domain.NodeEvent
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func NewHarness(t *testing.T, opts HarnessOptions) *Harness {
	t.Helper()
	root := repoRoot()

	var job domain.Job
	raw, err := os.ReadFile(filepath.Join(root, "config", "seed-job.yaml"))
	if err != nil {
		t.Fatalf("read seed job: %v", err)
	}
	if err := yaml.Unmarshal(raw, &job); err != nil {
		t.Fatalf("parse seed job: %v", err)
	}

	models, err := config.LoadModels(filepath.Join(root, "config", "models.yaml"))
	if err != nil {
		t.Fatalf("load models: %v", err)
	}

	cfg := config.Config{
		Version:             "0.1.0",
		LLMMode:             "fake",
		EmailMode:           "log",
		PublicAppURL:        "http://localhost:5173",
		FrontendOrigins:     []string{"http://localhost:5173"},
		RecruiterPassword:   "e2e-password",
		AuthTokenSecret:     "0123456789abcdef0123456789abcdef",
		SessionTTLHours:     168,
		SessionMaxMinutes:   15,
		SessionMaxQuestions: 15,
		LLMBaseURL:          "https://openrouter.ai/api/v1",
	}

	st := store.NewMemory()
	if err := st.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	var client llm.Client
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = cfg.LLMBaseURL
	}
	if opts.Live {
		cfg.LLMMode = "openrouter"
		if opts.APIKey == "" {
			t.Fatal("live harness needs an OpenRouter API key")
		}
		client = llm.NewOpenRouter(baseURL, opts.APIKey, opts.Client)
	} else {
		client = llm.NewFake(graph.FakeReplies)
	}

	mailer := &recordingMailer{}
	router := httpapi.NewRouter(cfg, httpapi.Deps{
		Store:     st,
		Storage:   storage.NewFake(),
		Extractor: cv.NewPDF(),
		Mailer:    mailer,
		LLM:       client,
		Models:    models,
		Registry:  session.NewRegistry(),
	})

	return &Harness{
		Root:   root,
		Cfg:    cfg,
		Store:  st,
		Router: router,
		Mailer: mailer,
	}
}

func (h *Harness) do(t *testing.T, method, target string, body io.Reader, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.Router.ServeHTTP(rec, req)
	return rec
}

func (h *Harness) apply(t *testing.T, cvPath, name, email string) string {
	t.Helper()
	cvBytes, err := os.ReadFile(cvPath)
	if err != nil {
		t.Fatalf("read CV: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("name", name); err != nil {
		t.Fatalf("write name: %v", err)
	}
	if err := mw.WriteField("email", email); err != nil {
		t.Fatalf("write email: %v", err)
	}
	fw, err := mw.CreateFormFile("cv", filepath.Base(cvPath))
	if err != nil {
		t.Fatalf("create cv field: %v", err)
	}
	if _, err := fw.Write(cvBytes); err != nil {
		t.Fatalf("write cv: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+h.seedJobID(t)+"/applications", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.RemoteAddr = "198.51.100.7:1234"
	rec := httptest.NewRecorder()
	h.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}

	var applied struct {
		ApplicationID string `json:"application_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply: %v", err)
	}
	if h.Mailer.link == "" {
		t.Fatal("apply did not deliver an invite link")
	}
	return applied.ApplicationID
}

func (h *Harness) seedJobID(t *testing.T) string {
	t.Helper()
	jobs, err := h.Store.ListJobs(context.Background())
	if err != nil || len(jobs) == 0 {
		t.Fatalf("list seeded jobs: %v", err)
	}
	return jobs[0].ID
}

func tokenFromLink(t *testing.T, link, publicAppURL string) string {
	t.Helper()
	prefix := publicAppURL + "/s/"
	if !strings.HasPrefix(link, prefix) {
		t.Fatalf("invite link %q is not a %s URL", link, prefix)
	}
	token := strings.TrimPrefix(link, prefix)
	if token == "" || strings.Contains(token, "/") {
		t.Fatalf("invite link %q has no single-use token", link)
	}
	return token
}

func (h *Harness) login(t *testing.T) string {
	t.Helper()
	body := strings.NewReader(`{"password":"` + h.Cfg.RecruiterPassword + `"}`)
	rec := h.do(t, http.MethodPost, "/api/v1/auth/login", body, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return login.AccessToken
}

func (h *Harness) waitUntil(t *testing.T, timeout time.Duration, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Run drives the flow and returns the persisted evidence. The timeout applies
// to one assistant step; live runs pass a larger value than fake runs.
func (h *Harness) Run(t *testing.T, cvPath string, answers []string, stepTimeout time.Duration) *Result {
	t.Helper()
	if !filepath.IsAbs(cvPath) {
		cvPath = filepath.Join(h.Root, cvPath)
	}
	result := &Result{CVPath: cvPath}
	result.ApplicationID = h.apply(t, cvPath, "E2E Applicant", "applicant@example.com")
	result.Token = tokenFromLink(t, h.Mailer.link, h.Cfg.PublicAppURL)

	app, err := h.Store.GetApplication(context.Background(), result.ApplicationID)
	if err != nil {
		t.Fatalf("get application: %v", err)
	}
	sess, err := h.Store.GetSessionByTokenHash(context.Background(), session.HashToken(result.Token))
	if err != nil {
		t.Fatalf("get session by token: %v", err)
	}
	result.SessionID = sess.ID

	detail := h.do(t, http.MethodGet, "/api/v1/sessions/"+result.Token, nil, "")
	if detail.Code != http.StatusOK {
		t.Fatalf("session detail status = %d (body: %s)", detail.Code, detail.Body.String())
	}

	start := h.do(t, http.MethodPost, "/api/v1/sessions/"+result.Token+"/start", nil, "")
	if start.Code != http.StatusOK {
		t.Fatalf("start status = %d (body: %s)", start.Code, start.Body.String())
	}

	if !h.waitForAssistant(t, result.ApplicationID, result.SessionID, 1, stepTimeout) {
		return h.failResult(t, result)
	}
	answered := 0
	for answered < 15 {
		if h.finished(t, result.ApplicationID) {
			break
		}
		if len(answers) == 0 {
			t.Fatal("no answers supplied")
		}
		if !h.waitForAssistant(t, result.ApplicationID, result.SessionID, answered+1, stepTimeout) {
			break
		}
		if h.finished(t, result.ApplicationID) {
			break
		}

		answer := answers[answered%len(answers)]
		body := strings.NewReader(fmt.Sprintf(`{"content":%q}`, answer))
		rec := h.do(t, http.MethodPost, "/api/v1/sessions/"+result.Token+"/messages", body, "")
		switch rec.Code {
		case http.StatusAccepted:
			answered++
		case http.StatusConflict:
			time.Sleep(50 * time.Millisecond)
		case http.StatusGone:
			// The session finished while the answer was in flight.
			answered = 15
		default:
			t.Fatalf("answer %d status = %d (body: %s)", answered+1, rec.Code, rec.Body.String())
		}
	}

	h.waitUntil(t, stepTimeout, "the application to leave scoring", func() bool {
		got, err := h.Store.GetApplication(context.Background(), result.ApplicationID)
		return err == nil && got.Status != domain.ApplicationStatusInvited &&
			got.Status != domain.ApplicationStatusInSession &&
			got.Status != domain.ApplicationStatusScoring
	})

	finalApp, err := h.Store.GetApplication(context.Background(), result.ApplicationID)
	if err != nil {
		t.Fatalf("get final application: %v", err)
	}
	if finalApp.Status == domain.ApplicationStatusFailed {
		result.Run, _ = h.Store.GetGraphRunBySession(context.Background(), result.SessionID)
		t.Fatalf("application failed (run error: %v)", result.Run.Error)
	}
	if finalApp.Status != domain.ApplicationStatusReview {
		t.Fatalf("application status = %q, want review", finalApp.Status)
	}

	result.Messages, err = h.Store.ListMessagesBySession(context.Background(), result.SessionID, false)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	result.Topics, err = h.Store.ListTopicsBySession(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("list topics: %v", err)
	}
	result.Scores, err = h.Store.ListScoresByApplication(context.Background(), result.ApplicationID)
	if err != nil {
		t.Fatalf("list scores: %v", err)
	}
	result.Run, err = h.Store.GetGraphRunBySession(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("get graph run: %v", err)
	}
	result.RunID = result.Run.ID
	result.Events, err = h.Store.ListNodeEvents(context.Background(), result.RunID, 0)
	if err != nil {
		t.Fatalf("list node events: %v", err)
	}

	if len(result.Scores) != len(appJobRubric(t, h)) {
		t.Fatalf("scores = %d, want one per rubric criterion", len(result.Scores))
	}
	if result.Run.Status != domain.GraphRunStatusSucceeded {
		t.Fatalf("run status = %q, want succeeded (events: %s)", result.Run.Status, eventSummary(result.Events))
	}
	if result.Run.TotalTokensIn <= 0 || result.Run.TotalTokensOut <= 0 || result.Run.CostUSD <= 0 {
		t.Fatalf("run totals = %d/%d/%v, want positive token counts and cost",
			result.Run.TotalTokensIn, result.Run.TotalTokensOut, result.Run.CostUSD)
	}
	if len(result.Topics) < 3 || len(result.Topics) > 5 {
		t.Errorf("topics = %d, want 3-5", len(result.Topics))
	}

	access := h.login(t)
	decisionBody := strings.NewReader(`{"decision":"shortlist","note":"E2E: evidence reviewed"}`)
	decision := h.do(t, http.MethodPost, "/api/v1/applications/"+result.ApplicationID+"/decision", decisionBody, access)
	if decision.Code != http.StatusOK {
		t.Fatalf("decision status = %d (body: %s)", decision.Code, decision.Body.String())
	}
	app, err = h.Store.GetApplication(context.Background(), result.ApplicationID)
	if err != nil {
		t.Fatalf("get decided application: %v", err)
	}
	if app.Status != domain.ApplicationStatusShortlisted {
		t.Errorf("application status = %q, want shortlisted", app.Status)
	}
	return result
}

func (h *Harness) finished(t *testing.T, appID string) bool {
	t.Helper()
	app, err := h.Store.GetApplication(context.Background(), appID)
	if err != nil {
		return false
	}
	switch app.Status {
	case domain.ApplicationStatusReview, domain.ApplicationStatusShortlisted,
		domain.ApplicationStatusRejected, domain.ApplicationStatusFailed:
		return true
	default:
		return false
	}
}

func (h *Harness) waitForAssistant(t *testing.T, appID, sessionID string, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		messages, err := h.Store.ListMessagesBySession(context.Background(), sessionID, false)
		if err == nil {
			count := 0
			for _, message := range messages {
				if message.Role == domain.MessageRoleAssistant {
					count++
				}
			}
			if count >= want {
				return true
			}
		}
		if h.finished(t, appID) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}

	run, _ := h.Store.GetGraphRunBySession(context.Background(), sessionID)
	events, _ := h.Store.ListNodeEvents(context.Background(), run.ID, 0)
	t.Fatalf("timed out waiting for assistant message %d (run status %q, error %v, events: %s)",
		want, run.Status, run.Error, eventSummary(events))
	return false
}

func (h *Harness) failResult(t *testing.T, result *Result) *Result {
	t.Helper()
	result.Run, _ = h.Store.GetGraphRunBySession(context.Background(), result.SessionID)
	if result.Run.ID != "" {
		result.Events, _ = h.Store.ListNodeEvents(context.Background(), result.Run.ID, 0)
	}
	t.Fatalf("session ended before the opening question (run status %q, error %v, events: %s)",
		result.Run.Status, result.Run.Error, eventSummary(result.Events))
	return result
}

func appJobRubric(t *testing.T, h *Harness) []domain.RubricCriterion {
	t.Helper()
	jobs, err := h.Store.ListJobs(context.Background())
	if err != nil || len(jobs) == 0 {
		t.Fatalf("list jobs: %v", err)
	}
	return jobs[0].Rubric
}

func eventSummary(events []domain.NodeEvent) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		if event.NodeID != nil {
			parts = append(parts, event.Type+"("+*event.NodeID+")")
			continue
		}
		parts = append(parts, event.Type)
	}
	return strings.Join(parts, " -> ")
}
