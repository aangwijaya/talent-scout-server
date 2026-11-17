package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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

func rateLimitLoginRequest(body, remoteAddr, xff string) *http.Request {
	r := loginRequest(body)
	if remoteAddr != "" {
		r.RemoteAddr = remoteAddr
	}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func assertRateLimited(t *testing.T, rec *httptest.ResponseRecorder, maxRetry int) {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body: %s)", rec.Code, rec.Body.String())
	}
	code, _, _ := decodeError(t, rec)
	if code != "rate_limited" {
		t.Errorf("error.code = %q, want rate_limited", code)
	}
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < 1 || retry > maxRetry {
		t.Errorf("Retry-After = %q, want 1..%d seconds", rec.Header().Get("Retry-After"), maxRetry)
	}
}

func TestLoginLimitsFailedAttemptsPerIP(t *testing.T) {
	cfg := authConfig()
	cfg.LoginMaxAttempts = 2
	cfg.LoginWindowMinutes = 15
	h := handlers.NewAuth(cfg, auth.New(cfg.AuthTokenSecret), nil)

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.Login(rec, loginRequest(`{"password":"wrong"}`))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d status = %d, want 401", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h.Login(rec, loginRequest(`{"password":"correct horse"}`))
	assertRateLimited(t, rec, 15*60)
}

func TestLoginSuccessDoesNotCountTowardLimit(t *testing.T) {
	cfg := authConfig()
	cfg.LoginMaxAttempts = 1
	cfg.LoginWindowMinutes = 15
	h := handlers.NewAuth(cfg, auth.New(cfg.AuthTokenSecret), nil)

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.Login(rec, loginRequest(`{"password":"correct horse"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("successful login %d status = %d, want 200", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h.Login(rec, loginRequest(`{"password":"wrong"}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("first failed login status = %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.Login(rec, loginRequest(`{"password":"wrong"}`))
	assertRateLimited(t, rec, 15*60)
}

func TestLoginRateLimitKeysByTrustedClientIP(t *testing.T) {
	tests := []struct {
		name      string
		bffSecret string
		header    string
	}{
		{name: "matching BFF secret trusts XFF", bffSecret: "shared-secret", header: "shared-secret"},
		{name: "wrong BFF secret ignores XFF", bffSecret: "shared-secret", header: "wrong"},
		{name: "missing BFF secret ignores XFF", bffSecret: "shared-secret", header: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := authConfig()
			cfg.LoginMaxAttempts = 1
			cfg.LoginWindowMinutes = 15
			cfg.BFFSharedSecret = tc.bffSecret
			h := handlers.NewAuth(cfg, auth.New(cfg.AuthTokenSecret), nil)

			req := func(xff string) *http.Request {
				r := rateLimitLoginRequest(`{"password":"wrong"}`, "10.0.0.5:1234", xff)
				if tc.header != "" {
					r.Header.Set("X-BFF-Secret", tc.header)
				}
				return r
			}

			rec := httptest.NewRecorder()
			h.Login(rec, req("203.0.113.7"))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("first IP A status = %d, want 401", rec.Code)
			}

			rec = httptest.NewRecorder()
			h.Login(rec, req("203.0.113.7"))
			assertRateLimited(t, rec, 15*60)

			rec = httptest.NewRecorder()
			h.Login(rec, req("203.0.113.8"))
			if tc.bffSecret != "" && tc.header == tc.bffSecret {
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("IP B status = %d, want 401 (trusted XFF keys are independent)", rec.Code)
				}
			} else {
				// A different XFF must not buy a new budget when the caller
				// did not prove it is the BFF.
				assertRateLimited(t, rec, 15*60)
			}
		})
	}
}

func TestLoginRateLimitNeverTrustsXFFWithoutConfiguredSecret(t *testing.T) {
	cfg := authConfig()
	cfg.LoginMaxAttempts = 1
	cfg.LoginWindowMinutes = 15
	cfg.BFFSharedSecret = ""
	h := handlers.NewAuth(cfg, auth.New(cfg.AuthTokenSecret), nil)

	req := func(xff string) *http.Request {
		r := rateLimitLoginRequest(`{"password":"wrong"}`, "10.0.0.5:1234", xff)
		r.Header.Set("X-BFF-Secret", "")
		return r
	}

	rec := httptest.NewRecorder()
	h.Login(rec, req("203.0.113.7"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("first attempt status = %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.Login(rec, req("203.0.113.8"))
	assertRateLimited(t, rec, 15*60)
}

// signalReader reports, once, that the handler started reading the body,
// which means the request already passed the rate-limit check.
type signalReader struct {
	r    io.Reader
	once sync.Once
	read chan struct{}
}

func (s *signalReader) Read(p []byte) (int, error) {
	s.once.Do(func() { close(s.read) })
	return s.r.Read(p)
}

// A client that sends its body slowly must not get past the limit: the slot
// is taken before the body is read, not after the password check fails.
func TestLoginConcurrentFailuresCapped(t *testing.T) {
	const attempts = 50
	cfg := authConfig()
	cfg.LoginMaxAttempts = 5
	cfg.LoginWindowMinutes = 15
	h := handlers.NewAuth(cfg, auth.New(cfg.AuthTokenSecret), nil)

	codes := make([]int, attempts)
	writers := make([]*io.PipeWriter, attempts)
	reading := make([]chan struct{}, attempts)
	done := make([]chan struct{}, attempts)
	for i := 0; i < attempts; i++ {
		pr, pw := io.Pipe()
		writers[i] = pw
		reading[i] = make(chan struct{})
		done[i] = make(chan struct{})
		body := &signalReader{r: pr, read: reading[i]}
		go func(i int) {
			defer close(done[i])
			rec := httptest.NewRecorder()
			h.Login(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body))
			codes[i] = rec.Code
		}(i)
	}

	// Wait until every request has either been rejected or is blocked reading
	// its body, so all of them passed the limit check before any failure.
	for i := 0; i < attempts; i++ {
		select {
		case <-reading[i]:
		case <-done[i]:
		case <-time.After(5 * time.Second):
			t.Fatalf("request %d neither read its body nor finished", i)
		}
	}
	for i := 0; i < attempts; i++ {
		go func(w *io.PipeWriter) {
			_, _ = w.Write([]byte(`{"password":"wrong"}`))
			_ = w.Close()
		}(writers[i])
	}
	for i := 0; i < attempts; i++ {
		<-done[i]
	}

	var unauthorized, limited int
	for _, code := range codes {
		switch code {
		case http.StatusUnauthorized:
			unauthorized++
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Errorf("unexpected status %d", code)
		}
	}
	if unauthorized > cfg.LoginMaxAttempts {
		t.Errorf("%d attempts were checked against the password, want at most %d", unauthorized, cfg.LoginMaxAttempts)
	}
	if limited != attempts-unauthorized {
		t.Errorf("429 count = %d, want %d", limited, attempts-unauthorized)
	}
}
