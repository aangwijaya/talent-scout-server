package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
)

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func testConfig() config.Config {
	return config.Config{Version: "0.1.0", LLMMode: "openrouter", EmailMode: "log"}
}

func TestHealthDefault(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)

	handlers.NewHealth(testConfig(), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "ok" {
		t.Errorf("status = %v, want ok", got["status"])
	}
	if got["version"] != "0.1.0" {
		t.Errorf("version = %v, want 0.1.0", got["version"])
	}
	if got["llm_mode"] != "openrouter" {
		t.Errorf("llm_mode = %v, want openrouter", got["llm_mode"])
	}
	if got["email_mode"] != "log" {
		t.Errorf("email_mode = %v, want log", got["email_mode"])
	}
	if _, ok := got["db"]; ok {
		t.Error("db field present without deep=1")
	}
}

func TestHealthUsesEmailModeFromConfig(t *testing.T) {
	cfg := testConfig()
	cfg.EmailMode = "gmail"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	handlers.NewHealth(cfg, nil).ServeHTTP(rec, req)

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["email_mode"] != "gmail" {
		t.Errorf("email_mode = %v, want gmail", got["email_mode"])
	}
}

func TestHealthDeep(t *testing.T) {
	tests := []struct {
		name       string
		db         handlers.DBPinger
		wantStatus int
		wantDB     string
		wantCode   string
	}{
		{
			name:       "database ok",
			db:         pingerFunc(func(context.Context) error { return nil }),
			wantStatus: http.StatusOK,
			wantDB:     "ok",
		},
		{
			name:       "database error",
			db:         pingerFunc(func(context.Context) error { return errors.New("paused") }),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "db_unavailable",
		},
		{
			name:       "database not configured",
			db:         nil,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "db_unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/health?deep=1", nil)
			handlers.NewHealth(testConfig(), tc.db).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}

			if tc.wantCode != "" {
				var got struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if got.Error.Code != tc.wantCode {
					t.Errorf("error.code = %q, want %q", got.Error.Code, tc.wantCode)
				}
				return
			}

			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got["db"] != tc.wantDB {
				t.Errorf("db = %v, want %q", got["db"], tc.wantDB)
			}
		})
	}
}
