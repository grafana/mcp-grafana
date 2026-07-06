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
func newTestContext() context.Context {
	grafanaURL := "http://localhost:3000"
	if u, ok := os.LookupEnv("GRAFANA_URL"); ok {
		grafanaURL = u
	}
	return newTestContextForURL(grafanaURL)
}

// newTestContextForURL builds a context targeting a specific Grafana base URL.
// It wires both the legacy Grafana client and the Kubernetes-style client, so
// dashboard tools exercise the same k8s-or-legacy routing as in production. Use
// this to target the legacy instance (e.g. http://localhost:3002) directly.
func newTestContextForURL(grafanaURL string) context.Context {
	cfg := client.DefaultTransportConfig()
	parsed, err := url.Parse(grafanaURL)
	if err != nil {
		panic(fmt.Errorf("invalid Grafana URL %q: %w", grafanaURL, err))
	}
	cfg.Host = parsed.Host
	// The Grafana client will always prefer HTTPS even if the URL is HTTP,
	// so we need to limit the schemes to HTTP if the URL is HTTP.
	if parsed.Scheme == "http" {
		cfg.Schemes = []string{"http"}
	} else {
		cfg.Schemes = []string{"https"}
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

	client := client.NewHTTPClientWithConfig(strfmt.Default, cfg)

	grafanaCfg := mcpgrafana.GrafanaConfig{
		Debug:     true,
		URL:       grafanaURL,
		APIKey:    cfg.APIKey,
		BasicAuth: cfg.BasicAuth,
	}

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)
	ctx = mcpgrafana.WithGrafanaClient(ctx, &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: client})

	// Wire the Kubernetes-style client too, so dashboard tools can reach the
	// dashboard.grafana.app API (with automatic fallback to legacy when absent).
	k8sClient, err := mcpgrafana.NewKubernetesClient(ctx)
	if err != nil {
		panic(fmt.Errorf("create kubernetes client: %w", err))
	}
	return mcpgrafana.WithKubernetesClient(ctx, k8sClient)
}
