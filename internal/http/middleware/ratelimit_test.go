package middleware

import (
	"fmt"
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

func TestRateLimiterPrunesExpiredBuckets(t *testing.T) {
	limiter := NewRateLimiter(1, time.Hour)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	const keys = 100
	for i := 0; i < keys; i++ {
		limiter.Allow(fmt.Sprintf("ip-%d", i))
	}
	if len(limiter.buckets) != keys {
		t.Fatalf("buckets before sweep = %d, want %d", len(limiter.buckets), keys)
	}

	now = now.Add(2 * time.Hour)
	limiter.Allow("new-ip")

	if len(limiter.buckets) != 1 {
		t.Fatalf("buckets after sweep = %d, want 1", len(limiter.buckets))
	}
	if _, ok := limiter.buckets["new-ip"]; !ok {
		t.Fatal("the key used to trigger the sweep should still be present")
	}
}

func TestRateLimitMiddlewareWrites429(t *testing.T) {
	limiter := NewRateLimiter(1, time.Hour)
	handler := RateLimit(limiter, "")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestRateLimitMiddlewareKeysByTrustedIP(t *testing.T) {
	limiter := NewRateLimiter(1, time.Hour)
	handler := RateLimit(limiter, "shared-secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := func(xff string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/x/applications", nil)
		r.RemoteAddr = "10.0.0.5:1234"
		r.Header.Set("X-BFF-Secret", "shared-secret")
		r.Header.Set("X-Forwarded-For", xff)
		return r
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req("203.0.113.1"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first request for IP A = %d, want 204", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req("203.0.113.1"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request for IP A = %d, want 429", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req("203.0.113.2"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("request for IP B = %d, want 204 (trusted IPs must be independent)", rec.Code)
	}
}

func TestClientIPTrustsXFFWithCorrectSecret(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-BFF-Secret", "shared-secret")
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.5")

	if got := clientIP(r, "shared-secret"); got != "203.0.113.7" {
		t.Fatalf("clientIP = %q, want 203.0.113.7", got)
	}
}

func TestClientIPIgnoresXFFWithWrongSecret(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-BFF-Secret", "wrong")
	r.Header.Set("X-Forwarded-For", "203.0.113.7")

	if got := clientIP(r, "shared-secret"); got != "10.0.0.5" {
		t.Fatalf("clientIP = %q, want 10.0.0.5", got)
	}
}

func TestClientIPIgnoresXFFWithoutSecretHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")

	if got := clientIP(r, "shared-secret"); got != "10.0.0.5" {
		t.Fatalf("clientIP = %q, want 10.0.0.5", got)
	}
}

func TestClientIPIgnoresXFFWhenSecretEmpty(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-BFF-Secret", "shared-secret")
	r.Header.Set("X-Forwarded-For", "203.0.113.7")

	if got := clientIP(r, ""); got != "10.0.0.5" {
		t.Fatalf("clientIP = %q, want 10.0.0.5 (empty env must never trust XFF)", got)
	}
}

func TestClientIPFallsBackToRemoteAddrWhenXFFEmpty(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-BFF-Secret", "shared-secret")
	r.Header.Set("X-Forwarded-For", "")

	if got := clientIP(r, "shared-secret"); got != "10.0.0.5" {
		t.Fatalf("clientIP = %q, want 10.0.0.5", got)
	}
}
