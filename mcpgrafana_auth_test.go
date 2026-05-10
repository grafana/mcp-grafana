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
