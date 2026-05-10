package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
