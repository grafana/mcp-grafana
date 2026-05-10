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

	// trustForwarded controls whether X-Forwarded-For / X-Real-IP
	// headers are honoured when bucketing. Default false: only
	// r.RemoteAddr is used, which is correct when mcp-grafana is the
	// edge listener. Set to true ONLY when the operator runs a header-
	// stripping proxy in front; otherwise an attacker can rotate XFF
	// per request and bypass per-IP limits entirely.
	trustForwarded bool

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSwept time.Time
}

type bucket struct {
	tokens    int
	updatedAt time.Time
}

// NewIPLimiter creates a limiter that allows up to max requests per refill
// window per IP. trustForwarded honours X-Forwarded-For / X-Real-IP — only
// pass true when a header-stripping reverse proxy is in front; otherwise
// an attacker can spoof XFF and bypass the limit.
func NewIPLimiter(max int, refill time.Duration, trustForwarded bool) *IPLimiter {
	return &IPLimiter{
		max:            max,
		refill:         refill,
		trustForwarded: trustForwarded,
		buckets:        make(map[string]*bucket),
	}
}

// Wrap returns an http.Handler that rate-limits by source IP.
func (l *IPLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r, l.trustForwarded)) {
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
// behind a reverse proxy and the operator has confirmed the proxy strips
// inbound X-Forwarded-For / X-Real-IP from untrusted clients (via
// trustForwarded=true, plumbed from --trust-forwarded-headers), we honour
// XFF (first hop) and XRI so the limiter scopes to the original client.
//
// Without that opt-in we fall back to r.RemoteAddr unconditionally —
// blindly trusting XFF would let any attacker rotate the header per
// request and bypass per-IP limits entirely, which is worse than the
// "every request looks like the proxy" failure mode of always using
// RemoteAddr.
func clientIP(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// XFF is "client, proxy1, proxy2, ..." — the first non-empty
			// entry is the original client.
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
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}
