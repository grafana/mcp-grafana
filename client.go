package mcpgrafana

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/mark3labs/mcp-go/server"
)

type clientKey struct{}

const (
	grafanaURLEnvVar = "GRAFANA_URL"
	grafanaAPIEnvVar = "GRAFANA_API_KEY"

	grafanaURLHeader    = "X-Grafana-URL"
	grafanaAPIKeyHeader = "X-Grafana-API-Key"
)

// ExtractClientFromEnv is a StdioContextFunc that extracts Grafana configuration
// from environment variables and injects a configured client into the context.
var ExtractClientFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	cfg := client.DefaultTransportConfig()
	// Extract transport config from env vars, and set it on the context.
	if u, ok := os.LookupEnv(grafanaURLEnvVar); ok {
		url, err := url.Parse(u)
		if err != nil {
			panic(fmt.Errorf("invalid %s: %w", grafanaURLEnvVar, err))
		}
		cfg.Host = url.Host
		// The Grafana client will always prefer HTTPS even if the URL is HTTP,
		// so we need to limit the schemes to HTTP if the URL is HTTP.
		if url.Scheme == "http" {
			cfg.Schemes = []string{"http"}
		}
	}
	if apiKey := os.Getenv(grafanaAPIEnvVar); apiKey != "" {
		cfg.APIKey = apiKey
	}

	client := client.NewHTTPClientWithConfig(strfmt.Default, cfg)
	return context.WithValue(ctx, clientKey{}, client)
}

// ExtractClientFromHeaders is a SSEContextFunc that extracts Grafana configuration
// from request headers and injects a configured client into the context.
var ExtractClientFromHeaders server.SSEContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	cfg := client.DefaultTransportConfig()
	// Extract transport config from request headers, and set it on the context.
	if u := req.Header.Get(grafanaURLHeader); u != "" {
		if url, err := url.Parse(u); err == nil {
			cfg.Host = url.Host
			if url.Scheme == "http" {
				cfg.Schemes = []string{"http"}
			}
		}
	}
	if apiKey := req.Header.Get(grafanaAPIKeyHeader); apiKey != "" {
		cfg.APIKey = apiKey
	}
	client := client.NewHTTPClientWithConfig(strfmt.Default, cfg)
	return WithGrafanaClient(ctx, client)
}

// WithGrafanaClient sets the Grafana client in the context.
//
// It can be retrieved using GrafanaClientFromContext.
func WithGrafanaClient(ctx context.Context, client *client.GrafanaHTTPAPI) context.Context {
	return context.WithValue(ctx, clientKey{}, client)
}

// GrafanaClientFromContext retrieves the Grafana client from the context.
func GrafanaClientFromContext(ctx context.Context) *client.GrafanaHTTPAPI {
	c, ok := ctx.Value(clientKey{}).(*client.GrafanaHTTPAPI)
	if !ok {
		return nil
	}
	return c
}
