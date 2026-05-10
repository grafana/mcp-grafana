package auth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// IPLimiter is a per-IP token bucket. Buckets are lazily allocated and
// garbage-collected periodically.
type IPLimiter struct {
	max    int
	refill time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens    int
	updatedAt time.Time
}

// NewIPLimiter creates a limiter that allows up to max requests per refill
// window per IP.
func NewIPLimiter(max int, refill time.Duration) *IPLimiter {
	return &IPLimiter{
		max:     max,
		refill:  refill,
		buckets: make(map[string]*bucket),
	}
}

// Wrap returns an http.Handler that rate-limits by source IP.
func (l *IPLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			httpError(w, http.StatusTooManyRequests, "too_many_requests", "rate limited")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *IPLimiter) allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		l.buckets[ip] = &bucket{tokens: l.max - 1, updatedAt: now}
		return true
	}
	elapsed := now.Sub(b.updatedAt)
	if elapsed >= l.refill {
		b.tokens = l.max
		b.updatedAt = now
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

func clientIP(r *http.Request) string {
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}
