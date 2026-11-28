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
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const (
	applicationsJobID     = "7c9e6679-0000-4000-8000-000000000100"
	applicationsOtherID   = "7c9e6679-0000-4000-8000-000000000101"
	applicationsAppID     = "7c9e6679-0000-4000-8000-000000000102"
	applicationsSessionID = "7c9e6679-0000-4000-8000-000000000103"
	applicationsRunID     = "7c9e6679-0000-4000-8000-000000000104"
)

type recordingMailer struct {
	calls int
	to    string
	name  string
	link  string
	err   error
}

func (m *recordingMailer) SendInvite(_ context.Context, to, name, link string) error {
	m.calls++
	m.to = to
	m.name = name
	m.link = link
	return m.err
}

type applicationsFixture struct {
	store        *store.Memory
	storage      *storage.Fake
	audioStorage *storage.Fake
	mailer       *recordingMailer
	router       http.Handler
	token        string
	cfg          config.Config
}

func newApplicationsFixture(t *testing.T, emailMode string) *applicationsFixture {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateJob(context.Background(), domain.Job{
		ID:          applicationsJobID,
		Title:       "Senior Frontend Engineer",
		Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
		Status:      domain.JobStatusOpen,
		Rubric: []domain.RubricCriterion{
			{Key: "frontend_depth", Label: "Frontend depth", Description: "Svelte and React depth.", Weight: 2},
			{Key: "communication", Label: "Communication", Description: "Clarity and collaboration.", Weight: 1},
			{Key: "system_design", Label: "System design", Description: "Component boundaries.", Weight: 2},
		},
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	cfg := config.Config{
		Version:             "0.1.0",
		LLMMode:             "fake",
		EmailMode:           emailMode,
		PublicAppURL:        "https://talentscout-id.vercel.app",
		RecruiterPassword:   "correct horse",
		AuthTokenSecret:     "0123456789abcdef0123456789abcdef",
		SessionTTLHours:     168,
		SessionMaxMinutes:   15,
		SessionMaxQuestions: 15,
	}
	fakeStorage := storage.NewFake()
	audioStorage := storage.NewFake()
	mailer := &recordingMailer{}
	router := httpapi.NewRouter(cfg, httpapi.Deps{Store: st, Storage: fakeStorage, AudioStorage: audioStorage, Mailer: mailer})
	return &applicationsFixture{
		store:        st,
		storage:      fakeStorage,
		audioStorage: audioStorage,
		mailer:       mailer,
		router:       router,
		token:        login(t, router),
		cfg:          cfg,
	}
}

func (f *applicationsFixture) seedApplication(t *testing.T, email string, status domain.ApplicationStatus) domain.Application {
	t.Helper()
	app := domain.Application{
		ID:           domain.NewID(),
		JobID:        applicationsJobID,
		Name:         "Budi",
		Email:        email,
		CVObjectPath: domain.NewID() + ".pdf",
		CVText:       "Senior frontend engineer with Svelte experience.",
		Status:       status,
	}
	if err := f.store.CreateApplication(context.Background(), app); err != nil {
		t.Fatalf("seed application: %v", err)
	}
	return app
}

func (f *applicationsFixture) seedSession(t *testing.T, app domain.Application, status domain.SessionStatus) (domain.Session, string) {
	t.Helper()
	raw, hash, err := session.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	sess := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     hash,
		Status:        status,
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
	}
	if err := f.store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sess, raw
}

func (f *applicationsFixture) do(t *testing.T, method, target, body string, withToken bool) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if withToken {
		req.Header.Set("Authorization", "Bearer "+f.token)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func decodeErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error: %v (body: %s)", err, rec.Body.String())
	}
	return got.Error.Code
}

