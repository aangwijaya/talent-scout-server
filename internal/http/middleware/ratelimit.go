package middleware

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
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
	mu        sync.Mutex
	limit     int
	window    time.Duration
	now       func() time.Time
	buckets   map[string]*bucket
	lastSweep time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}
}

func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

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

// sweep deletes expired buckets, at most once per window, so Allow stays O(1)
// on average instead of growing unbounded as distinct keys are seen.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	for key, b := range l.buckets {
		if now.Sub(b.windowStart) >= l.window {
			delete(l.buckets, key)
		}
	}
	l.lastSweep = now
}

// clientIP returns the client IP the apply rate limit counts. It trusts the
// first X-Forwarded-For entry only when secret is non-empty and matches the
// X-BFF-Secret header (constant-time); otherwise it returns the TCP remote
// address and ignores X-Forwarded-For.
func clientIP(r *http.Request, secret string) string {
	if secret != "" && subtle.ConstantTimeCompare([]byte(secret), []byte(r.Header.Get("X-BFF-Secret"))) == 1 {
		if ip := firstForwardedFor(r.Header.Get("X-Forwarded-For")); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// firstForwardedFor returns the first (leftmost) entry of an X-Forwarded-For
// header, which is the client IP. It returns "" for an empty header.
func firstForwardedFor(xff string) string {
	if i := strings.IndexByte(xff, ','); i >= 0 {
		xff = xff[:i]
	}
	return strings.TrimSpace(xff)
}

// RateLimit rejects requests once the key exceeds the limiter's budget.
func RateLimit(limiter *RateLimiter, secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow(clientIP(r, secret)) {
				apierror.Write(w, http.StatusTooManyRequests, apierror.CodeRateLimited, "too many requests", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
