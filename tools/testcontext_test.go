//go:build integration

package tools

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

// newTestContext creates a new context with the Grafana URL and service account token
// from the environment variables GRAFANA_URL and GRAFANA_SERVICE_ACCOUNT_TOKEN (or deprecated GRAFANA_API_KEY).
// It also injects a GrafanaInstance so that tests exercise the capability-aware code path.
func newTestContext() context.Context {
	cfg := client.DefaultTransportConfig()
	cfg.Host = "localhost:3000"
	cfg.Schemes = []string{"http"}

	grafanaURL := "http://localhost:3000"

	// Extract transport config from env vars, and set it on the context.
	if u, ok := os.LookupEnv("GRAFANA_URL"); ok {
		parsedURL, err := url.Parse(u)
		if err != nil {
			panic(fmt.Errorf("invalid %s: %w", "GRAFANA_URL", err))
		}
		cfg.Host = parsedURL.Host
		grafanaURL = u
		// The Grafana client will always prefer HTTPS even if the URL is HTTP,
		// so we need to limit the schemes to HTTP if the URL is HTTP.
		if parsedURL.Scheme == "http" {
			cfg.Schemes = []string{"http"}
		}
	}

	// Check for the new service account token environment variable first
	if apiKey := os.Getenv("GRAFANA_SERVICE_ACCOUNT_TOKEN"); apiKey != "" {
		cfg.APIKey = apiKey
	} else if apiKey := os.Getenv("GRAFANA_API_KEY"); apiKey != "" {
		// Fall back to the deprecated API key environment variable
		cfg.APIKey = apiKey
	} else {
		cfg.BasicAuth = url.UserPassword("admin", "admin")
	}

	legacyClient := client.NewHTTPClientWithConfig(strfmt.Default, cfg)

	grafanaCfg := mcpgrafana.GrafanaConfig{
		Debug:     true,
		URL:       grafanaURL,
		APIKey:    cfg.APIKey,
		BasicAuth: cfg.BasicAuth,
	}

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)
	ctx = mcpgrafana.WithGrafanaClient(ctx, legacyClient)

	// Create and inject a GrafanaInstance so that capability-aware code paths
	// are exercised by all integration tests (e.g. getDashboardByUID will enter
	// the ShouldUseKubernetesAPI branch instead of the legacy-only early return).
	httpClient := mcpgrafana.NewHTTPClient(ctx, grafanaCfg)
	instance := mcpgrafana.NewGrafanaInstance(grafanaCfg, legacyClient, httpClient)
	ctx = mcpgrafana.WithGrafanaInstance(ctx, instance)

	return ctx
}
