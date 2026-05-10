package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

func TestMiddleware_NoBearer_401(t *testing.T) {
	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Store:     NewMemoryStore(),
		Encryptor: mustEnc(t, mustKey(t), nil),
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	w := httptest.NewRecorder()
	srv.Middleware()(next).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d", w.Code)
	}
	if !strings.Contains(w.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("WWW-Authenticate=%q", w.Header().Get("WWW-Authenticate"))
	}
	if called {
		t.Errorf("next handler should NOT run on 401")
	}
}

func TestMiddleware_GoodBearer_PopulatesContext(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()
	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
	}

	plainAT, hashAT := NewToken()
	credCT, _ := enc.Seal([]byte("sa-token"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:       hashAT,
		ExpiresAt:       time.Now().Add(time.Hour),
		Identity:        Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		UpstreamCredsCT: credCT,
	})

	var observed mcpgrafana.GrafanaConfig
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = mcpgrafana.GrafanaConfigFromContext(r.Context())
	})
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+plainAT)
	w := httptest.NewRecorder()
	srv.Middleware()(next).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if observed.APIKey != "sa-token" {
		t.Errorf("APIKey=%q want sa-token", observed.APIKey)
	}
}

// TestMiddleware_PinsGrafanaURL verifies the middleware overrides any
// pre-set context URL with the operator-configured Server.GrafanaURL so a
// downstream X-Grafana-URL handler cannot redirect the decrypted session
// API key at an attacker-controlled host.
func TestMiddleware_PinsGrafanaURL(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()
	srv := &Server{
		Metrics:    NewMetrics(),
		PublicURL:  "https://mcp.example.com",
		GrafanaURL: "https://grafana.internal:3000",
		Store:      store,
		Encryptor:  enc,
	}
	plainAT, hashAT := NewToken()
	credCT, _ := enc.Seal([]byte("sa-token"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:       hashAT,
		ExpiresAt:       time.Now().Add(time.Hour),
		Identity:        Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		UpstreamCredsCT: credCT,
	})

	var observed mcpgrafana.GrafanaConfig
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = mcpgrafana.GrafanaConfigFromContext(r.Context())
	})
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+plainAT)
	w := httptest.NewRecorder()
	srv.Middleware()(next).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if observed.URL != "https://grafana.internal:3000" {
		t.Errorf("URL=%q want %q (Server.GrafanaURL must pin)", observed.URL, "https://grafana.internal:3000")
	}
}

func TestMiddleware_ExpiredBearer_401(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()
	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
	}
	plainAT, hashAT := NewToken()
	credCT, _ := enc.Seal([]byte("x"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:       hashAT,
		ExpiresAt:       time.Now().Add(-time.Second),
		UpstreamCredsCT: credCT,
	})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+plainAT)
	w := httptest.NewRecorder()
	srv.Middleware()(next).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d", w.Code)
	}
	if !strings.Contains(w.Header().Get("WWW-Authenticate"), "invalid_token") {
		t.Errorf("WWW-Authenticate=%q", w.Header().Get("WWW-Authenticate"))
	}
}

// TestMiddleware_ExpiredBearer_PreservesRefresh covers the standard
// OAuth "access expired → use refresh token" flow: when middleware sees
// an expired access token it must NOT delete the session, otherwise the
// sessByRefresh mapping would also drop and handleRefreshGrant would
// return invalid_grant for a refresh token that's still well within its
// 30-day TTL.
func TestMiddleware_ExpiredBearer_PreservesRefresh(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()
	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
	}
	plainAT, hashAT := NewToken()
	plainRT, hashRT := NewToken()
	credCT, _ := enc.Seal([]byte("x"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:        hashAT,
		RefreshHash:      hashRT,
		ExpiresAt:        time.Now().Add(-time.Second),
		RefreshExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		UpstreamCredsCT:  credCT,
		Identity:         Identity{Mode: ModeOAuthOIDC, ID: "alice"},
	})

	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+plainAT)
	srv.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(httptest.NewRecorder(), r)

	// Session must still be resolvable by refresh hash; if Middleware had
	// called DeleteSession we'd get ErrNotFound here and the client could
	// never refresh.
	if _, err := store.GetSessionByRefreshHash(context.Background(), HashToken(plainRT)); err != nil {
		t.Errorf("session was deleted on access-token expiry — refresh path is broken: %v", err)
	}
}

