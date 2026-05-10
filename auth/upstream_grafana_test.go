package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockGrafanaOAuth2 stands in for Grafana's oauth2_server. It implements the
// minimum surface we exercise: /oauth2/authorize, /oauth2/token (both
// authorization_code and refresh_token grants).
type mockGrafanaOAuth2 struct {
	*httptest.Server

	mu             sync.Mutex
	authorizedCode string            // most recently issued auth code
	authorizedSub  string            // subject bound to that code
	refreshTokens  map[string]string // refresh_token -> subject

	clientID     string
	clientSecret string
}

func newMockGrafanaOAuth2(t *testing.T) *mockGrafanaOAuth2 {
	t.Helper()
	m := &mockGrafanaOAuth2{
		clientID:      "mcp",
		clientSecret:  "shh",
		refreshTokens: map[string]string{},
	}
	mux := http.NewServeMux()
	m.Server = httptest.NewServer(mux)

	mux.HandleFunc("/oauth2/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirect := q.Get("redirect_uri")
		state := q.Get("state")
		u, _ := url.Parse(redirect)
		m.mu.Lock()
		m.authorizedCode = "grafana-code-" + state
		m.authorizedSub = "user-42"
		m.mu.Unlock()
		qq := u.Query()
		qq.Set("code", m.authorizedCode)
		qq.Set("state", state)
		u.RawQuery = qq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})

	writeJSON := func(w http.ResponseWriter, v any) {
		body, err := json.Marshal(v)
		if err != nil {
			http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}

	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grant := r.Form.Get("grant_type")
		switch grant {
		case "authorization_code":
			code := r.Form.Get("code")
			m.mu.Lock()
			expected := m.authorizedCode
			sub := m.authorizedSub
			m.mu.Unlock()
			if code != expected {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			refresh := "refresh-" + sub + "-" + code
			m.mu.Lock()
			m.refreshTokens[refresh] = sub
			m.mu.Unlock()
			writeJSON(w, map[string]any{
				"access_token":  "access-" + sub + "-1",
				"refresh_token": refresh,
				"expires_in":    600,
				"token_type":    "Bearer",
			})
		case "refresh_token":
			rt := r.Form.Get("refresh_token")
			m.mu.Lock()
			sub, ok := m.refreshTokens[rt]
			if ok {
				delete(m.refreshTokens, rt) // rotate
			}
			m.mu.Unlock()
			if !ok {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			newRT := rt + "-rotated"
			m.mu.Lock()
			m.refreshTokens[newRT] = sub
			m.mu.Unlock()
			writeJSON(w, map[string]any{
				"access_token":  "access-" + sub + "-2",
				"refresh_token": newRT,
				"expires_in":    600,
				"token_type":    "Bearer",
			})
		default:
			http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
		}
	})

	return m
}

