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

// fakeGrafana stands in for the real Grafana /api/user endpoint.
func fakeGrafana(t *testing.T, expectedToken string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+expectedToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"login":"alice"}`))
	}))
}

func TestBootstrap_GET_RendersForm(t *testing.T) {
	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Store:     NewMemoryStore(),
		Encryptor: mustEnc(t, mustKey(t), nil),
	}
	srv.bootstrapPendings().Store("flow-1", &pendingBootstrap{
		identity:    Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		clientID:    "cid",
		redirectURI: "http://localhost:1/cb",
		createdAt:   time.Now(),
	})

	r := httptest.NewRequest(http.MethodGet, "/bootstrap?flow=flow-1", nil)
	w := httptest.NewRecorder()
	srv.BootstrapHandler("https://grafana.example.com").ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `name="grafana_token"`) {
		t.Errorf("missing token field in form: %s", body)
	}
	if !strings.Contains(body, "flow-1") {
		t.Errorf("flow token must round-trip in the form")
	}
}

func TestBootstrap_POST_ValidatesAndStoresToken(t *testing.T) {
	gf := fakeGrafana(t, "good-token")
	defer gf.Close()
	srv := &Server{
		Metrics:     NewMetrics(),
		PublicURL:   "https://mcp.example.com",
		Store:       NewMemoryStore(),
		Encryptor:   mustEnc(t, mustKey(t), nil),
		AuthCodeTTL: 5 * time.Minute,
	}
	srv.bootstrapPendings().Store("flow-2", &pendingBootstrap{
		identity:            Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		clientID:            "cid",
		redirectURI:         "http://localhost:1/cb",
		clientState:         "client-state",
		codeChallenge:       "x",
		codeChallengeMethod: "S256",
		createdAt:           time.Now(),
	})

	form := url.Values{}
	form.Set("flow", "flow-2")
	form.Set("grafana_token", "good-token")
	r := httptest.NewRequest(http.MethodPost, "/bootstrap", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	srv.BootstrapHandler(gf.URL).ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Host != "localhost:1" || loc.Query().Get("code") == "" {
		t.Errorf("loc=%v", loc)
	}

	// Bootstrap writes the encrypted SA token onto the auth code (not a session).
	// Find the auth code via the redirect's `code` query param, then consume + verify.
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("expected code in redirect URL")
	}
	ac, err := srv.Store.ConsumeAuthCode(context.Background(), HashToken(code))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := srv.Encryptor.Open(ac.UpstreamCredsCT)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "good-token" {
		t.Errorf("decrypted creds=%q", pt)
	}
}

// A flow token must be consumed exactly once: a second concurrent POST
// against the same token must be rejected, otherwise both requests would
// mint auth codes for the same identity, and the second-to-finish would
// silently invalidate the first (one-session-per-identity invariant).
func TestBootstrap_POST_RejectsDoubleConsume(t *testing.T) {
	gf := fakeGrafana(t, "good-token")
	defer gf.Close()
	srv := &Server{
		Metrics:     NewMetrics(),
		PublicURL:   "https://mcp.example.com",
		Store:       NewMemoryStore(),
		Encryptor:   mustEnc(t, mustKey(t), nil),
		AuthCodeTTL: 5 * time.Minute,
	}
	srv.bootstrapPendings().Store("flow-double", &pendingBootstrap{
		identity:            Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		clientID:            "cid",
		redirectURI:         "http://localhost:1/cb",
		clientState:         "client-state",
		codeChallenge:       "x",
		codeChallengeMethod: "S256",
		createdAt:           time.Now(),
	})

	doPost := func() *httptest.ResponseRecorder {
		form := url.Values{}
		form.Set("flow", "flow-double")
		form.Set("grafana_token", "good-token")
		r := httptest.NewRequest(http.MethodPost, "/bootstrap", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		srv.BootstrapHandler(gf.URL).ServeHTTP(w, r)
		return w
	}

	first := doPost()
	if first.Code != http.StatusFound {
		t.Fatalf("first POST status=%d body=%s", first.Code, first.Body)
	}
	second := doPost()
	if second.Code != http.StatusBadRequest {
		t.Errorf("second POST status=%d (want 400 already consumed) body=%s", second.Code, second.Body)
	}
}

func TestBootstrap_POST_BadToken(t *testing.T) {
	gf := fakeGrafana(t, "good-token")
	defer gf.Close()
	srv := &Server{
		Metrics:   NewMetrics(),
		PublicURL: "https://mcp.example.com",
		Store:     NewMemoryStore(),
		Encryptor: mustEnc(t, mustKey(t), nil),
	}
	srv.bootstrapPendings().Store("flow-3", &pendingBootstrap{
		identity:    Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		clientID:    "cid",
		redirectURI: "http://localhost:1/cb",
		createdAt:   time.Now(),
	})

	form := url.Values{}
	form.Set("flow", "flow-3")
	form.Set("grafana_token", "wrong-token")
	r := httptest.NewRequest(http.MethodPost, "/bootstrap", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	srv.BootstrapHandler(gf.URL).ServeHTTP(w, r)

	// Expect re-render of the form with an error message; flow token must
	// remain valid for retry, so the same flow should still work afterwards.
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusOK {
		t.Errorf("status=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "token") {
		t.Errorf("body should mention token rejection: %s", w.Body)
	}
	if _, ok := srv.bootstrapPendings().Consume("flow-3"); !ok {
		t.Errorf("flow token should remain valid after a failed paste")
	}
}
