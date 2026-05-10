package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// stubSAMLValidator is a fake upstream for handler tests. It bypasses real
// SAML XML and returns canned identity/attributes/logout data.
type stubSAMLValidator struct {
	metadata       []byte
	metadataErr    error
	assertion      samlAssertion
	assertErr      error
	logoutIdentity Identity
	logoutURL      string
	logoutErr      error
}

func (s *stubSAMLValidator) MetadataXML() ([]byte, error) {
	return s.metadata, s.metadataErr
}
func (s *stubSAMLValidator) ValidateAssertion(_ *http.Request) (samlAssertion, error) {
	return s.assertion, s.assertErr
}
func (s *stubSAMLValidator) BuildLogoutResponseURL(_ *http.Request) (Identity, string, error) {
	return s.logoutIdentity, s.logoutURL, s.logoutErr
}

// stubSAMLValidator must also satisfy Upstream (so we can store it on Server).
func (s *stubSAMLValidator) Mode() Mode                      { return ModeSAML }
func (s *stubSAMLValidator) AuthorizeURL(_, _ string) string { return "" }
func (s *stubSAMLValidator) HandleCallback(_ context.Context, _ url.Values) (CallbackResult, error) {
	return CallbackResult{}, nil
}
func (s *stubSAMLValidator) Refresh(_ context.Context, _ []byte) (CallbackResult, error) {
	return CallbackResult{}, ErrRefreshNotSupported
}

func TestSAMLMetadataHandler_ServesXML(t *testing.T) {
	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Upstream: &stubSAMLValidator{
			metadata: []byte(`<EntityDescriptor></EntityDescriptor>`),
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/saml/metadata", nil)
	w := httptest.NewRecorder()
	srv.SAMLMetadataHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "xml") {
		t.Errorf("Content-Type=%q", ct)
	}
	if !strings.Contains(w.Body.String(), "EntityDescriptor") {
		t.Errorf("body missing EntityDescriptor: %s", w.Body)
	}
}

func TestSAMLACSHandler_FirstLogin_RedirectsToBootstrap(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()
	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
		Upstream: &stubSAMLValidator{
			assertion: samlAssertion{
				Identity:   Identity{Mode: ModeSAML, ID: "alice@example.com"},
				RelayState: "rs-1",
			},
		},
		AuthCodeTTL: 5 * time.Minute,
	}
	srv.authzPendings().Store("rs-1", &pendingFlow{
		clientID:            "cid",
		redirectURI:         "http://localhost:1/cb",
		clientState:         "client-state",
		codeChallenge:       "x",
		codeChallengeMethod: "S256",
	})

	body := strings.NewReader("RelayState=rs-1&SAMLResponse=ignored-by-stub")
	r := httptest.NewRequest(http.MethodPost, "/saml/acs", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.SAMLACSHandler().ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if !strings.HasPrefix(loc.Path, "/bootstrap") {
		t.Errorf("loc=%v", loc)
	}
	if loc.Query().Get("flow") == "" {
		t.Errorf("flow token missing")
	}
}

func TestSAMLACSHandler_RepeatLogin_ShortcutsToClient(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()

	id := Identity{Mode: ModeSAML, ID: "alice@example.com"}
	ct, _ := enc.Seal([]byte("sa-token"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:       HashToken("old-tok"),
		Identity:        id,
		UpstreamCredsCT: ct,
		ExpiresAt:       time.Now().Add(time.Hour),
		CreatedAt:       time.Now(),
	})

	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
		Upstream: &stubSAMLValidator{
			assertion: samlAssertion{
				Identity:   id,
				RelayState: "rs-2",
			},
		},
		AuthCodeTTL: 5 * time.Minute,
	}
	srv.authzPendings().Store("rs-2", &pendingFlow{
		clientID:            "cid",
		redirectURI:         "http://localhost:1/cb",
		clientState:         "client-state",
		codeChallenge:       "x",
		codeChallengeMethod: "S256",
	})

	body := strings.NewReader("RelayState=rs-2&SAMLResponse=ignored-by-stub")
	r := httptest.NewRequest(http.MethodPost, "/saml/acs", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.SAMLACSHandler().ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Host != "localhost:1" || loc.Query().Get("code") == "" {
		t.Errorf("expected redirect back to client with code; got %v", loc)
	}
}

func TestSAMLACSHandler_InvalidAssertion_400(t *testing.T) {
	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Upstream: &stubSAMLValidator{
			assertErr: ErrSAMLInvalidAssertion,
		},
	}
	body := strings.NewReader("RelayState=anything&SAMLResponse=anything")
	r := httptest.NewRequest(http.MethodPost, "/saml/acs", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.SAMLACSHandler().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d", w.Code)
	}
}

func TestSAMLACSHandler_RejectsGET(t *testing.T) {
	srv := &Server{Upstream: &stubSAMLValidator{}}
	r := httptest.NewRequest(http.MethodGet, "/saml/acs", nil)
	w := httptest.NewRecorder()
	srv.SAMLACSHandler().ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d", w.Code)
	}
}

func TestSAMLSLSHandler_DeletesSession(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()

	id := Identity{Mode: ModeSAML, ID: "alice@example.com"}
	ct, _ := enc.Seal([]byte("sa-token"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:       HashToken("tok-1"),
		Identity:        id,
		UpstreamCredsCT: ct,
		ExpiresAt:       time.Now().Add(time.Hour),
	})

	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
		Upstream: &stubSAMLValidator{
			logoutIdentity: id,
			logoutURL:      "https://idp.example.com/slo?LogoutResponse=...",
		},
	}

	r := httptest.NewRequest(http.MethodPost, "/saml/sls", strings.NewReader("SAMLRequest=ignored"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.SAMLSLSHandler().ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Location"), "idp.example.com/slo") {
		t.Errorf("loc=%s", w.Header().Get("Location"))
	}
	if _, err := store.GetSessionByTokenHash(context.Background(), HashToken("tok-1")); !errors.Is(err, ErrNotFound) {
		t.Errorf("session should be deleted, got err=%v", err)
	}
}

func TestSAMLSLSHandler_RejectsNonGetPost(t *testing.T) {
	srv := &Server{Upstream: &stubSAMLValidator{}}
	for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		r := httptest.NewRequest(m, "/saml/sls", nil)
		w := httptest.NewRecorder()
		srv.SAMLSLSHandler().ServeHTTP(w, r)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: status=%d, want 405", m, w.Code)
		}
		if !strings.Contains(w.Header().Get("Allow"), "GET") || !strings.Contains(w.Header().Get("Allow"), "POST") {
			t.Errorf("method %s: Allow=%q", m, w.Header().Get("Allow"))
		}
	}
}
