package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const manageDescription = "We are hiring a senior frontend engineer with deep Svelte and React experience."

func manageRubric() []domain.RubricCriterion {
	return []domain.RubricCriterion{
		{Key: "frontend_depth", Label: "Frontend depth", Description: "Svelte, React and rendering depth.", Weight: 3},
		{Key: "system_design", Label: "System design", Description: "Component boundaries and data flow.", Weight: 2},
		{Key: "communication", Label: "Communication", Description: "Clarity and collaboration.", Weight: 1},
	}
}

func manageModels() config.ModelsConfig {
	return config.ModelsConfig{
		Default: config.Defaults{Temperature: 0.3, TimeoutSeconds: 30, MaxRetriesOnHTTPError: 1},
		Nodes: map[string]config.Node{
			"rubric_suggester": {Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 2000},
		},
		Prices: map[string]config.Price{
			"deepseek/deepseek-v4.1-flash": {In: 0.045, Out: 0.5},
		},
	}
}

const validSuggestionJSON = `{"rubric":[
	{"key":"frontend_depth","label":"Frontend depth","description":"Svelte, React and rendering depth.","weight":3},
	{"key":"system_design","label":"System design","description":"Component boundaries and data flow.","weight":2},
	{"key":"communication","label":"Communication","description":"Clarity and collaboration.","weight":1}
]}`

func newManageJobs(st store.Store, client llm.Client) *handlers.Jobs {
	return handlers.NewJobs(jobsConfig(), st, client, manageModels())
}

type manageJobBody struct {
	Title       string                   `json:"title"`
	Description string                   `json:"description"`
	Rubric      []domain.RubricCriterion `json:"rubric"`
	Status      string                   `json:"status,omitempty"`
}

func (b manageJobBody) JSON(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal job body: %v", err)
	}
	return string(data)
}

func validManageBody() manageJobBody {
	return manageJobBody{
		Title:       "Senior Frontend Engineer",
		Description: manageDescription,
		Rubric:      manageRubric(),
	}
}

