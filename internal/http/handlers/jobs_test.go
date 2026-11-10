package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const jobsJobID = "7c9e6679-0000-4000-8000-000000000005"

func jobsConfig() config.Config {
	return config.Config{
		Version:           "0.1.0",
		LLMMode:           "fake",
		EmailMode:         "log",
		PublicAppURL:      "https://talentscout.vercel.app",
		RecruiterPassword: "correct horse",
		AuthTokenSecret:   "0123456789abcdef0123456789abcdef",
	}
}

func withJobID(r *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("jobId", id)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func jobsStore(t *testing.T) *store.Memory {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateJob(context.Background(), domain.Job{
		ID:          jobsJobID,
		Title:       "Senior Frontend Engineer",
		Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
		Status:      domain.JobStatusOpen,
		Rubric: []domain.RubricCriterion{
			{Key: "frontend_depth", Label: "Frontend depth", Description: "Svelte and React", Weight: 2},
		},
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	for i, email := range []string{"a@example.com", "b@example.com"} {
		if err := st.CreateApplication(context.Background(), domain.Application{
			ID:     domain.NewID(),
			JobID:  jobsJobID,
			Name:   "Applicant",
			Email:  email,
			Status: domain.ApplicationStatusApplied,
		}); err != nil {
			t.Fatalf("seed application %d: %v", i, err)
		}
	}
	return st
}

func TestListJobs(t *testing.T) {
	h := handlers.NewJobs(jobsConfig(), jobsStore(t))

	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []struct {
			ID               string `json:"id"`
			ApplyURL         string `json:"apply_url"`
			ApplicationCount int    `json:"application_count"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(got.Items))
	}
	if got.Items[0].ID != jobsJobID {
		t.Errorf("id = %q, want %q", got.Items[0].ID, jobsJobID)
	}
	if want := "https://talentscout.vercel.app/apply/" + jobsJobID; got.Items[0].ApplyURL != want {
		t.Errorf("apply_url = %q, want %q", got.Items[0].ApplyURL, want)
	}
	if got.Items[0].ApplicationCount != 2 {
		t.Errorf("application_count = %d, want 2", got.Items[0].ApplicationCount)
	}
	if got.NextCursor != nil {
		t.Errorf("next_cursor = %v, want null", *got.NextCursor)
	}
}

func TestGetJob(t *testing.T) {
	h := handlers.NewJobs(jobsConfig(), jobsStore(t))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobsJobID, nil)
	req = withJobID(req, jobsJobID)
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		ID               string            `json:"id"`
		Title            string            `json:"title"`
		Status           domain.JobStatus  `json:"status"`
		ApplyURL         string            `json:"apply_url"`
		ApplicationCount int               `json:"application_count"`
		Rubric           []json.RawMessage `json:"rubric"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != jobsJobID || got.Title != "Senior Frontend Engineer" || got.Status != domain.JobStatusOpen {
		t.Errorf("unexpected job: %+v", got)
	}
	if got.ApplicationCount != 2 {
		t.Errorf("application_count = %d, want 2", got.ApplicationCount)
	}
	if len(got.Rubric) != 1 {
		t.Errorf("rubric len = %d, want 1", len(got.Rubric))
	}
}

func TestGetJobNotFound(t *testing.T) {
	h := handlers.NewJobs(jobsConfig(), jobsStore(t))

	for _, id := range []string{"7c9e6679-0000-4000-8000-000000000099", "not-a-uuid"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+id, nil)
		req = withJobID(req, id)
		h.Get(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("id %q status = %d, want 404", id, rec.Code)
		}
	}
}

func TestGetPublicJob(t *testing.T) {
	h := handlers.NewJobs(jobsConfig(), jobsStore(t))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobsJobID+"/public", nil)
	req = withJobID(req, jobsJobID)
	h.Public(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] != jobsJobID {
		t.Errorf("id = %v, want %q", got["id"], jobsJobID)
	}
	if got["status"] != "open" {
		t.Errorf("status = %v, want open", got["status"])
	}
	for _, key := range []string{"rubric", "apply_url", "application_count", "created_at", "updated_at"} {
		if _, ok := got[key]; ok {
			t.Errorf("public job must not include %q", key)
		}
	}
}

func TestGetPublicJobNotFound(t *testing.T) {
	h := handlers.NewJobs(jobsConfig(), jobsStore(t))

	for _, id := range []string{"7c9e6679-0000-4000-8000-000000000099", "not-a-uuid"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+id+"/public", nil)
		req = withJobID(req, id)
		h.Public(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("id %q status = %d, want 404", id, rec.Code)
		}
	}
}
