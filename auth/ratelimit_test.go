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
