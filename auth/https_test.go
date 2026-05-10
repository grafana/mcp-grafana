package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireHTTPS_AcceptsForwardedProto(t *testing.T) {
	guard := RequireHTTPS(false)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r := httptest.NewRequest(http.MethodGet, "/authorize", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	guard(next).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d", w.Code)
	}
}

func TestRequireHTTPS_RejectsPlain(t *testing.T) {
	guard := RequireHTTPS(false)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	r := httptest.NewRequest(http.MethodGet, "/authorize", nil)
	w := httptest.NewRecorder()
	guard(next).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status=%d", w.Code)
	}
}

func TestRequireHTTPS_AllowInsecureFlag(t *testing.T) {
	guard := RequireHTTPS(true)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	r := httptest.NewRequest(http.MethodGet, "/authorize", nil)
	w := httptest.NewRecorder()
	guard(next).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d", w.Code)
	}
}
