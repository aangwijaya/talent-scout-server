package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/conformance"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
)

func TestHealthMatchesContract(t *testing.T) {
	cfg := config.Config{Version: "0.1.0", LLMMode: "fake", EmailMode: "log"}
	router := httpapi.NewRouter(cfg, nil)
	validator := conformance.New(t)

	tests := []struct {
		name   string
		target string
		status int
	}{
		{name: "shallow", target: "/api/v1/health", status: http.StatusOK},
		{name: "deep unavailable", target: "/api/v1/health?deep=1", status: http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			validator.ValidateResponse(t, req, rec.Result())
		})
	}
}
