package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimit_BucketRefills(t *testing.T) {
	limiter := NewIPLimiter(2, 100*time.Millisecond)
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
	limiter := NewIPLimiter(4, 200*time.Millisecond)
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

func TestRateLimit_PerIPIsolation(t *testing.T) {
	limiter := NewIPLimiter(1, time.Second)
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
