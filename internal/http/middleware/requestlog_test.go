package middleware_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/http/middleware"
)

func TestRequestLoggerRedactsSecrets(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	r := chi.NewRouter()
	r.Use(middleware.RequestLogger(logger))
	r.Get("/api/v1/sessions/{token}/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	const (
		rawToken   = "tok-raw-abc123"
		queryValue = "query-raw-xyz"
		authValue  = "bearer-raw-999"
		bffValue   = "bff-raw-777"
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+rawToken+"/events?secret="+queryValue, nil)
	req.Header.Set("Authorization", "Bearer "+authValue)
	req.Header.Set("X-BFF-Secret", bffValue)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	out := buf.String()

	if !strings.Contains(out, "/api/v1/sessions/{token}/events") {
		t.Errorf("log must contain the route pattern, got:\n%s", out)
	}

	for _, secret := range []string{rawToken, queryValue, authValue, bffValue} {
		if strings.Contains(out, secret) {
			t.Errorf("log must not contain %q, got:\n%s", secret, out)
		}
	}

	if strings.Contains(out, req.URL.Path) {
		t.Errorf("log must not contain the raw request path %q, got:\n%s", req.URL.Path, out)
	}
}

func TestRequestLoggerRedactsRunSSEQueryAndHeaders(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	r := chi.NewRouter()
	r.Use(middleware.RequestLogger(logger))
	r.Get("/api/v1/graph-runs/{runId}/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	const (
		runID      = "7c9e6679-0000-4000-8000-000000000057"
		sseToken   = "sse-raw-token"
		authValue  = "bearer-raw-999"
		bffValue   = "bff-raw-777"
		queryValue = "since-raw-42"
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/graph-runs/"+runID+"/events?sse_token="+sseToken+"&since_seq="+queryValue, nil)
	req.Header.Set("Authorization", "Bearer "+authValue)
	req.Header.Set("X-BFF-Secret", bffValue)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	out := buf.String()

	if !strings.Contains(out, "/api/v1/graph-runs/{runId}/events") {
		t.Errorf("log must contain the route pattern, got:\n%s", out)
	}

	for _, secret := range []string{runID, sseToken, queryValue, authValue, bffValue} {
		if strings.Contains(out, secret) {
			t.Errorf("log must not contain %q, got:\n%s", secret, out)
		}
	}
}
