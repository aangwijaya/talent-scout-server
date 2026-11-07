package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
)

// Apply rate limit: 5 requests per client IP per hour.
const (
	ApplyRateLimit       = 5
	ApplyRateLimitWindow = time.Hour
)

type bucket struct {
	windowStart time.Time
	count       int
}

// RateLimiter is a fixed-window in-memory limiter keyed by an opaque string.
type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*bucket
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, buckets: make(map[string]*bucket)}
}

func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b := l.buckets[key]
	if b == nil || now.Sub(b.windowStart) >= l.window {
		b = &bucket{windowStart: now}
		l.buckets[key] = b
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}

// clientIP returns the TCP remote address host. The trusted X-Forwarded-For
// handling (X-BFF-Secret) is added by #21; until then this is the remote address.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimit rejects requests once the key exceeds the limiter's budget.
func RateLimit(limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow(clientIP(r)) {
				apierror.Write(w, http.StatusTooManyRequests, apierror.CodeRateLimited, "too many requests", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
