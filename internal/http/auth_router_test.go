package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/conformance"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const authRouterJobID = "7c9e6679-0000-4000-8000-000000000055"

func authRouter(t *testing.T) (http.Handler, config.Config) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateJob(context.Background(), domain.Job{
		ID:          authRouterJobID,
		Title:       "Senior Frontend Engineer",
		Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
		Status:      domain.JobStatusOpen,
		Rubric: []domain.RubricCriterion{
			{Key: "frontend_depth", Label: "Frontend depth", Description: "Svelte and React", Weight: 2},
		},
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := st.CreateApplication(context.Background(), domain.Application{
		ID:     "7c9e6679-0000-4000-8000-000000000056",
		JobID:  authRouterJobID,
		Name:   "Budi",
		Email:  "budi@example.com",
		Status: domain.ApplicationStatusApplied,
	}); err != nil {
		t.Fatalf("seed application: %v", err)
	}

	cfg := config.Config{
		Version:           "0.1.0",
		LLMMode:           "fake",
		EmailMode:         "log",
		PublicAppURL:      "https://talentscout.vercel.app",
		RecruiterPassword: "correct horse",
		AuthTokenSecret:   "test-auth-secret",
	}
	return httpapi.NewRouter(cfg, httpapi.Deps{Store: st}), cfg
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"correct horse"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if got.AccessToken == "" {
		t.Fatal("login returned an empty access_token")
	}
	return got.AccessToken
}

func serve(router http.Handler, method, target, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestLoginMatchesContract(t *testing.T) {
	router, _ := authRouter(t)
	validator := conformance.New(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"correct horse"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())
}

func TestLoginUnauthorizedMatchesContract(t *testing.T) {
	router, _ := authRouter(t)
	validator := conformance.New(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"wrong"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())
}

func TestRecruiterBearerIsHeaderOnly(t *testing.T) {
	router, cfg := authRouter(t)
	token := login(t, router)

	expiredIssuer := auth.NewWithClock(cfg.AuthTokenSecret, func() time.Time {
		return time.Now().Add(-auth.AccessTokenTTL - time.Hour)
	})
	expired, _, err := expiredIssuer.IssueAccessToken()
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}

	tests := []struct {
		name   string
		target string
		token  string
		header string
		want   int
	}{
		{name: "valid bearer", target: "/api/v1/jobs", token: token, want: http.StatusOK},
		{name: "no credentials", target: "/api/v1/jobs", want: http.StatusUnauthorized},
		{name: "access_token in query is rejected", target: "/api/v1/jobs?access_token=" + token, want: http.StatusUnauthorized},
		{name: "invalid bearer token", target: "/api/v1/jobs", token: "not-a-token", want: http.StatusUnauthorized},
		{name: "expired bearer token", target: "/api/v1/jobs", token: expired, want: http.StatusUnauthorized},
		{name: "wrong scheme", target: "/api/v1/jobs", header: "Basic " + token, want: http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestJobsEndpointsMatchContract(t *testing.T) {
	router, _ := authRouter(t)
	token := login(t, router)
	validator := conformance.New(t)

	tests := []struct {
		name   string
		target string
		status int
		token  bool
	}{
		{name: "list jobs", target: "/api/v1/jobs", status: http.StatusOK, token: true},
		{name: "job detail", target: "/api/v1/jobs/" + authRouterJobID, status: http.StatusOK, token: true},
		{name: "unknown job", target: "/api/v1/jobs/7c9e6679-0000-4000-8000-000000000099", status: http.StatusNotFound, token: true},
		{name: "malformed job id", target: "/api/v1/jobs/not-a-uuid", status: http.StatusNotFound, token: true},
		{name: "public job", target: "/api/v1/jobs/" + authRouterJobID + "/public", status: http.StatusOK},
		{name: "unknown public job", target: "/api/v1/jobs/7c9e6679-0000-4000-8000-000000000099/public", status: http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.token {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.status, rec.Body.String())
			}
			validator.ValidateResponse(t, req, rec.Result())
		})
	}
}

func TestNoRefreshOrLogoutEndpoint(t *testing.T) {
	router, _ := authRouter(t)

	for _, target := range []string{"/api/v1/auth/refresh", "/api/v1/auth/logout"} {
		rec := serve(router, http.MethodPost, target, "")
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 404/405", target, rec.Code)
		}
	}
}