func serveBody(h http.HandlerFunc, method, target, body, jobID string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if jobID != "" {
		req = withJobID(req, jobID)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestCreateGetAndListJobs(t *testing.T) {
	st := store.NewMemory()
	h := newManageJobs(st, llm.NewFake(nil))

	first := serveBody(h.Create, http.MethodPost, "/api/v1/jobs", validManageBody().JSON(t), "")
	if first.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	var created struct {
		ID               string                   `json:"id"`
		Title            string                   `json:"title"`
		Status           domain.JobStatus         `json:"status"`
		Rubric           []domain.RubricCriterion `json:"rubric"`
		ApplicationCount int                      `json:"application_count"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created job: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created job has no id")
	}
	if created.Status != domain.JobStatusOpen {
		t.Errorf("status = %q, want open", created.Status)
	}
	if len(created.Rubric) != len(manageRubric()) {
		t.Errorf("rubric len = %d, want %d", len(created.Rubric), len(manageRubric()))
	}

	got := serveBody(h.Get, http.MethodGet, "/api/v1/jobs/"+created.ID, "", created.ID)
	if got.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200 (body: %s)", got.Code, got.Body.String())
	}
	var fetched struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode fetched job: %v", err)
	}
	if fetched.ID != created.ID || fetched.Title != created.Title {
		t.Errorf("fetched job = %+v, want id %q title %q", fetched, created.ID, created.Title)
	}

	time.Sleep(2 * time.Millisecond)
	second := serveBody(h.Create, http.MethodPost, "/api/v1/jobs", validManageBody().JSON(t), "")
	if second.Code != http.StatusCreated {
		t.Fatalf("second create status = %d, want 201 (body: %s)", second.Code, second.Body.String())
	}
	var secondJob struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondJob); err != nil {
		t.Fatalf("decode second job: %v", err)
	}

	list := serveBody(h.List, http.MethodGet, "/api/v1/jobs", "", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200 (body: %s)", list.Code, list.Body.String())
	}
	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode listed jobs: %v", err)
	}
	if len(listed.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(listed.Items))
	}
	if listed.Items[0].ID != secondJob.ID {
		t.Errorf("first item = %q, want newest %q", listed.Items[0].ID, secondJob.ID)
	}
}

func TestCreateJobValidation(t *testing.T) {
	h := newManageJobs(store.NewMemory(), llm.NewFake(nil))

	tests := []struct {
		name   string
		mutate func(*manageJobBody)
		field  string
	}{
		{name: "title too short", mutate: func(b *manageJobBody) { b.Title = "ab" }, field: "title"},
		{name: "title too long", mutate: func(b *manageJobBody) { b.Title = strings.Repeat("a", 121) }, field: "title"},
		{name: "description too short", mutate: func(b *manageJobBody) { b.Description = strings.Repeat("a", 49) }, field: "description"},
		{name: "description too long", mutate: func(b *manageJobBody) { b.Description = strings.Repeat("a", 12001) }, field: "description"},
		{name: "too few criteria", mutate: func(b *manageJobBody) { b.Rubric = b.Rubric[:2] }, field: "rubric"},
		{name: "too many criteria", mutate: func(b *manageJobBody) {
			b.Rubric = append(b.Rubric,
				domain.RubricCriterion{Key: "extra_a", Label: "Extra A", Description: "An extra criterion.", Weight: 1},
				domain.RubricCriterion{Key: "extra_b", Label: "Extra B", Description: "Another criterion.", Weight: 1},
				domain.RubricCriterion{Key: "extra_c", Label: "Extra C", Description: "A third criterion.", Weight: 1},
				domain.RubricCriterion{Key: "extra_d", Label: "Extra D", Description: "A fourth criterion.", Weight: 1},
			)
		}, field: "rubric"},
		{name: "invalid key", mutate: func(b *manageJobBody) { b.Rubric[0].Key = "Frontend" }, field: "rubric[0].key"},
		{name: "duplicate key", mutate: func(b *manageJobBody) { b.Rubric[1].Key = b.Rubric[0].Key }, field: "rubric[1].key"},
		{name: "label too short", mutate: func(b *manageJobBody) { b.Rubric[0].Label = "x" }, field: "rubric[0].label"},
		{name: "label too long", mutate: func(b *manageJobBody) { b.Rubric[0].Label = strings.Repeat("a", 61) }, field: "rubric[0].label"},
		{name: "criterion description too short", mutate: func(b *manageJobBody) { b.Rubric[0].Description = "short" }, field: "rubric[0].description"},
		{name: "criterion description too long", mutate: func(b *manageJobBody) { b.Rubric[0].Description = strings.Repeat("a", 301) }, field: "rubric[0].description"},
		{name: "weight too low", mutate: func(b *manageJobBody) { b.Rubric[0].Weight = 0 }, field: "rubric[0].weight"},
		{name: "weight too high", mutate: func(b *manageJobBody) { b.Rubric[0].Weight = 4 }, field: "rubric[0].weight"},
		{name: "invalid status", mutate: func(b *manageJobBody) { b.Status = "paused" }, field: "status"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := validManageBody()
			tc.mutate(&body)
			rec := serveBody(h.Create, http.MethodPost, "/api/v1/jobs", body.JSON(t), "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			code, _, details := decodeError(t, rec)
			if code != "validation_failed" {
				t.Errorf("code = %q, want validation_failed", code)
			}
			if details["field"] != tc.field {
				t.Errorf("field = %q, want %q", details["field"], tc.field)
			}
		})
	}
}

func TestUpdateJobLocksRubricAfterApplication(t *testing.T) {
	st := store.NewMemory()
	job := domain.Job{
		ID:          "7c9e6679-0000-4000-8000-000000000080",
		Title:       "Senior Frontend Engineer",
		Description: manageDescription,
		Rubric:      manageRubric(),
		Status:      domain.JobStatusOpen,
	}
	if err := st.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := st.CreateApplication(context.Background(), domain.Application{
		ID:     "7c9e6679-0000-4000-8000-000000000081",
		JobID:  job.ID,
		Name:   "Budi",
		Email:  "budi@example.com",
		Status: domain.ApplicationStatusApplied,
	}); err != nil {
		t.Fatalf("seed application: %v", err)
	}
	h := newManageJobs(st, nil)

	title := `{"title":"Staff Frontend Engineer"}`
	titleRec := serveBody(h.Update, http.MethodPatch, "/api/v1/jobs/"+job.ID, title, job.ID)
	if titleRec.Code != http.StatusOK {
		t.Fatalf("title patch status = %d, want 200 (body: %s)", titleRec.Code, titleRec.Body.String())
	}

	rubric, err := json.Marshal(map[string]any{"rubric": manageRubric()})
	if err != nil {
		t.Fatalf("marshal rubric patch: %v", err)
	}
	rubricRec := serveBody(h.Update, http.MethodPatch, "/api/v1/jobs/"+job.ID, string(rubric), job.ID)
	if rubricRec.Code != http.StatusConflict {
		t.Fatalf("rubric patch status = %d, want 409 (body: %s)", rubricRec.Code, rubricRec.Body.String())
	}
	code, _, _ := decodeError(t, rubricRec)
	if code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", code)
	}

	stored, err := st.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if stored.Title != "Staff Frontend Engineer" {
		t.Errorf("title = %q, want updated title", stored.Title)
	}
	if len(stored.Rubric) != len(manageRubric()) {
		t.Errorf("rubric len = %d, want unchanged %d", len(stored.Rubric), len(manageRubric()))
	}
}

func TestUpdateJobStatus(t *testing.T) {
	st := store.NewMemory()
	job := domain.Job{
		ID:          "7c9e6679-0000-4000-8000-000000000082",
		Title:       "Senior Frontend Engineer",
		Description: manageDescription,
		Rubric:      manageRubric(),
		Status:      domain.JobStatusOpen,
	}
	if err := st.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	h := newManageJobs(st, nil)

	rec := serveBody(h.Update, http.MethodPatch, "/api/v1/jobs/"+job.ID, `{"status":"closed"}`, job.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Status domain.JobStatus `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode patched job: %v", err)
	}
	if got.Status != domain.JobStatusClosed {
		t.Errorf("status = %q, want closed", got.Status)
	}
}

func TestUpdateJobNotFound(t *testing.T) {
	h := newManageJobs(store.NewMemory(), nil)

	rec := serveBody(h.Update, http.MethodPatch, "/api/v1/jobs/7c9e6679-0000-4000-8000-000000000099", `{"title":"Staff Engineer"}`, "7c9e6679-0000-4000-8000-000000000099")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestCreateJobLimit(t *testing.T) {
	st := store.NewMemory()
	for i := 0; i < 20; i++ {
		if err := st.CreateJob(context.Background(), domain.Job{
			ID:          domain.NewID(),
			Title:       "Senior Frontend Engineer",
			Description: manageDescription,
			Rubric:      manageRubric(),
			Status:      domain.JobStatusOpen,
		}); err != nil {
			t.Fatalf("seed job %d: %v", i, err)
		}
	}
	h := newManageJobs(st, nil)

	rec := serveBody(h.Create, http.MethodPost, "/api/v1/jobs", validManageBody().JSON(t), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	code, _, _ := decodeError(t, rec)
	if code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", code)
	}
}

func TestRubricSuggestionValid(t *testing.T) {
	fake := llm.NewFake(map[string]string{"rubric_suggester": validSuggestionJSON})
	h := newManageJobs(store.NewMemory(), fake)

	body := `{"title":"Senior Frontend Engineer","description":"` + manageDescription + `"}`
	rec := serveBody(h.SuggestRubric, http.MethodPost, "/api/v1/jobs/rubric-suggestion", body, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Rubric []domain.RubricCriterion `json:"rubric"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode suggestion: %v", err)
	}
	if len(got.Rubric) != 3 {
		t.Fatalf("rubric len = %d, want 3", len(got.Rubric))
	}
	if calls := len(fake.Calls()); calls != 1 {
		t.Errorf("llm calls = %d, want 1", calls)
	}
}

func TestRubricSuggestionRetriesInvalidOutputThenFails(t *testing.T) {
	fake := llm.NewFake(map[string]string{"rubric_suggester": `{"rubric":[{"key":"BAD"}]}`})
	h := newManageJobs(store.NewMemory(), fake)

	body := `{"title":"Senior Frontend Engineer","description":"` + manageDescription + `"}`
	rec := serveBody(h.SuggestRubric, http.MethodPost, "/api/v1/jobs/rubric-suggestion", body, "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
	code, _, _ := decodeError(t, rec)
	if code != "llm_unavailable" {
		t.Errorf("code = %q, want llm_unavailable", code)
	}
	if calls := len(fake.Calls()); calls != 2 {
		t.Errorf("llm calls = %d, want 2", calls)
	}
}

func TestRubricSuggestionRateLimited(t *testing.T) {
	fake := llm.NewFake(map[string]string{"rubric_suggester": validSuggestionJSON})
	h := newManageJobs(store.NewMemory(), fake)

	body := `{"title":"Senior Frontend Engineer","description":"` + manageDescription + `"}`
	for i := 0; i < 10; i++ {
		rec := serveBody(h.SuggestRubric, http.MethodPost, "/api/v1/jobs/rubric-suggestion", body, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("suggestion %d status = %d, want 200 (body: %s)", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := serveBody(h.SuggestRubric, http.MethodPost, "/api/v1/jobs/rubric-suggestion", body, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th status = %d, want 429 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header is missing")
	}
}

func TestRubricSuggestionValidation(t *testing.T) {
	h := newManageJobs(store.NewMemory(), llm.NewFake(map[string]string{"rubric_suggester": validSuggestionJSON}))

	tests := []struct {
		name  string
		body  string
		field string
	}{
		{
			name:  "title too short",
			body:  `{"title":"ab","description":"` + manageDescription + `"}`,
			field: "title",
		},
		{
			name:  "description too short",
			body:  `{"title":"Senior Frontend Engineer","description":"short"}`,
			field: "description",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveBody(h.SuggestRubric, http.MethodPost, "/api/v1/jobs/rubric-suggestion", tc.body, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			_, _, details := decodeError(t, rec)
			if details["field"] != tc.field {
				t.Errorf("field = %q, want %q", details["field"], tc.field)
			}
		})
	}
}