func TestMiddleware_RefreshesNearExpiry(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()

	// Pre-existing session with creds expiring in 30 seconds (< 60s window).
	plainAT, hashAT := NewToken()
	credCT, _ := enc.Seal([]byte("expiring-bearer"))
	refreshCT, _ := enc.Seal([]byte("refresh-1"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:         hashAT,
		ExpiresAt:         time.Now().Add(time.Hour),
		Identity:          Identity{Mode: ModeOAuthGrafana, ID: "alice"},
		UpstreamCredsCT:   credCT,
		UpstreamRefreshCT: refreshCT,
		UpstreamExpiresAt: time.Now().Add(30 * time.Second),
	})

	srv := &Server{
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
		Upstream: &refreshableStub{
			newCreds:   []byte("refreshed-bearer"),
			newRefresh: []byte("refresh-2"),
			newExpiry:  time.Now().Add(10 * time.Minute),
		},
	}

	var observedKey string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := mcpgrafana.GrafanaConfigFromContext(r.Context())
		observedKey = cfg.APIKey
	})
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+plainAT)
	w := httptest.NewRecorder()
	srv.Middleware()(next).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if observedKey != "refreshed-bearer" {
		t.Errorf("APIKey on context = %q, want refreshed-bearer", observedKey)
	}

	// Persisted session must reflect the new credentials.
	got, err := store.GetSessionByTokenHash(context.Background(), hashAT)
	if err != nil {
		t.Fatal(err)
	}
	pt, _ := enc.Open(got.UpstreamCredsCT)
	if string(pt) != "refreshed-bearer" {
		t.Errorf("session creds not persisted: got %q", pt)
	}
	pt, _ = enc.Open(got.UpstreamRefreshCT)
	if string(pt) != "refresh-2" {
		t.Errorf("session refresh not persisted: got %q", pt)
	}
	if got.UpstreamExpiresAt.Before(time.Now().Add(time.Minute)) {
		t.Errorf("session expiry not advanced: %v", got.UpstreamExpiresAt)
	}
}

// Per OAuth 2 §6, an upstream may omit refresh_token from a refresh response;
// the previous refresh token then remains valid. doRefreshUpstream must
// preserve the existing UpstreamRefreshCT in that case rather than overwrite
// it with an encrypted empty value (which would break the next refresh).
func TestMiddleware_Refresh_PreservesOldRefreshTokenWhenUpstreamOmitsIt(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()

	plainAT, hashAT := NewToken()
	credCT, _ := enc.Seal([]byte("expiring-bearer"))
	originalRefreshCT, _ := enc.Seal([]byte("original-refresh"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:         hashAT,
		ExpiresAt:         time.Now().Add(time.Hour),
		Identity:          Identity{Mode: ModeOAuthGrafana, ID: "alice"},
		UpstreamCredsCT:   credCT,
		UpstreamRefreshCT: originalRefreshCT,
		UpstreamExpiresAt: time.Now().Add(20 * time.Second),
	})

	srv := &Server{
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
		Upstream: &refreshableStub{
			newCreds:   []byte("refreshed-bearer"),
			newRefresh: nil, // upstream omitted refresh_token
			newExpiry:  time.Now().Add(10 * time.Minute),
		},
	}

	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+plainAT)
	w := httptest.NewRecorder()
	srv.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	got, err := store.GetSessionByTokenHash(context.Background(), hashAT)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := enc.Open(got.UpstreamRefreshCT)
	if err != nil {
		t.Fatalf("decrypt persisted refresh: %v", err)
	}
	if string(pt) != "original-refresh" {
		t.Errorf("refresh token clobbered: got %q want original-refresh", pt)
	}
}

