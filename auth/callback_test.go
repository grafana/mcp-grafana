package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// stubUpstreamWithIdentity returns the same identity for every callback.
type stubUpstreamWithIdentity struct {
	stubUpstream
	id   Identity
	cred []byte
	has  bool
}

func (s *stubUpstreamWithIdentity) HandleCallback(_ context.Context, _ url.Values) (Identity, []byte, bool, error) {
	return s.id, s.cred, s.has, nil
}

func newCallbackServer(t *testing.T, upHas bool) (*Server, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	enc := mustEnc(t, mustKey(t), nil)
	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
		Upstream: &stubUpstreamWithIdentity{
			stubUpstream: stubUpstream{mode: ModeOAuthOIDC},
			id:           Identity{Mode: ModeOAuthOIDC, ID: "alice"},
			has:          upHas,
		},
		AuthCodeTTL: 5 * time.Minute,
	}
	return srv, store
}

func TestCallback_FirstLogin_RedirectsToBootstrap(t *testing.T) {
	srv, _ := newCallbackServer(t, false)
	state := stateToken()
	srv.authzPendings().Store(state, &pendingFlow{
		clientID:            "cid",
		redirectURI:         "http://localhost:1/cb",
		codeChallenge:       "x",
		codeChallengeMethod: "S256",
		clientState:         "client-state",
	})

	r := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+state, nil)
	w := httptest.NewRecorder()
	srv.CallbackHandler().ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc == nil || !strings.HasPrefix(loc.Path, "/bootstrap") {
		t.Errorf("loc=%v", loc)
	}
	if loc.Query().Get("flow") == "" {
		t.Errorf("bootstrap flow token not on URL: %v", loc)
	}
}

func TestCallback_RepeatLogin_ShortcutsToClient(t *testing.T) {
	srv, store := newCallbackServer(t, false)
	id := Identity{Mode: ModeOAuthOIDC, ID: "alice"}
	// Pre-existing session with an SA token on file.
	enc := srv.Encryptor
	ct, _ := enc.Seal([]byte("sa-token"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:       HashToken("old-tok"),
		Identity:        id,
		UpstreamCredsCT: ct,
		ExpiresAt:       time.Now().Add(time.Hour),
		CreatedAt:       time.Now(),
	})

	state := stateToken()
	srv.authzPendings().Store(state, &pendingFlow{
		clientID:            "cid",
		redirectURI:         "http://localhost:1/cb",
		codeChallenge:       "x",
		codeChallengeMethod: "S256",
		clientState:         "client-state",
	})

	r := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+state, nil)
	w := httptest.NewRecorder()
	srv.CallbackHandler().ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Host != "localhost:1" || loc.Path != "/cb" {
		t.Errorf("expected redirect back to client, got %v", loc)
	}
	if loc.Query().Get("code") == "" {
		t.Errorf("expected code parameter")
	}
	if loc.Query().Get("state") != "client-state" {
		t.Errorf("client state not preserved")
	}
}

func TestCallback_UnknownState(t *testing.T) {
	srv, _ := newCallbackServer(t, false)
	r := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state=missing", nil)
	w := httptest.NewRecorder()
	srv.CallbackHandler().ServeHTTP(w, r)
	if w.Code == http.StatusFound {
		t.Errorf("expected error, got redirect")
	}
}

// failingUpstream returns a chatty error from HandleCallback so we can
// confirm the callback handler does NOT propagate err.Error() to the
// user-agent redirect.
type failingUpstream struct{ stubUpstream }

func (f *failingUpstream) HandleCallback(_ context.Context, _ url.Values) (Identity, []byte, bool, error) {
	return Identity{}, nil, false, fmt.Errorf("token exchange against https://internal-idp.corp.example/oauth/token: invalid_grant - corrupted refresh slot 0xdeadbeef")
}

func TestCallback_UpstreamError_RedirectsWithGenericDescription(t *testing.T) {
	srv, _ := newCallbackServer(t, false)
	srv.Upstream = &failingUpstream{stubUpstream: stubUpstream{mode: ModeOAuthOIDC}}

	state := stateToken()
	srv.authzPendings().Store(state, &pendingFlow{
		clientID:            "cid",
		redirectURI:         "http://localhost:1/cb",
		codeChallenge:       "x",
		codeChallengeMethod: "S256",
		clientState:         "client-state",
	})

	r := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+state, nil)
	w := httptest.NewRecorder()
	srv.CallbackHandler().ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	loc := w.Header().Get("Location")
	if strings.Contains(loc, "internal-idp.corp.example") || strings.Contains(loc, "deadbeef") {
		t.Errorf("redirect leaked upstream error details: %s", loc)
	}
	if !strings.Contains(loc, "error=access_denied") {
		t.Errorf("redirect missing error=access_denied: %s", loc)
	}
}

// failingAuthCodeStore wraps MemoryStore and forces PutAuthCode to fail
// with a chatty error so the test can confirm completeAuthCode does NOT
// echo that error string back to the client redirect.
type failingAuthCodeStore struct{ *MemoryStore }

func (f *failingAuthCodeStore) PutAuthCode(_ context.Context, _ AuthCode) error {
	return fmt.Errorf("file:///var/lib/mcp/state.json: write failed: disk quota exceeded for tenant t-0xdeadbeef")
}

func TestCompleteAuthCode_PutFails_RedirectsWithGenericDescription(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	srv := &Server{
		Metrics:     NewMetrics(),
		PublicURL:   "https://mcp.example.com",
		Store:       &failingAuthCodeStore{MemoryStore: NewMemoryStore()},
		Encryptor:   enc,
		AuthCodeTTL: 5 * time.Minute,
	}
	pf := &pendingFlow{
		clientID:            "cid",
		redirectURI:         "http://localhost:1/cb",
		codeChallenge:       "x",
		codeChallengeMethod: "S256",
		clientState:         "client-state",
	}
	r := httptest.NewRequest(http.MethodGet, "/callback", nil)
	w := httptest.NewRecorder()
	srv.completeAuthCode(w, r, pf, Identity{Mode: ModeOAuthOIDC, ID: "alice"}, []byte("ct"))

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	loc := w.Header().Get("Location")
	for _, leak := range []string{"file:///var/lib", "disk quota", "deadbeef", "tenant t-"} {
		if strings.Contains(loc, leak) {
			t.Errorf("redirect leaked store error fragment %q: %s", leak, loc)
		}
	}
	if !strings.Contains(loc, "error=server_error") {
		t.Errorf("redirect missing error=server_error: %s", loc)
	}
}
