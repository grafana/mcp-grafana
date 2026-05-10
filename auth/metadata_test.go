package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestASMetadata(t *testing.T) {
	h := ASMetadataHandler("https://mcp.example.com")
	r := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type=%q", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"issuer", "authorization_endpoint", "token_endpoint",
		"registration_endpoint", "code_challenge_methods_supported",
		"response_types_supported", "grant_types_supported",
	} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
	if got["issuer"] != "https://mcp.example.com" {
		t.Errorf("issuer=%v", got["issuer"])
	}

	methods, _ := got["code_challenge_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("code_challenge_methods_supported=%v (S256 only)", methods)
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	h := ProtectedResourceMetadataHandler("https://mcp.example.com")
	r := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["resource"] != "https://mcp.example.com" {
		t.Errorf("resource=%v", got["resource"])
	}
	auths, _ := got["authorization_servers"].([]any)
	if len(auths) != 1 || auths[0] != "https://mcp.example.com" {
		t.Errorf("authorization_servers=%v", auths)
	}
}
