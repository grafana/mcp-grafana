package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// stubUpstream is a deterministic Upstream for handler tests. It captures
// the redirectURI argument passed to AuthorizeURL so tests can assert that
// AuthorizeHandler forwards the configured callback URL to the upstream.
type stubUpstream struct {
	mode            Mode
	authURL         string
	lastRedirectURI string
}

func (s *stubUpstream) Mode() Mode { return s.mode }
func (s *stubUpstream) AuthorizeURL(redirectURI, state string) string {
	s.lastRedirectURI = redirectURI
	return s.authURL + "?state=" + state
}
func (s *stubUpstream) HandleCallback(_ context.Context, _ url.Values) (Identity, []byte, bool, error) {
	return Identity{Mode: s.mode, ID: "u1"}, nil, false, nil
}

func newAuthorizeServer(t *testing.T) (*Server, *MemoryStore, *stubUpstream) {
	t.Helper()
	store := NewMemoryStore()
	up := &stubUpstream{mode: ModeOAuthOIDC, authURL: "https://idp.example.com/auth"}
	srv := &Server{
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Upstream:  up,
	}
	return srv, store, up
}

func TestAuthorize_RedirectsToUpstream(t *testing.T) {
	srv, store, up := newAuthorizeServer(t)
	_ = store.PutClient(context.Background(), DCRClient{
		ClientID:     "cid",
		RedirectURIs: []string{"http://localhost:1234/cb"},
	})

	q := url.Values{}
	q.Set("client_id", "cid")
	q.Set("redirect_uri", "http://localhost:1234/cb")
	q.Set("response_type", "code")
	q.Set("code_challenge", "challenge")
	q.Set("code_challenge_method", "S256")
	q.Set("state", "client-state")

	r := httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	srv.AuthorizeHandler().ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, up.authURL) {
		t.Errorf("location=%s", loc)
	}
	// AuthorizeHandler must forward the configured callback URL
	// (PublicURL + "/callback") to the upstream so OIDCUpstream can override
	// the oauth2.Config.RedirectURL for this specific flow.
	if up.lastRedirectURI != srv.PublicURL+"/callback" {
		t.Errorf("upstream.AuthorizeURL got redirectURI=%q, want %q", up.lastRedirectURI, srv.PublicURL+"/callback")
	}
}

func TestAuthorize_RejectsUnknownClient(t *testing.T) {
	srv, _, _ := newAuthorizeServer(t)
	q := url.Values{}
	q.Set("client_id", "missing")
	q.Set("redirect_uri", "http://localhost/cb")
	q.Set("response_type", "code")
	q.Set("code_challenge", "x")
	q.Set("code_challenge_method", "S256")
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	srv.AuthorizeHandler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d", w.Code)
	}
}

func TestAuthorize_RejectsRedirectMismatch(t *testing.T) {
	srv, store, _ := newAuthorizeServer(t)
	_ = store.PutClient(context.Background(), DCRClient{
		ClientID: "cid", RedirectURIs: []string{"http://localhost:1/cb"},
	})
	q := url.Values{}
	q.Set("client_id", "cid")
	q.Set("redirect_uri", "http://evil.example/cb")
	q.Set("response_type", "code")
	q.Set("code_challenge", "x")
	q.Set("code_challenge_method", "S256")
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	srv.AuthorizeHandler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d", w.Code)
	}
}

func TestAuthorize_RequiresS256(t *testing.T) {
	srv, store, _ := newAuthorizeServer(t)
	_ = store.PutClient(context.Background(), DCRClient{
		ClientID: "cid", RedirectURIs: []string{"http://localhost:1/cb"},
	})
	q := url.Values{}
	q.Set("client_id", "cid")
	q.Set("redirect_uri", "http://localhost:1/cb")
	q.Set("response_type", "code")
	q.Set("code_challenge", "x")
	q.Set("code_challenge_method", "plain")
	q.Set("state", "client-state")
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	srv.AuthorizeHandler().ServeHTTP(w, r)

	// PKCE failure with a verified redirect_uri must redirect with error=
	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Host != "localhost:1" || loc.Path != "/cb" {
		t.Errorf("loc=%v", loc)
	}
	if loc.Query().Get("error") != "invalid_request" {
		t.Errorf("error=%q", loc.Query().Get("error"))
	}
	if loc.Query().Get("state") != "client-state" {
		t.Errorf("state=%q", loc.Query().Get("state"))
	}
}

// TestSweepPendings_DropsExpiredEntries verifies that pending /authorize
// entries that exceed pendingFlowTTL are evicted by the opportunistic
// sweep, so a flood of unfinished /authorize calls can't grow the map
// without bound.
func TestSweepPendings_DropsExpiredEntries(t *testing.T) {
	now := time.Now()

	pendingMu.Lock()
	for k := range pendings {
		delete(pendings, k)
	}
	pendings["fresh"] = &pendingFlow{createdAt: now}
	pendings["expired"] = &pendingFlow{createdAt: now.Add(-2 * pendingFlowTTL)}
	// Force the sweep gate to fire on the next call.
	pendingLastSwept = now.Add(-2 * pendingFlowTTL)
	sweepPendingsLocked(now)
	_, hasFresh := pendings["fresh"]
	_, hasExpired := pendings["expired"]
	pendingMu.Unlock()

	if hasExpired {
		t.Errorf("expected expired entry to be swept")
	}
	if !hasFresh {
		t.Errorf("fresh entry should survive the sweep")
	}
}

// TestConsumePending_TreatsExpiredAsMissing verifies that an entry which
// has somehow lingered past pendingFlowTTL is rejected by consumePending,
// forcing the user to restart the flow rather than completing with stale
// state.
func TestConsumePending_TreatsExpiredAsMissing(t *testing.T) {
	pendingMu.Lock()
	for k := range pendings {
		delete(pendings, k)
	}
	pendingLastSwept = time.Now()
	pendings["stale"] = &pendingFlow{createdAt: time.Now().Add(-(pendingFlowTTL + time.Second))}
	pendingMu.Unlock()

	if _, ok := consumePending("stale"); ok {
		t.Errorf("expected expired pending to be treated as missing")
	}
}
