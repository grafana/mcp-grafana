package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimit_BucketRefills(t *testing.T) {
	limiter := NewIPLimiter(2, 100*time.Millisecond, true)
	handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	doReq := func() int {
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		r.RemoteAddr = "10.0.0.1:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}

	if doReq() != http.StatusOK {
		t.Errorf("first")
	}
	if doReq() != http.StatusOK {
		t.Errorf("second")
	}
	if doReq() != http.StatusTooManyRequests {
		t.Errorf("third should be limited")
	}
	time.Sleep(150 * time.Millisecond)
	if doReq() != http.StatusOK {
		t.Errorf("after refill should pass")
	}
}

// TestRateLimit_ContinuousRefill_NoBoundaryBurst confirms that after
// exhausting tokens, the bucket regenerates one token per (refill/max)
// duration rather than refilling all-at-once at the window boundary —
// preventing the 2×max burst that a fixed-window limiter would allow.
func TestRateLimit_ContinuousRefill_NoBoundaryBurst(t *testing.T) {
	// max=4, refill=200ms → one token regenerates every 50ms.
	limiter := NewIPLimiter(4, 200*time.Millisecond, true)
	allow := func() bool { return limiter.allow("10.0.0.1") }

	// Drain the bucket.
	for i := 0; i < 4; i++ {
		if !allow() {
			t.Fatalf("initial drain: request %d unexpectedly limited", i+1)
		}
	}
	if allow() {
		t.Fatalf("expected limit after drain")
	}

	// Sleep just over one perToken interval (50ms). One token should be
	// available, not all 4.
	time.Sleep(60 * time.Millisecond)
	if !allow() {
		t.Errorf("expected one regenerated token after 60ms")
	}
	if allow() {
		t.Errorf("expected only one regenerated token, second request should be limited")
	}
}

func TestRateLimit_IdleBucketsAreEvicted(t *testing.T) {
	// Use a short refill so a single sleep covers the 4× idle threshold.
	limiter := NewIPLimiter(2, 50*time.Millisecond)

	// Hit the limiter from many distinct IPs.
	for i := 0; i < 50; i++ {
		ip := "10.0.0." + string(rune('a'+i)) + ":1234" // 50 distinct strings
		_ = limiter.allow(ip)
	}
	limiter.mu.Lock()
	before := len(limiter.buckets)
	limiter.mu.Unlock()
	if before < 50 {
		t.Fatalf("expected ≥50 buckets after 50 unique IPs, got %d", before)
	}

	// Wait long enough that all buckets are idle past the 4× cutoff,
	// then trigger a sweep by hitting one fresh IP.
	time.Sleep(250 * time.Millisecond)
	_ = limiter.allow("99.99.99.99:1")

	limiter.mu.Lock()
	after := len(limiter.buckets)
	limiter.mu.Unlock()

	// Only the freshly-touched IP should remain.
	if after != 1 {
		t.Errorf("after sweep: %d buckets, want 1", after)
	}
}

func TestRateLimit_PerIPIsolation(t *testing.T) {
	limiter := NewIPLimiter(1, time.Second, true)
	handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, ip := range []string{"1.1.1.1:1", "2.2.2.2:1", "3.3.3.3:1"} {
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		r.RemoteAddr = ip
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("%s status=%d", ip, w.Code)
		}
	}
}

func TestRateLimit_PrefersForwardedHeaders(t *testing.T) {
	// Two requests arrive from the same proxy IP but with different
	// X-Forwarded-For — they must occupy independent buckets.
	limiter := NewIPLimiter(1, time.Second, true)
	handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	doReq := func(xff string) int {
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		r.RemoteAddr = "10.0.0.1:1234" // same proxy
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if doReq("1.1.1.1") != http.StatusOK {
		t.Errorf("first client should get its own bucket")
	}
	if doReq("2.2.2.2") != http.StatusOK {
		t.Errorf("second client should get its own bucket")
	}
	if doReq("1.1.1.1") != http.StatusTooManyRequests {
		t.Errorf("first client must hit its own per-bucket cap, not share a global one")
	}
}

func TestRateLimit_SweepDropsIdleBuckets(t *testing.T) {
	l := NewIPLimiter(1, 10*time.Millisecond, true)
	// Plant an old bucket directly so we do not have to wait real time.
	now := time.Now()
	l.buckets["stale"] = &bucket{tokens: 1, updatedAt: now.Add(-5 * l.refill)}
	l.buckets["fresh"] = &bucket{tokens: 1, updatedAt: now}
	l.mu.Lock()
	l.sweepBucketsLocked(now)
	l.mu.Unlock()
	if _, ok := l.buckets["stale"]; ok {
		t.Errorf("idle bucket was not swept")
	}
	if _, ok := l.buckets["fresh"]; !ok {
		t.Errorf("fresh bucket was incorrectly swept")
	}
}
