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
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
	}

	plainAT, hashAT := NewToken()
	credCT, _ := enc.Seal([]byte("sa-token"))
	_ = store.PutSession(context.Background(), Session{
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

func TestMiddleware_ExpiredBearer_401(t *testing.T) {
	enc := mustEnc(t, mustKey(t), nil)
	store := NewMemoryStore()
	srv := &Server{
		PublicURL: "https://mcp.example.com",
		Store:     store,
		Encryptor: enc,
	}
	plainAT, hashAT := NewToken()
	credCT, _ := enc.Seal([]byte("x"))
	_ = store.PutSession(context.Background(), Session{
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
