package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IPLimiter is a per-IP token bucket. Buckets are lazily allocated and
// opportunistically garbage-collected — any allow() call may sweep
// idle buckets at most once per refill window.
type IPLimiter struct {
	max    int
	refill time.Duration

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSwept time.Time
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
	l.sweepBucketsLocked(now)
	b, ok := l.buckets[ip]
	if !ok {
		l.buckets[ip] = &bucket{tokens: l.max - 1, updatedAt: now}
		return true
	}

	// Continuous refill: regenerate one token every refill/max nanoseconds,
	// capped at max. Advancing updatedAt by exactly the time consumed for
	// the tokens we added preserves fractional refill across calls and
	// prevents the 2×max boundary burst a fixed-window limiter allows.
	if l.refill > 0 && l.max > 0 {
		perToken := time.Duration(int64(l.refill) / int64(l.max))
		if perToken > 0 {
			elapsed := now.Sub(b.updatedAt)
			if earned := int(elapsed / perToken); earned > 0 {
				b.tokens += earned
				if b.tokens > l.max {
					b.tokens = l.max
				}
				b.updatedAt = b.updatedAt.Add(time.Duration(earned) * perToken)
			}
		}
	}

	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// sweepBucketsLocked drops buckets that have been idle long enough that
// they're guaranteed full (i.e. updatedAt older than refill * 2 — one full
// refill window of inactivity past their last refill tick). Caller must
// hold l.mu. Runs at most once per refill window so the amortised cost
// stays O(1) per allow() call under sustained traffic.
func (l *IPLimiter) sweepBucketsLocked(now time.Time) {
	if now.Sub(l.lastSwept) < l.refill {
		return
	}
	cutoff := now.Add(-2 * l.refill)
	for k, b := range l.buckets {
		if b.updatedAt.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
	l.lastSwept = now
}

// clientIP returns the source IP for rate-limiting. When mcp-grafana sits
// behind a reverse proxy (the typical production deployment, since the
// /authorize and /token endpoints require HTTPS), r.RemoteAddr is the
// proxy's address — every request looks identical and the limiter
// degenerates to a single global bucket. We honour X-Forwarded-For (first
// hop) and X-Real-IP when present so the limiter scopes to the original
// client. Operators MUST run a proxy that strips inbound XFF/XRI headers
// from untrusted clients, otherwise these headers are spoofable.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// XFF is "client, proxy1, proxy2, ..." — the first non-empty entry
		// is the original client.
		for _, raw := range strings.Split(xff, ",") {
			candidate := strings.TrimSpace(raw)
			if candidate != "" {
				return candidate
			}
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}
