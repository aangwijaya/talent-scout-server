package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
	"github.com/aangwijaya/talent-scout-server/internal/http/middleware"
)

const sseAuthSecret = "0123456789abcdef0123456789abcdef"

func sseAuthRouter(tokens *auth.Manager) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.SSEToken(tokens)).Get("/graph-runs/{runId}/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return r
}

func TestSSETokenAuthAcceptsQueryToken(t *testing.T) {
	manager := auth.New(sseAuthSecret)
	token, _, err := manager.IssueSSEToken("run-1")
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graph-runs/run-1/events?sse_token="+token, nil)
	sseAuthRouter(manager).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestSSETokenAuthRejectsBadCredentials(t *testing.T) {
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	manager := auth.NewWithClock(sseAuthSecret, func() time.Time { return now })
	router := sseAuthRouter(manager)

	valid, _, err := manager.IssueSSEToken("run-1")
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}
	otherRun, _, err := manager.IssueSSEToken("run-2")
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}
	access, _, err := manager.IssueAccessToken()
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	expiredIssuer := auth.NewWithClock(sseAuthSecret, func() time.Time {
		return now.Add(-2 * time.Minute)
	})
	expired, _, err := expiredIssuer.IssueSSEToken("run-1")
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}

	tests := []struct {
		name   string
		target string
		header string
		want   int
	}{
		{name: "missing token", target: "/graph-runs/run-1/events", want: http.StatusUnauthorized},
		{name: "expired token", target: "/graph-runs/run-1/events?sse_token=" + expired, want: http.StatusUnauthorized},
		{name: "wrong audience", target: "/graph-runs/run-1/events?sse_token=" + access, want: http.StatusUnauthorized},
		{name: "wrong run id", target: "/graph-runs/run-1/events?sse_token=" + otherRun, want: http.StatusUnauthorized},
		{name: "access_token in query", target: "/graph-runs/run-1/events?access_token=" + access, want: http.StatusUnauthorized},
		{name: "bearer header is not accepted", target: "/graph-runs/run-1/events", header: "Bearer " + access, want: http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
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

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graph-runs/run-1/events?sse_token="+valid, nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}