func TestApplicationsEndpointsRequireBearer(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusReview)

	tests := []struct {
		method string
		target string
		body   string
	}{
		{http.MethodGet, "/api/v1/jobs/" + applicationsJobID + "/applications", ""},
		{http.MethodGet, "/api/v1/applications/" + app.ID, ""},
		{http.MethodGet, "/api/v1/applications/" + app.ID + "/cv", ""},
		{http.MethodPost, "/api/v1/applications/" + app.ID + "/decision", `{"decision":"shortlist"}`},
		{http.MethodPost, "/api/v1/applications/" + app.ID + "/resend-invite", ""},
	}

	for _, tc := range tests {
		rec := f.do(t, tc.method, tc.target, tc.body, false)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", tc.method, tc.target, rec.Code)
		}
		if code := decodeErrorCode(t, rec); code != "unauthorized" {
			t.Errorf("%s %s code = %q, want unauthorized", tc.method, tc.target, code)
		}
	}
}

func TestListApplicationsMatchesContract(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	validator := conformance.New(t)

	review := f.seedApplication(t, "review@example.com", domain.ApplicationStatusReview)
	reviewSession, _ := f.seedSession(t, review, domain.SessionStatusCompleted)
	if err := f.store.UpsertScores(context.Background(), []domain.Score{
		{ApplicationID: review.ID, RubricKey: "frontend_depth", Score: domain.IntPtr(4), Status: domain.ScoreStatusValid, Attempts: 1},
		{ApplicationID: review.ID, RubricKey: "communication", Score: domain.IntPtr(3), Status: domain.ScoreStatusValid, Attempts: 1},
	}); err != nil {
		t.Fatalf("seed scores: %v", err)
	}
	invited := f.seedApplication(t, "invited@example.com", domain.ApplicationStatusInvited)
	f.seedSession(t, invited, domain.SessionStatusInvited)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+applicationsJobID+"/applications", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())

	var got struct {
		Items []struct {
			ID            string   `json:"id"`
			JobID         string   `json:"job_id"`
			Status        string   `json:"status"`
			SessionStatus *string  `json:"session_status"`
			OverallScore  *float64 `json:"overall_score"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(got.Items))
	}
	if got.NextCursor != nil {
		t.Errorf("next_cursor = %v, want null", *got.NextCursor)
	}
	byID := make(map[string]int, len(got.Items))
	for i, item := range got.Items {
		byID[item.ID] = i
		if item.JobID != applicationsJobID {
			t.Errorf("job_id = %q, want %q", item.JobID, applicationsJobID)
		}
	}
	reviewItem := got.Items[byID[review.ID]]
	if reviewItem.SessionStatus == nil || *reviewItem.SessionStatus != "completed" {
		t.Errorf("review session_status = %v, want completed", reviewItem.SessionStatus)
	}
	if reviewItem.OverallScore == nil || *reviewItem.OverallScore != (4*2+3)/3.0 {
		t.Errorf("review overall_score = %v, want %v", reviewItem.OverallScore, (4*2+3)/3.0)
	}
	_ = reviewSession

	filtered := f.do(t, http.MethodGet, "/api/v1/jobs/"+applicationsJobID+"/applications?status=invited", "", true)
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered status = %d, want 200 (body: %s)", filtered.Code, filtered.Body.String())
	}
	if err := json.Unmarshal(filtered.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != invited.ID {
		t.Fatalf("filtered items = %+v, want only %s", got.Items, invited.ID)
	}

	if rec := f.do(t, http.MethodGet, "/api/v1/jobs/"+applicationsJobID+"/applications?status=nope", "", true); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid status = %d, want 400", rec.Code)
	}
	if rec := f.do(t, http.MethodGet, "/api/v1/jobs/not-a-uuid/applications", "", true); rec.Code != http.StatusNotFound {
		t.Errorf("malformed job id = %d, want 404", rec.Code)
	}
	if rec := f.do(t, http.MethodGet, "/api/v1/jobs/"+applicationsOtherID+"/applications", "", true); rec.Code != http.StatusOK {
		t.Errorf("unknown job = %d, want 200 empty list", rec.Code)
	}
}

func TestGetApplicationDetailMatchesContract(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	validator := conformance.New(t)
	ctx := context.Background()

	app := domain.Application{
		ID:           applicationsAppID,
		JobID:        applicationsJobID,
		Name:         "Budi",
		Email:        "budi@example.com",
		CVObjectPath: "budi.pdf",
		CVText:       "Senior frontend engineer with Svelte experience.",
		Status:       domain.ApplicationStatusReview,
	}
	if err := f.store.CreateApplication(ctx, app); err != nil {
		t.Fatalf("seed application: %v", err)
	}
	startedAt := time.Now().Add(-20 * time.Minute).UTC()
	endedAt := time.Now().Add(-5 * time.Minute).UTC()
	reason := domain.FinishReasonCompleted
	sess := domain.Session{
		ID:            applicationsSessionID,
		ApplicationID: app.ID,
		TokenHash:     "hash",
		Status:        domain.SessionStatusCompleted,
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
		StartedAt:     &startedAt,
		EndedAt:       &endedAt,
		FinishReason:  &reason,
		QuestionCount: 4,
	}
	if err := f.store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	topics := []domain.Topic{
		{ID: domain.NewID(), SessionID: sess.ID, Position: 1, Title: "State management", Goal: "Assess state", RubricKeys: []string{"frontend_depth"}, Status: domain.TopicStatusDone},
		{ID: domain.NewID(), SessionID: sess.ID, Position: 2, Title: "Communication", Goal: "Assess clarity", RubricKeys: []string{"communication"}, Status: domain.TopicStatusDone},
	}
	if err := f.store.CreateTopics(ctx, topics); err != nil {
		t.Fatalf("seed topics: %v", err)
	}
	assistant := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleAssistant, Kind: domain.MessageKindQuestion, Content: "How do you manage state?"}
	applicant := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer, Content: "I moved state into a separate store."}
	system := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleSystem, Kind: domain.MessageKindOpening, Content: "system prompt"}
	for _, message := range []domain.Message{assistant, applicant, system} {
		if err := f.store.CreateMessage(ctx, message); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}
	if err := f.store.UpsertScores(ctx, []domain.Score{
		{ApplicationID: app.ID, RubricKey: "frontend_depth", Score: domain.IntPtr(4), Rationale: "strong", Status: domain.ScoreStatusValid, Attempts: 2, Evidence: []domain.ScoreEvidence{{MessageID: applicant.ID, Quote: "moved state into a separate store"}}},
		{ApplicationID: app.ID, RubricKey: "communication", Score: domain.IntPtr(3), Rationale: "clear", Status: domain.ScoreStatusValid, Attempts: 1},
	}); err != nil {
		t.Fatalf("seed scores: %v", err)
	}
	if err := f.store.CreateGraphRun(ctx, domain.GraphRun{ID: applicationsRunID, SessionID: sess.ID, Status: domain.GraphRunStatusSucceeded, CostUSD: 0.0041}); err != nil {
		t.Fatalf("seed graph run: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+app.ID, nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())

	var got struct {
		Application struct {
			ID            string   `json:"id"`
			Status        string   `json:"status"`
			SessionStatus *string  `json:"session_status"`
			OverallScore  *float64 `json:"overall_score"`
		} `json:"application"`
		Job struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Rubric []any  `json:"rubric"`
		} `json:"job"`
		CVText   string `json:"cv_text"`
		Decision *struct {
			Value string `json:"value"`
			Note  string `json:"note"`
		} `json:"decision"`
		Session *struct {
			ID            string `json:"id"`
			Status        string `json:"status"`
			QuestionCount int    `json:"question_count"`
		} `json:"session"`
		GraphRun *struct {
			ID      string  `json:"id"`
			Status  string  `json:"status"`
			CostUSD float64 `json:"cost_usd"`
		} `json:"graph_run"`
		Topics   []any `json:"topics"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
		Scores []any `json:"scores"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Application.ID != app.ID || got.Application.Status != "review" {
		t.Errorf("application = %+v", got.Application)
	}
	if got.Application.SessionStatus == nil || *got.Application.SessionStatus != "completed" {
		t.Errorf("session_status = %v, want completed", got.Application.SessionStatus)
	}
	if got.Application.OverallScore == nil || *got.Application.OverallScore != (4*2+3)/3.0 {
		t.Errorf("overall_score = %v, want %v", got.Application.OverallScore, (4*2+3)/3.0)
	}
	if got.Job.ID != applicationsJobID || len(got.Job.Rubric) != 3 {
		t.Errorf("job = %+v", got.Job)
	}
	if got.CVText != app.CVText {
		t.Errorf("cv_text = %q, want %q", got.CVText, app.CVText)
	}
	if got.Decision != nil {
		t.Errorf("decision = %+v, want null", got.Decision)
	}
	if got.Session == nil || got.Session.ID != sess.ID || got.Session.Status != "completed" || got.Session.QuestionCount != 4 {
		t.Errorf("session = %+v", got.Session)
	}
	if got.GraphRun == nil || got.GraphRun.ID != applicationsRunID || got.GraphRun.Status != "succeeded" || got.GraphRun.CostUSD != 0.0041 {
		t.Errorf("graph_run = %+v", got.GraphRun)
	}
	if len(got.Topics) != 2 || len(got.Messages) != 2 || len(got.Scores) != 2 {
		t.Errorf("topics/messages/scores = %d/%d/%d, want 2/2/2", len(got.Topics), len(got.Messages), len(got.Scores))
	}
	for _, message := range got.Messages {
		if message.Role == "system" {
			t.Errorf("transcript must not include system messages")
		}
	}

	if rec := f.do(t, http.MethodPost, "/api/v1/applications/"+app.ID+"/decision", `{"decision":"shortlist","note":"great"}`, true); rec.Code != http.StatusOK {
		t.Fatalf("decision status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+app.ID, nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec = httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode decided detail: %v", err)
	}
	if got.Decision == nil || got.Decision.Value != "shortlist" || got.Decision.Note != "great" {
		t.Errorf("decision = %+v, want shortlist/great", got.Decision)
	}

	for _, id := range []string{"7c9e6679-0000-4000-8000-000000000999", "not-a-uuid"} {
		if rec := f.do(t, http.MethodGet, "/api/v1/applications/"+id, "", true); rec.Code != http.StatusNotFound {
			t.Errorf("id %q status = %d, want 404", id, rec.Code)
		}
	}
}

func TestDownloadApplicationCVMatchesContract(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	validator := conformance.New(t)

	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusReview)
	data := []byte("%PDF-1.4 cv body")
	if err := f.storage.Upload(context.Background(), app.CVObjectPath, data, "application/pdf"); err != nil {
		t.Fatalf("seed CV: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+app.ID+"/cv", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
		t.Errorf("content-type = %q, want application/pdf", got)
	}
	if rec.Body.String() != string(data) {
		t.Errorf("body = %q, want %q", rec.Body.String(), data)
	}
	validator.ValidateResponse(t, req, rec.Result())

	missing := f.seedApplication(t, "missing@example.com", domain.ApplicationStatusReview)
	rec = f.do(t, http.MethodGet, "/api/v1/applications/"+missing.ID+"/cv", "", true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing CV status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec); code != "not_found" {
		t.Errorf("missing CV code = %q, want not_found", code)
	}

	for _, id := range []string{"7c9e6679-0000-4000-8000-000000000999", "not-a-uuid"} {
		if rec := f.do(t, http.MethodGet, "/api/v1/applications/"+id+"/cv", "", true); rec.Code != http.StatusNotFound {
			t.Errorf("id %q status = %d, want 404", id, rec.Code)
		}
	}
}

func TestDecisionMatchesContract(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	validator := conformance.New(t)

	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusReview)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+app.ID+"/decision", strings.NewReader(`{"decision":"shortlist","note":"great"}`))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())

	var got struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != app.ID || got.Status != "shortlisted" {
		t.Errorf("decision response = %+v, want shortlisted", got)
	}
	stored, err := f.store.GetApplication(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if stored.Status != domain.ApplicationStatusShortlisted || stored.DecisionNote != "great" || stored.DecidedAt == nil {
		t.Errorf("stored application = %+v", stored)
	}

	rec = f.do(t, http.MethodPost, "/api/v1/applications/"+app.ID+"/decision", `{"decision":"reject","note":""}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("change decision status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode changed: %v", err)
	}
	if got.Status != "rejected" {
		t.Errorf("changed status = %q, want rejected", got.Status)
	}

	invited := f.seedApplication(t, "invited@example.com", domain.ApplicationStatusInvited)
	for _, tc := range []struct {
		name string
		id   string
		body string
		want int
		code string
	}{
		{"invalid decision", app.ID, `{"decision":"maybe"}`, http.StatusBadRequest, "validation_failed"},
		{"note too long", app.ID, `{"decision":"shortlist","note":"` + strings.Repeat("x", 1001) + `"}`, http.StatusBadRequest, "validation_failed"},
		{"invalid json", app.ID, `{`, http.StatusBadRequest, "validation_failed"},
		{"wrong state", invited.ID, `{"decision":"shortlist"}`, http.StatusConflict, "invalid_state"},
		{"unknown", "7c9e6679-0000-4000-8000-000000000999", `{"decision":"shortlist"}`, http.StatusNotFound, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, http.MethodPost, "/api/v1/applications/"+tc.id+"/decision", tc.body, true)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec); code != tc.code {
				t.Errorf("code = %q, want %q", code, tc.code)
			}
		})
	}
}

