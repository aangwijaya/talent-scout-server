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
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const (
	authRouterJobID = "7c9e6679-0000-4000-8000-000000000055"
	authRouterRunID = "7c9e6679-0000-4000-8000-000000000057"
)

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
	if err := st.CreateGraphRun(context.Background(), domain.GraphRun{
		ID:        authRouterRunID,
		SessionID: "7c9e6679-0000-4000-8000-000000000058",
		Status:    domain.GraphRunStatusSucceeded,
	}); err != nil {
		t.Fatalf("seed graph run: %v", err)
	}

	cfg := config.Config{
		Version:           "0.1.0",
		LLMMode:           "fake",
		EmailMode:         "log",
		PublicAppURL:      "https://talentscout.vercel.app",
		RecruiterPassword: "correct horse",
		AuthTokenSecret:   "0123456789abcdef0123456789abcdef",
	}
	return httpapi.NewRouter(cfg, httpapi.Deps{
		Store:  st,
		LLM:    llm.NewFake(graph.FakeReplies),
		Models: testGraphModels(),
	}), cfg
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
		{name: "job detail needs credentials", target: "/api/v1/jobs/7c9e6679-7425-40de-944b-e07fc1f90ae7", want: http.StatusUnauthorized},
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

func TestSSETokenEndpointMatchesContract(t *testing.T) {
	router, cfg := authRouter(t)
	access := login(t, router)
	validator := conformance.New(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sse-token", strings.NewReader(`{"run_id":"`+authRouterRunID+`"}`))
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())

	var got struct {
		SSEToken  string    `json:"sse_token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode sse-token response: %v", err)
	}
	if got.SSEToken == "" {
		t.Fatal("sse_token is empty")
	}

	claims, err := auth.New(cfg.AuthTokenSecret).VerifySSEToken(got.SSEToken, authRouterRunID)
	if err != nil {
		t.Fatalf("issued sse_token does not verify: %v", err)
	}
	if claims.Audience != auth.AudienceSSE {
		t.Errorf("aud = %q, want %q", claims.Audience, auth.AudienceSSE)
	}
	if claims.RunID != authRouterRunID {
		t.Errorf("run_id = %q, want %q", claims.RunID, authRouterRunID)
	}
	if !claims.ExpiresAt.Equal(got.ExpiresAt) {
		t.Errorf("expires_at = %v, verified claim = %v", got.ExpiresAt, claims.ExpiresAt)
	}
	if got.ExpiresAt.Nanosecond() != 0 {
		t.Errorf("expires_at = %v, want whole seconds", got.ExpiresAt)
	}
}

func TestSSETokenEndpointRejectsBadRequests(t *testing.T) {
	router, _ := authRouter(t)
	access := login(t, router)
	validator := conformance.New(t)

	tests := []struct {
		name   string
		body   string
		token  string
		target string
		status int
	}{
		{name: "missing bearer", body: `{"run_id":"` + authRouterRunID + `"}`, target: "/api/v1/auth/sse-token", status: http.StatusUnauthorized},
		{name: "access_token in query", body: `{"run_id":"` + authRouterRunID + `"}`, token: "", target: "/api/v1/auth/sse-token?access_token=" + access, status: http.StatusUnauthorized},
		{name: "unknown run", body: `{"run_id":"7c9e6679-0000-4000-8000-000000000099"}`, token: access, target: "/api/v1/auth/sse-token", status: http.StatusNotFound},
		{name: "malformed run id", body: `{"run_id":"not-a-uuid"}`, token: access, target: "/api/v1/auth/sse-token", status: http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.target, strings.NewReader(tc.body))
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
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

func TestRunSSERequiresSSEToken(t *testing.T) {
	router, cfg := authRouter(t)
	access := login(t, router)
	validator := conformance.New(t)

	manager := auth.New(cfg.AuthTokenSecret)
	valid, _, err := manager.IssueSSEToken(authRouterRunID)
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}
	otherRun, _, err := manager.IssueSSEToken("7c9e6679-0000-4000-8000-000000000099")
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}
	expiredIssuer := auth.NewWithClock(cfg.AuthTokenSecret, func() time.Time {
		return time.Now().Add(-2 * time.Minute)
	})
	expired, _, err := expiredIssuer.IssueSSEToken(authRouterRunID)
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}

	path := "/api/v1/graph-runs/" + authRouterRunID + "/events"
	tests := []struct {
		name   string
		target string
		header string
		status int
	}{
		{name: "valid sse token", target: path + "?sse_token=" + valid, status: http.StatusOK},
		{name: "missing token", target: path, status: http.StatusUnauthorized},
		{name: "expired token", target: path + "?sse_token=" + expired, status: http.StatusUnauthorized},
		{name: "wrong audience", target: path + "?sse_token=" + access, status: http.StatusUnauthorized},
		{name: "wrong run id", target: path + "?sse_token=" + otherRun, status: http.StatusUnauthorized},
		{name: "access_token in query", target: path + "?access_token=" + access, status: http.StatusUnauthorized},
		{name: "bearer header only", target: path, header: "Bearer " + access, status: http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
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
