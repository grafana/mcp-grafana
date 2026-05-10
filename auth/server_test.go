package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRegisterRoutes_AllPathsRespond(t *testing.T) {
	srv := &Server{
		Metrics:        NewMetrics(),
		PublicURL:      "https://mcp.example.com",
		Store:          NewMemoryStore(),
		Encryptor:      mustEnc(t, mustKey(t), nil),
		Upstream:       &stubUpstream{mode: ModeOAuthOIDC, authURL: "https://idp.example/auth"},
		AccessTokenTTL: time.Hour, RefreshTokenTTL: 24 * time.Hour, AuthCodeTTL: 5 * time.Minute,
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux, "https://grafana.example.com", true)

	for _, path := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource",
	} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("%s status=%d", path, w.Code)
		}
	}
	// /authorize without params is a 400, but it should still resolve (i.e. not 404).
	r := httptest.NewRequest(http.MethodGet, "/authorize", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code == http.StatusNotFound {
		t.Errorf("/authorize not registered")
	}
}

func TestRegisterRoutes_SAMLRoutesMountedWhenSAMLValidator(t *testing.T) {
	srv := &Server{
		PublicURL:       "https://mcp.example.com",
		Store:           NewMemoryStore(),
		Encryptor:       mustEnc(t, mustKey(t), nil),
		Upstream:        &stubSAMLValidator{metadata: []byte(`<EntityDescriptor></EntityDescriptor>`)},
		AuthCodeTTL:     5 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
		AccessTokenTTL:  time.Hour,
		SAMLEnableSLO:   true,
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux, "https://grafana.example.com", true)

	for _, path := range []string{"/saml/metadata", "/saml/acs", "/saml/sls"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code == http.StatusNotFound {
			t.Errorf("%s not registered (404)", path)
		}
	}
}