func TestGrafanaUpstream_HandleCallback(t *testing.T) {
	mock := newMockGrafanaOAuth2(t)
	defer mock.Close()

	cfg := Config{
		Mode:                      ModeOAuthGrafana,
		PublicURL:                 "https://mcp.example.com",
		GrafanaOAuth2IssuerURL:    mock.URL,
		GrafanaOAuth2ClientID:     mock.clientID,
		GrafanaOAuth2ClientSecret: mock.clientSecret,
	}
	up, err := NewGrafanaUpstream(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	authURL := up.AuthorizeURL("https://mcp.example.com/callback", "state-1")
	if !strings.Contains(authURL, "code_challenge=") || !strings.Contains(authURL, "code_challenge_method=S256") {
		t.Errorf("authorize URL missing PKCE: %s", authURL)
	}

	// Drive the upstream's /authorize with a no-redirect client to capture the
	// Location header (the mock 302s back with a code).
	noRedir := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noRedir.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %v", loc)
	}

	result, err := up.HandleCallback(context.Background(), url.Values{
		"code":  {code},
		"state": {"state-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasCred {
		t.Errorf("Mode A must return HasCred=true")
	}
	if string(result.UpstreamCreds) == "" {
		t.Errorf("UpstreamCreds is empty")
	}
	if string(result.UpstreamRefresh) == "" {
		t.Errorf("UpstreamRefresh is empty")
	}
	if result.UpstreamExpiresAt.Before(time.Now()) {
		t.Errorf("UpstreamExpiresAt is in the past: %v", result.UpstreamExpiresAt)
	}
	if result.Identity.Mode != ModeOAuthGrafana {
		t.Errorf("identity mode = %q, want %q", result.Identity.Mode, ModeOAuthGrafana)
	}
}

func TestGrafanaUpstream_Refresh(t *testing.T) {
	mock := newMockGrafanaOAuth2(t)
	defer mock.Close()

	cfg := Config{
		Mode:                      ModeOAuthGrafana,
		PublicURL:                 "https://mcp.example.com",
		GrafanaOAuth2IssuerURL:    mock.URL,
		GrafanaOAuth2ClientID:     mock.clientID,
		GrafanaOAuth2ClientSecret: mock.clientSecret,
	}
	up, err := NewGrafanaUpstream(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Seed: do a code exchange to obtain a refresh token.
	authURL := up.AuthorizeURL("https://mcp.example.com/callback", "state-2")
	noRedir := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, _ := noRedir.Get(authURL)
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	first, err := up.HandleCallback(context.Background(), url.Values{
		"code":  {loc.Query().Get("code")},
		"state": {"state-2"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Now refresh.
	refreshed, err := up.Refresh(context.Background(), first.UpstreamRefresh)
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed.HasCred {
		t.Errorf("refresh must return HasCred=true")
	}
	if string(refreshed.UpstreamCreds) == string(first.UpstreamCreds) {
		t.Errorf("refresh should rotate the access token")
	}
	if string(refreshed.UpstreamRefresh) == string(first.UpstreamRefresh) {
		t.Errorf("refresh should rotate the refresh token")
	}

	// Old refresh token must now be rejected.
	if _, err := up.Refresh(context.Background(), first.UpstreamRefresh); err == nil {
		t.Errorf("old refresh token should be invalidated")
	}
}

func TestGrafanaUpstream_OAuthScopesIncludeOpenID(t *testing.T) {
	cfg := Config{
		Mode:                      ModeOAuthGrafana,
		PublicURL:                 "https://mcp.example.com",
		GrafanaOAuth2IssuerURL:    "https://grafana.example.com",
		GrafanaOAuth2ClientID:     "mcp",
		GrafanaOAuth2ClientSecret: "shh",
	}
	up, err := NewGrafanaUpstream(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	authURL := up.AuthorizeURL("https://mcp.example.com/callback", "state-x")
	u, _ := url.Parse(authURL)
	scope := u.Query().Get("scope")
	if !strings.Contains(scope, "openid") {
		t.Errorf("authorize URL must request openid scope (got %q)", scope)
	}
}

func TestGrafanaUpstream_SweepDropsExpiredPendings(t *testing.T) {
	up := &GrafanaUpstream{pendings: map[string]*grafanaPending{}}

	now := time.Now()
	// Old entry: created two TTLs ago — should be reaped.
	up.pendings["old"] = &grafanaPending{verifier: "v1", createdAt: now.Add(-2 * grafanaPendingTTL)}
	// Fresh entry: created "now" — should survive a sweep that runs "now".
	up.pendings["fresh"] = &grafanaPending{verifier: "v2", createdAt: now}

	up.mu.Lock()
	up.sweepPendingsLocked(now)
	up.mu.Unlock()

	if _, ok := up.pendings["old"]; ok {
		t.Errorf("expired pending was not swept")
	}
	if _, ok := up.pendings["fresh"]; !ok {
		t.Errorf("fresh pending was incorrectly swept")
	}
}
