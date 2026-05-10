package mcpgrafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractGrafanaInfoFromHeaders_PreservesSessionAPIKey(t *testing.T) {
	// Simulate the auth middleware having set APIKey=session-token before
	// the SSE/HTTP context func runs.
	ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
		URL:    "https://grafana.example.com",
		APIKey: "session-token",
	})
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("X-Grafana-Service-Account-Token", "header-token")
	r.Header.Set("X-Grafana-URL", "https://grafana.example.com")

	out := ExtractGrafanaInfoFromHeaders(ctx, r)
	cfg := GrafanaConfigFromContext(out)
	if cfg.APIKey != "session-token" {
		t.Errorf("session-derived APIKey was overwritten: got %q want session-token", cfg.APIKey)
	}
}

func TestExtractGrafanaInfoFromHeaders_HeadersWinWhenNoSession(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("X-Grafana-Service-Account-Token", "header-token")
	r.Header.Set("X-Grafana-URL", "https://grafana.example.com")

	out := ExtractGrafanaInfoFromHeaders(context.Background(), r)
	cfg := GrafanaConfigFromContext(out)
	if cfg.APIKey != "header-token" {
		t.Errorf("APIKey=%q want header-token", cfg.APIKey)
	}
}

// TestComposedHTTPContextFunc_PreservesSessionAPIKey verifies the per-user
// APIKey placed on the context by upstream HTTP middleware (e.g. the auth
// middleware) survives the composed-context-func chain. Previously the
// chain's first step did `WithGrafanaConfig(ctx, config)` unconditionally,
// silently dropping the session-derived APIKey.
func TestComposedHTTPContextFunc_PreservesSessionAPIKey(t *testing.T) {
	staticCfg := GrafanaConfig{
		URL:   "http://localhost:3000",
		Debug: true,
	}
	ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
		APIKey: "session-token-from-middleware",
	})

	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	out := ComposedHTTPContextFunc(staticCfg)(ctx, r)
	cfg := GrafanaConfigFromContext(out)

	if cfg.APIKey != "session-token-from-middleware" {
		t.Errorf("APIKey=%q, expected session-token-from-middleware", cfg.APIKey)
	}
	if !cfg.Debug {
		t.Errorf("Debug should be carried over from static config")
	}
	if cfg.URL == "" {
		t.Errorf("URL should be carried over from static config, got empty")
	}
}

// TestPreservingPerRequestFields covers the credential-merging contract:
// per-request fields override the receiver, deployment-level fields stay.
func TestPreservingPerRequestFields(t *testing.T) {
	base := GrafanaConfig{
		URL:   "http://localhost:3000",
		Debug: true,
	}

	// Empty other → no per-request overrides.
	got := base.PreservingPerRequestFields(GrafanaConfig{})
	if got.APIKey != "" || got.AccessToken != "" || got.OrgID != 0 {
		t.Errorf("empty other should not populate per-request fields: %+v", got)
	}
	if !got.Debug {
		t.Errorf("baseline Debug should survive: %+v", got)
	}

	// Per-request fields overlay.
	got = base.PreservingPerRequestFields(GrafanaConfig{
		APIKey:      "session-key",
		AccessToken: "obo-access",
		IDToken:     "obo-id",
		OrgID:       42,
	})
	if got.APIKey != "session-key" {
		t.Errorf("APIKey=%q", got.APIKey)
	}
	if got.AccessToken != "obo-access" || got.IDToken != "obo-id" {
		t.Errorf("OBO fields not preserved: %+v", got)
	}
	if got.OrgID != 42 {
		t.Errorf("OrgID=%d", got.OrgID)
	}
	// Baseline fields still set.
	if got.URL == "" || !got.Debug {
		t.Errorf("baseline fields lost: %+v", got)
	}
}

// TestPreservingPerRequestFields_PreservesURL is the regression guard for
// the per-user auth flow: auth.Middleware pins cfg.URL to the operator-
// configured Grafana base URL, and the composed SSE/HTTP context func
// must NOT drop that value back to the baseline (which is typically
// empty — GRAFANA_URL is read at request time via
// ExtractGrafanaInfoFromHeaders). Dropping URL on composition reopens
// the X-Grafana-URL exfil path the auth middleware closes.
func TestPreservingPerRequestFields_PreservesURL(t *testing.T) {
	// Baseline with empty URL (typical CLI shape).
	base := GrafanaConfig{}

	// Per-request URL pinned by the auth middleware.
	got := base.PreservingPerRequestFields(GrafanaConfig{
		URL:    "https://grafana.internal:3000",
		APIKey: "session-key",
	})
	if got.URL != "https://grafana.internal:3000" {
		t.Errorf("URL pinned by per-request context was dropped: got %q, want %q", got.URL, "https://grafana.internal:3000")
	}
	if got.APIKey != "session-key" {
		t.Errorf("APIKey not preserved alongside URL: %+v", got)
	}

	// Baseline with a URL set, no per-request URL → baseline wins (the
	// existing no-auth deployment path keeps working).
	base = GrafanaConfig{URL: "http://baseline.example/"}
	got = base.PreservingPerRequestFields(GrafanaConfig{})
	if got.URL != "http://baseline.example/" {
		t.Errorf("empty per-request URL should not clobber baseline: got %q", got.URL)
	}
}