func TestResendInviteForInvitedSession(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	validator := conformance.New(t)

	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusInvited)
	old, oldRaw := f.seedSession(t, app, domain.SessionStatusInvited)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+app.ID+"/resend-invite", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())

	var got struct {
		SessionID string  `json:"session_id"`
		ExpiresAt string  `json:"expires_at"`
		InviteURL *string `json:"invite_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionID == old.ID {
		t.Errorf("session_id = %q, want a new session", got.SessionID)
	}
	if got.InviteURL == nil || !strings.HasPrefix(*got.InviteURL, "https://talentscout-id.vercel.app/s/") {
		t.Errorf("invite_url = %v, want a public /s/ link", got.InviteURL)
	}
	if strings.Contains(*got.InviteURL, oldRaw) {
		t.Errorf("invite_url must not reuse the revoked token")
	}

	gotOld, err := f.store.GetSession(context.Background(), old.ID)
	if err != nil {
		t.Fatalf("GetSession old: %v", err)
	}
	if gotOld.Status != domain.SessionStatusRevoked {
		t.Errorf("old session status = %q, want revoked", gotOld.Status)
	}
	gotNew, err := f.store.GetSession(context.Background(), got.SessionID)
	if err != nil {
		t.Fatalf("GetSession new: %v", err)
	}
	if gotNew.Status != domain.SessionStatusInvited {
		t.Errorf("new session status = %q, want invited", gotNew.Status)
	}
	if f.mailer.calls != 1 || f.mailer.link != *got.InviteURL {
		t.Errorf("mailer calls/link = %d/%q, want 1/%q", f.mailer.calls, f.mailer.link, *got.InviteURL)
	}
}

func TestResendInviteForFailedApplication(t *testing.T) {
	f := newApplicationsFixture(t, "log")

	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusFailed)
	old, _ := f.seedSession(t, app, domain.SessionStatusCompleted)

	rec := f.do(t, http.MethodPost, "/api/v1/applications/"+app.ID+"/resend-invite", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		SessionID string  `json:"session_id"`
		InviteURL *string `json:"invite_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionID == old.ID {
		t.Errorf("session_id = %q, want a new session", got.SessionID)
	}
	if got.InviteURL == nil {
		t.Errorf("invite_url = nil, want a link in log mode")
	}

	gotOld, err := f.store.GetSession(context.Background(), old.ID)
	if err != nil {
		t.Fatalf("GetSession old: %v", err)
	}
	if gotOld.Status != domain.SessionStatusCompleted {
		t.Errorf("old terminal session status = %q, want completed", gotOld.Status)
	}
	gotApp, err := f.store.GetApplication(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if gotApp.Status != domain.ApplicationStatusInvited {
		t.Errorf("application status = %q, want invited", gotApp.Status)
	}
}

