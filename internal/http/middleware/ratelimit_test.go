package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterAllowWithinWindow(t *testing.T) {
	limiter := NewRateLimiter(3, time.Hour)

	for i := 0; i < 3; i++ {
		if !limiter.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if limiter.Allow("1.2.3.4") {
		t.Fatal("4th request within the window should be denied")
	}
}

func TestRateLimiterResetsAfterWindow(t *testing.T) {
	limiter := NewRateLimiter(1, 20*time.Millisecond)

	if !limiter.Allow("1.2.3.4") {
		t.Fatal("first request should be allowed")
	}
	if limiter.Allow("1.2.3.4") {
		t.Fatal("second request within the window should be denied")
	}

	time.Sleep(30 * time.Millisecond)
	if !limiter.Allow("1.2.3.4") {
		t.Fatal("request after the window should be allowed")
	}
}

func TestRateLimiterKeysAreIndependent(t *testing.T) {
	limiter := NewRateLimiter(1, time.Hour)

	if !limiter.Allow("1.1.1.1") {
		t.Fatal("first ip should be allowed")
	}
	if !limiter.Allow("2.2.2.2") {
		t.Fatal("second ip should be allowed")
	}
}

func TestRateLimitMiddlewareWrites429(t *testing.T) {
	limiter := NewRateLimiter(1, time.Hour)
	handler := RateLimit(limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	first := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/x/applications", nil)
	first.RemoteAddr = "1.2.3.4:1234"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, first)
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("first status = %d, want 204", rec1.Code)
	}

	second := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/x/applications", nil)
	second.RemoteAddr = "1.2.3.4:1234"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, second)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", rec2.Code)
	}
}
