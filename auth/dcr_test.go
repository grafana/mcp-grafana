package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDCR_HappyPath(t *testing.T) {
	store := NewMemoryStore()
	h := DCRHandler(store)

	body := bytes.NewBufferString(`{
		"client_name": "Claude Desktop",
		"redirect_uris": ["http://127.0.0.1:33418/callback"],
		"token_endpoint_auth_method": "none"
	}`)
	r := httptest.NewRequest(http.MethodPost, "/register", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	cid, _ := got["client_id"].(string)
	if cid == "" {
		t.Fatalf("client_id missing: %s", w.Body)
	}
	if _, ok := got["client_secret"]; ok {
		t.Errorf("public client should have no client_secret")
	}

	saved, err := store.GetClient(context.Background(), cid)
	if err != nil {
		t.Fatalf("client not persisted: %v", err)
	}
	if saved.ClientName != "Claude Desktop" {
		t.Errorf("client_name=%q", saved.ClientName)
	}
}

func TestDCR_RejectsNonHTTPSRedirect(t *testing.T) {
	store := NewMemoryStore()
	h := DCRHandler(store)
	body := bytes.NewBufferString(`{
		"redirect_uris": ["http://evil.example/cb"]
	}`)
	r := httptest.NewRequest(http.MethodPost, "/register", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDCR_AllowsLoopback(t *testing.T) {
	store := NewMemoryStore()
	h := DCRHandler(store)
	for _, uri := range []string{
		"http://localhost:1234/cb",
		"http://127.0.0.1:1234/cb",
		"http://[::1]:1234/cb",
	} {
		body := bytes.NewBufferString(`{"redirect_uris":["` + uri + `"]}`)
		r := httptest.NewRequest(http.MethodPost, "/register", body)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Errorf("loopback %q rejected: %d %s", uri, w.Code, w.Body)
		}
	}
}

func TestDCR_RequiresAtLeastOneRedirect(t *testing.T) {
	h := DCRHandler(NewMemoryStore())
	body := bytes.NewBufferString(`{"client_name":"x"}`)
	r := httptest.NewRequest(http.MethodPost, "/register", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d", w.Code)
	}
}

func TestDCR_RejectsRedirectWithFragment(t *testing.T) {
	store := NewMemoryStore()
	h := DCRHandler(store)
	body := bytes.NewBufferString(`{
		"redirect_uris": ["http://localhost:1234/cb#frag"]
	}`)
	r := httptest.NewRequest(http.MethodPost, "/register", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDCR_RejectsNonPost(t *testing.T) {
	h := DCRHandler(NewMemoryStore())
	r := httptest.NewRequest(http.MethodGet, "/register", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d", w.Code)
	}
	if got := w.Header().Get("Allow"); got != "POST" {
		t.Errorf("Allow=%q want POST", got)
	}
}
