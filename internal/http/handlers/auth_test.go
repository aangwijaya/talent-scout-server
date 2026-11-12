package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
)

func authConfig() config.Config {
	return config.Config{
		Version:           "0.1.0",
		LLMMode:           "fake",
		EmailMode:         "log",
		RecruiterPassword: "correct horse",
		AuthTokenSecret:   "0123456789abcdef0123456789abcdef",
	}
}

func loginRequest(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
}

func TestLoginReturnsValidToken(t *testing.T) {
	cfg := authConfig()
	manager := auth.NewWithClock(cfg.AuthTokenSecret, func() time.Time {
		return time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	})

	rec := httptest.NewRecorder()
	handlers.NewAuth(cfg, manager, nil).Login(rec, loginRequest(`{"password":"correct horse"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got struct {
		AccessToken string    `json:"access_token"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AccessToken == "" {
		t.Fatal("access_token is empty")
	}
	if _, err := manager.VerifyAccessToken(got.AccessToken); err != nil {
		t.Fatalf("issued token does not verify: %v", err)
	}
	if want := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC); !got.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want %v", got.ExpiresAt, want)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	cfg := authConfig()
	manager := auth.New(cfg.AuthTokenSecret)

	tests := []struct {
		name string
		body string
	}{
		{name: "wrong password", body: `{"password":"wrong"}`},
		{name: "empty password", body: `{"password":""}`},
		{name: "missing password", body: `{}`},
		{name: "malformed json", body: `{`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handlers.NewAuth(cfg, manager, nil).Login(rec, loginRequest(tc.body))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
			}
			var got struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Error.Code != "unauthorized" {
				t.Errorf("error.code = %q, want unauthorized", got.Error.Code)
			}
		})
	}
}

func TestLoginRejectsWhenPasswordNotConfigured(t *testing.T) {
	cfg := authConfig()
	cfg.RecruiterPassword = ""
	manager := auth.New(cfg.AuthTokenSecret)

	rec := httptest.NewRecorder()
	handlers.NewAuth(cfg, manager, nil).Login(rec, loginRequest(`{"password":""}`))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