func TestMiddleware_RefreshFailureReturns401(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()

	plainAT, hashAT := NewToken()
	credCT, _ := enc.Seal([]byte("expiring-bearer"))
	refreshCT, _ := enc.Seal([]byte("refresh-1"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:         hashAT,
		ExpiresAt:         time.Now().Add(time.Hour),
		Identity:          Identity{Mode: ModeOAuthGrafana, ID: "alice"},
		UpstreamCredsCT:   credCT,
		UpstreamRefreshCT: refreshCT,
		UpstreamExpiresAt: time.Now().Add(10 * time.Second),
	})

	srv := &Server{
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
		Upstream:  &refreshableStub{refreshErr: errors.New("upstream rejected refresh token")},
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+plainAT)
	w := httptest.NewRecorder()
	srv.Middleware()(next).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", w.Code)
	}
	if !strings.Contains(w.Header().Get("WWW-Authenticate"), "invalid_token") {
		t.Errorf("WWW-Authenticate=%q", w.Header().Get("WWW-Authenticate"))
	}
	if called {
		t.Errorf("next handler should not run on refresh failure")
	}

	// Session should be deleted on failed refresh — client must re-auth.
	if _, err := store.GetSessionByTokenHash(context.Background(), hashAT); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected session deleted after failed refresh, got err=%v", err)
	}
}

// refreshableStub is a test Upstream that returns canned refresh results.
type refreshableStub struct {
	newCreds   []byte
	newRefresh []byte
	newExpiry  time.Time
	refreshErr error
}

func (r *refreshableStub) Mode() Mode                      { return ModeOAuthGrafana }
func (r *refreshableStub) AuthorizeURL(_, _ string) string { return "" }
func (r *refreshableStub) HandleCallback(_ context.Context, _ url.Values) (CallbackResult, error) {
	return CallbackResult{}, nil
}
func (r *refreshableStub) Refresh(_ context.Context, _ []byte) (CallbackResult, error) {
	if r.refreshErr != nil {
		return CallbackResult{}, r.refreshErr
	}
	return CallbackResult{
		HasCred:           true,
		UpstreamCreds:     r.newCreds,
		UpstreamRefresh:   r.newRefresh,
		UpstreamExpiresAt: r.newExpiry,
	}, nil
}

func TestMiddleware_ConcurrentRefreshes_CoalesceToOneUpstreamCall(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()

	plainAT, hashAT := NewToken()
	credCT, _ := enc.Seal([]byte("expiring-bearer"))
	refreshCT, _ := enc.Seal([]byte("refresh-1"))
	_, _ = store.PutSession(context.Background(), Session{
		TokenHash:         hashAT,
		ExpiresAt:         time.Now().Add(time.Hour),
		Identity:          Identity{Mode: ModeOAuthGrafana, ID: "alice"},
		UpstreamCredsCT:   credCT,
		UpstreamRefreshCT: refreshCT,
		UpstreamExpiresAt: time.Now().Add(20 * time.Second),
	})

	var calls int32
	stub := &countingRefreshableStub{
		newCreds:   []byte("refreshed-bearer"),
		newRefresh: []byte("refresh-2"),
		newExpiry:  time.Now().Add(10 * time.Minute),
		delay:      50 * time.Millisecond, // gives concurrent callers time to coalesce
		calls:      &calls,
	}

	srv := &Server{
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
		Upstream:  stub,
	}

	var wg sync.WaitGroup
	handler := srv.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			r.Header.Set("Authorization", "Bearer "+plainAT)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected exactly 1 upstream refresh, got %d", got)
	}
}

// countingRefreshableStub is a refreshable Upstream that counts Refresh calls
// and optionally sleeps before responding.
type countingRefreshableStub struct {
	newCreds, newRefresh []byte
	newExpiry            time.Time
	delay                time.Duration
	calls                *int32
}

func (c *countingRefreshableStub) Mode() Mode                      { return ModeOAuthGrafana }
func (c *countingRefreshableStub) AuthorizeURL(_, _ string) string { return "" }
func (c *countingRefreshableStub) HandleCallback(_ context.Context, _ url.Values) (CallbackResult, error) {
	return CallbackResult{}, nil
}
func (c *countingRefreshableStub) Refresh(_ context.Context, _ []byte) (CallbackResult, error) {
	atomic.AddInt32(c.calls, 1)
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	return CallbackResult{
		HasCred:           true,
		UpstreamCreds:     c.newCreds,
		UpstreamRefresh:   c.newRefresh,
		UpstreamExpiresAt: c.newExpiry,
	}, nil
}