func TestResendInviteSnapshotsSpokenEnglishRequired(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	ctx := context.Background()

	job, err := f.store.GetJob(ctx, applicationsJobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	job.SpokenEnglishRequired = true
	if err := f.store.UpdateJob(ctx, job); err != nil {
		t.Fatalf("set voice job: %v", err)
	}

	app := f.seedApplication(t, "resend-voice@example.com", domain.ApplicationStatusInvited)
	f.seedSession(t, app, domain.SessionStatusInvited)
	resend := func(t *testing.T) domain.Session {
		t.Helper()
		rec := f.do(t, http.MethodPost, "/api/v1/applications/"+app.ID+"/resend-invite", "", true)
		if rec.Code != http.StatusOK {
			t.Fatalf("resend status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		var got struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode resend: %v", err)
		}
		sess, err := f.store.GetSession(ctx, got.SessionID)
		if err != nil {
			t.Fatalf("get resent session: %v", err)
		}
		return sess
	}

	voice := resend(t)
	if !voice.Voice {
		t.Fatal("resent session from a voice job is not a voice session")
	}
	if kept, err := f.store.GetSession(ctx, voice.ID); err != nil || !kept.Voice {
		t.Fatalf("existing resent session = %+v, %v; want voice", kept, err)
	}

	job.SpokenEnglishRequired = false
	if err := f.store.UpdateJob(ctx, job); err != nil {
		t.Fatalf("set text job: %v", err)
	}
	text := resend(t)
	if text.Voice {
		t.Fatal("resent session after the edit did not take the new text mode")
	}
}

func TestResendInviteGmailModeHidesLink(t *testing.T) {
	f := newApplicationsFixture(t, "gmail")

	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusInvited)
	f.seedSession(t, app, domain.SessionStatusInvited)

	rec := f.do(t, http.MethodPost, "/api/v1/applications/"+app.ID+"/resend-invite", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		InviteURL *string `json:"invite_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.InviteURL != nil {
		t.Errorf("invite_url = %q, want null in gmail mode", *got.InviteURL)
	}
	if f.mailer.calls != 1 {
		t.Errorf("mailer calls = %d, want 1", f.mailer.calls)
	}
}

func TestResendInviteInvalidState(t *testing.T) {
	f := newApplicationsFixture(t, "log")

	tests := []struct {
		name   string
		status domain.ApplicationStatus
		sess   domain.SessionStatus
	}{
		{"completed session", domain.ApplicationStatusReview, domain.SessionStatusCompleted},
		{"active session", domain.ApplicationStatusInSession, domain.SessionStatusActive},
		{"revoked session", domain.ApplicationStatusInvited, domain.SessionStatusRevoked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := f.seedApplication(t, strings.ReplaceAll(tc.name, " ", "-")+"@example.com", tc.status)
			f.seedSession(t, app, tc.sess)
			rec := f.do(t, http.MethodPost, "/api/v1/applications/"+app.ID+"/resend-invite", "", true)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec); code != "invalid_state" {
				t.Errorf("code = %q, want invalid_state", code)
			}
		})
	}

	for _, id := range []string{"7c9e6679-0000-4000-8000-000000000999", "not-a-uuid"} {
		if rec := f.do(t, http.MethodPost, "/api/v1/applications/"+id+"/resend-invite", "", true); rec.Code != http.StatusNotFound {
			t.Errorf("id %q status = %d, want 404", id, rec.Code)
		}
	}
}

// docs/API.md: an `expired` current session is 409 invalid_state, even though
// its stored status is still `invited`.
func TestResendInviteExpiredInvitation(t *testing.T) {
	f := newApplicationsFixture(t, "log")

	app := f.seedApplication(t, "expired@example.com", domain.ApplicationStatusInvited)
	_, hash, err := session.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	expired := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     hash,
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(-time.Minute).UTC(),
	}
	if err := f.store.CreateSession(context.Background(), expired); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	rec := f.do(t, http.MethodPost, "/api/v1/applications/"+app.ID+"/resend-invite", "", true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec); code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", code)
	}
	latest, err := f.store.GetLatestSessionByApplication(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("GetLatestSessionByApplication: %v", err)
	}
	if latest.ID != expired.ID || latest.Status != domain.SessionStatusInvited {
		t.Errorf("latest session = %s/%s, want the untouched expired invite %s", latest.ID, latest.Status, expired.ID)
	}
}

// The recruiter's application detail exposes the applicant's client signals per
// message and the advisory authenticity result; interviewer messages carry a
// null entry.
func TestApplicationDetailReturnsEntryAndAuthenticity(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	validator := conformance.New(t)
	ctx := context.Background()

	app := f.seedApplication(t, "signals@example.com", domain.ApplicationStatusReview)
	sess, _ := f.seedSession(t, app, domain.SessionStatusCompleted)

	assistant := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleAssistant, Kind: domain.MessageKindQuestion, Content: "How do you manage state?"}
	applicant := domain.Message{
		ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer,
		Content: "I moved state into a separate store.",
		Entry:   &domain.MessageEntry{PastedChars: 32, TypedChars: 4, DurationMS: 900},
	}
	for _, message := range []domain.Message{assistant, applicant} {
		if err := f.store.CreateMessage(ctx, message); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}
	assessedAt := time.Date(2026, 11, 28, 5, 0, 0, 0, time.UTC)
	auth := domain.Authenticity{
		Level:      domain.AuthenticityLevelPossible,
		Reasons:    []string{"1 of 1 answers were pasted in full"},
		AssessedAt: assessedAt,
	}
	if err := f.store.SetApplicationAuthenticity(ctx, app.ID, &auth); err != nil {
		t.Fatalf("SetApplicationAuthenticity: %v", err)
	}
	fluency := domain.Fluency{
		WordsPerMinute:       132.4,
		PauseRatio:           0.18,
		FillerRate:           0.03,
		TranscriptConfidence: 0.94,
		Languages:            []string{"en"},
		Summary:              "Steady pace with a few short pauses.",
		SpokenAnswers:        1,
		TypedAnswers:         0,
		AssessedAt:           assessedAt,
	}
	if err := f.store.SetApplicationFluency(ctx, app.ID, &fluency); err != nil {
		t.Fatalf("SetApplicationFluency: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+app.ID, nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())

	var got struct {
		Authenticity *struct {
			Level      string    `json:"level"`
			Reasons    []string  `json:"reasons"`
			AssessedAt time.Time `json:"assessed_at"`
		} `json:"authenticity"`
		Fluency *struct {
			WordsPerMinute       float64   `json:"words_per_minute"`
			PauseRatio           float64   `json:"pause_ratio"`
			FillerRate           float64   `json:"filler_rate"`
			TranscriptConfidence float64   `json:"transcript_confidence"`
			Languages            []string  `json:"languages"`
			Summary              string    `json:"summary"`
			SpokenAnswers        int       `json:"spoken_answers"`
			TypedAnswers         int       `json:"typed_answers"`
			AssessedAt           time.Time `json:"assessed_at"`
		} `json:"fluency"`
		Messages []struct {
			Role  string `json:"role"`
			Entry *struct {
				PastedChars int `json:"pasted_chars"`
				TypedChars  int `json:"typed_chars"`
				DurationMS  int `json:"duration_ms"`
			} `json:"entry"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Authenticity == nil || got.Authenticity.Level != "possible" || len(got.Authenticity.Reasons) != 1 {
		t.Fatalf("authenticity = %+v, want possible with its reason", got.Authenticity)
	}
	if got.Fluency == nil || got.Fluency.WordsPerMinute != 132.4 || got.Fluency.SpokenAnswers != 1 ||
		len(got.Fluency.Languages) != 1 || got.Fluency.Languages[0] != "en" || got.Fluency.AssessedAt.IsZero() {
		t.Fatalf("fluency = %+v, want the stored assessment", got.Fluency)
	}
	var applicantEntry, assistantEntry bool
	for _, message := range got.Messages {
		switch message.Role {
		case "applicant":
			if message.Entry == nil || message.Entry.PastedChars != 32 || message.Entry.TypedChars != 4 || message.Entry.DurationMS != 900 {
				t.Errorf("applicant entry = %+v, want the stored signals", message.Entry)
			}
			applicantEntry = true
		case "assistant":
			if message.Entry != nil {
				t.Errorf("interviewer entry = %+v, want null", message.Entry)
			}
			assistantEntry = true
		}
	}
	if !applicantEntry || !assistantEntry {
		t.Errorf("messages = %+v, want both roles", got.Messages)
	}
}

// The recruiter's application detail carries the recording metadata for spoken
// answers: available, deleted after retention, or not_stored when storage failed.
// Typed and interviewer messages carry a null audio. The applicant's own
// transcript carries neither entry nor audio.
func TestApplicationDetailReturnsAudioStatusPerMessage(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	validator := conformance.New(t)
	ctx := context.Background()

	app := f.seedApplication(t, "audio-status@example.com", domain.ApplicationStatusReview)
	sess, raw := f.seedSession(t, app, domain.SessionStatusActive)

	assistant := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleAssistant, Kind: domain.MessageKindQuestion, Content: "How did you record?"}
	available := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer, Content: "available answer",
		Entry: &domain.MessageEntry{Mode: domain.MessageEntryModeSpoken, SpeakingMS: 17500}}
	deleted := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer, Content: "deleted answer",
		Entry: &domain.MessageEntry{Mode: domain.MessageEntryModeSpoken, SpeakingMS: 12000}}
	notStored := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer, Content: "not stored answer",
		Entry: &domain.MessageEntry{Mode: domain.MessageEntryModeSpoken, SpeakingMS: 9000}}
	typed := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer, Content: "typed answer",
		Entry: &domain.MessageEntry{Mode: domain.MessageEntryModeTyped, FallbackReason: domain.MessageEntryFallbackChoseText}}
	for _, message := range []domain.Message{assistant, available, deleted, notStored, typed} {
		if err := f.store.CreateMessage(ctx, message); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	deletedAt := time.Now().UTC().Add(-time.Hour)
	uploads := []store.AudioUpload{
		{ID: domain.NewID(), SessionID: sess.ID, ObjectPath: "a.webm", ContentType: "audio/webm", DurationMS: 18000, Transcript: available.Content, Language: "en", Stored: true, Counted: true, MessageID: &available.ID},
		{ID: domain.NewID(), SessionID: sess.ID, ObjectPath: "b.webm", ContentType: "audio/webm", DurationMS: 12000, Transcript: deleted.Content, Language: "en", Stored: true, Counted: true, MessageID: &deleted.ID, DeletedAt: &deletedAt},
		{ID: domain.NewID(), SessionID: sess.ID, ObjectPath: "c.webm", ContentType: "audio/webm", DurationMS: 9000, Transcript: notStored.Content, Language: "en", Stored: false, Counted: true, MessageID: &notStored.ID},
	}
	for _, upload := range uploads {
		if err := f.store.CreateAudioUpload(ctx, upload); err != nil {
			t.Fatalf("CreateAudioUpload: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+app.ID, nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())

	var got struct {
		Messages []struct {
			ID    string `json:"id"`
			Role  string `json:"role"`
			Audio *struct {
				DurationMS int    `json:"duration_ms"`
				Language   string `json:"language"`
				Status     string `json:"status"`
			} `json:"audio"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]string{
		available.ID: "available",
		deleted.ID:   "deleted",
		notStored.ID: "not_stored",
	}
	for _, message := range got.Messages {
		status, spoken := want[message.ID]
		switch {
		case spoken:
			if message.Audio == nil {
				t.Errorf("message %s audio = null, want %s", message.ID, status)
				continue
			}
			if message.Audio.Status != status || message.Audio.Language != "en" {
				t.Errorf("message %s audio = %+v, want status %s and language en", message.ID, message.Audio, status)
			}
		default:
			if message.Audio != nil {
				t.Errorf("message %s (%s) audio = %+v, want null", message.ID, message.Role, message.Audio)
			}
		}
	}

	// The applicant's transcript never exposes the recruiter-only entry or audio.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+raw+"/messages", nil)
	rec = httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session messages status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	for _, field := range []string{`"entry"`, `"audio"`} {
		if strings.Contains(rec.Body.String(), field) {
			t.Errorf("applicant transcript exposes %s:\n%s", field, rec.Body.String())
		}
	}
}
