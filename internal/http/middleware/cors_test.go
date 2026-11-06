package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/http/middleware"
)

func TestCORSOnlyOnSSEGETRoutes(t *testing.T) {
	handler := middleware.CORS([]string{"http://localhost:5173"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		method     string
		path       string
		origin     string
		wantOrigin string
		wantStatus int
	}{
		{
			name:       "session SSE allowed",
			method:     http.MethodGet,
			path:       "/api/v1/sessions/abc/events",
			origin:     "http://localhost:5173",
			wantOrigin: "http://localhost:5173",
			wantStatus: http.StatusOK,
		},
		{
			name:       "run SSE allowed",
			method:     http.MethodGet,
			path:       "/api/v1/graph-runs/123/events",
			origin:     "http://localhost:5173",
			wantOrigin: "http://localhost:5173",
			wantStatus: http.StatusOK,
		},
		{
			name:       "disallowed origin gets no headers",
			method:     http.MethodGet,
			path:       "/api/v1/sessions/abc/events",
			origin:     "http://evil.example",
			wantStatus: http.StatusOK,
		},
		{
			name:       "REST route gets no headers",
			method:     http.MethodGet,
			path:       "/api/v1/jobs",
			origin:     "http://localhost:5173",
			wantStatus: http.StatusOK,
		},
		{
			name:       "preflight allowed",
			method:     http.MethodOptions,
			path:       "/api/v1/sessions/abc/events",
			origin:     "http://localhost:5173",
			wantOrigin: "http://localhost:5173",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "preflight disallowed",
			method:     http.MethodOptions,
			path:       "/api/v1/sessions/abc/events",
			origin:     "http://evil.example",
			wantStatus: http.StatusNoContent,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Origin", tc.origin)
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tc.wantOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tc.wantOrigin)
			}
			if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Error("Access-Control-Allow-Credentials must not be set")
			}
		})
	}
}
