package mcpgrafana

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/incident-go"
	"github.com/mark3labs/mcp-go/server"
)

const (
	grafanaURLEnvVar = "GRAFANA_URL"
	grafanaAPIEnvVar = "GRAFANA_API_KEY"

	grafanaURLHeader    = "X-Grafana-URL"
	grafanaAPIKeyHeader = "X-Grafana-API-Key"
)

func urlAndAPIKeyFromEnv() (string, string) {
	u := os.Getenv(grafanaURLEnvVar)
	apiKey := os.Getenv(grafanaAPIEnvVar)
	return u, apiKey
}

func urlAndAPIKeyFromHeaders(req *http.Request) (string, string) {
	u := req.Header.Get(grafanaURLHeader)
	apiKey := req.Header.Get(grafanaAPIKeyHeader)
	return u, apiKey
}

type grafanaURLKey struct{}
type grafanaAPIKeyKey struct{}

// ExtractGrafanaInfoFromEnv is a StdioContextFunc that extracts Grafana configuration
// from environment variables and injects a configured client into the context.
var ExtractGrafanaInfoFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	u, apiKey := urlAndAPIKeyFromEnv()
	return WithGrafanaURL(WithGrafanaAPIKey(ctx, apiKey), u)
}

// ExtractGrafanaInfoFromHeaders is a SSEContextFunc that extracts Grafana configuration
// from request headers and injects a configured client into the context.
var ExtractGrafanaInfoFromHeaders server.SSEContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	u, apiKey := urlAndAPIKeyFromHeaders(req)
	return WithGrafanaURL(WithGrafanaAPIKey(ctx, apiKey), u)
}

// WithGrafanaURL adds the Grafana URL to the context.
func WithGrafanaURL(ctx context.Context, url string) context.Context {
	return context.WithValue(ctx, grafanaURLKey{}, url)
}

// WithGrafanaAPIKey adds the Grafana API key to the context.
func WithGrafanaAPIKey(ctx context.Context, apiKey string) context.Context {
	return context.WithValue(ctx, grafanaAPIKeyKey{}, apiKey)
}

// GrafanaURLFromContext extracts the Grafana URL from the context.
func GrafanaURLFromContext(ctx context.Context) string {
	return ctx.Value(grafanaURLKey{}).(string)
}

// GrafanaAPIKeyFromContext extracts the Grafana API key from the context.
func GrafanaAPIKeyFromContext(ctx context.Context) string {
	return ctx.Value(grafanaAPIKeyKey{}).(string)
}

type grafanaClientKey struct{}

// ExtractGrafanaClientFromEnv is a StdioContextFunc that extracts Grafana configuration
// from environment variables and injects a configured client into the context.
var ExtractGrafanaClientFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
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
	return context.WithValue(ctx, grafanaClientKey{}, client)
}

// ExtractGrafanaClientFromHeaders is a SSEContextFunc that extracts Grafana configuration
// from request headers and injects a configured client into the context.
var ExtractGrafanaClientFromHeaders server.SSEContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	cfg := client.DefaultTransportConfig()
	// Extract transport config from request headers, and set it on the context.
	u, apiKey := urlAndAPIKeyFromHeaders(req)
	if u != "" {
		if url, err := url.Parse(u); err == nil {
			cfg.Host = url.Host
			if url.Scheme == "http" {
				cfg.Schemes = []string{"http"}
			}
		}
	}
	if apiKey != "" {
		cfg.APIKey = apiKey
	}
	client := client.NewHTTPClientWithConfig(strfmt.Default, cfg)
	return WithGrafanaClient(ctx, client)
}

// WithGrafanaClient sets the Grafana client in the context.
//
// It can be retrieved using GrafanaClientFromContext.
func WithGrafanaClient(ctx context.Context, client *client.GrafanaHTTPAPI) context.Context {
	return context.WithValue(ctx, grafanaClientKey{}, client)
}

// GrafanaClientFromContext retrieves the Grafana client from the context.
func GrafanaClientFromContext(ctx context.Context) *client.GrafanaHTTPAPI {
	c, ok := ctx.Value(grafanaClientKey{}).(*client.GrafanaHTTPAPI)
	if !ok {
		return nil
	}
	return c
}

type incidentClientKey struct{}

var ExtractIncidentClientFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	grafanaURL, apiKey := urlAndAPIKeyFromEnv()
	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-incident-app/resources/api", grafanaURL)
	client := incident.NewClient(incidentURL, apiKey)
	return context.WithValue(ctx, incidentClientKey{}, client)
}

var ExtractIncidentClientFromHeaders server.SSEContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	grafanaURL, apiKey := urlAndAPIKeyFromHeaders(req)
	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-incident-app/resources/api", grafanaURL)
	client := incident.NewClient(incidentURL, apiKey)
	return context.WithValue(ctx, incidentClientKey{}, client)
}

func WithIncidentClient(ctx context.Context, client *incident.Client) context.Context {
	return context.WithValue(ctx, incidentClientKey{}, client)
}

func IncidentClientFromContext(ctx context.Context) *incident.Client {
	c, ok := ctx.Value(incidentClientKey{}).(*incident.Client)
	if !ok {
		return nil
	}
	return c
}

// ComposeStdioContextFuncs composes multiple StdioContextFuncs into a single one.
func ComposeStdioContextFuncs(funcs ...server.StdioContextFunc) server.StdioContextFunc {
	return func(ctx context.Context) context.Context {
		for _, f := range funcs {
			ctx = f(ctx)
		}
		return ctx
	}
}

// ComposeSSEContextFuncs composes multiple SSEContextFuncs into a single one.
func ComposeSSEContextFuncs(funcs ...server.SSEContextFunc) server.SSEContextFunc {
	return func(ctx context.Context, req *http.Request) context.Context {
		for _, f := range funcs {
			ctx = f(ctx, req)
		}
		return ctx
	}
}

// ComposedStdioContextFunc is a StdioContextFunc that comprises all predefined StdioContextFuncs.
var ComposedStdioContextFunc = ComposeStdioContextFuncs(
	ExtractGrafanaInfoFromEnv,
	ExtractGrafanaClientFromEnv,
	ExtractIncidentClientFromEnv,
)

// ComposedSSEContextFunc is a SSEContextFunc that comprises all predefined SSEContextFuncs.
var ComposedSSEContextFunc = ComposeSSEContextFuncs(
	ExtractGrafanaInfoFromHeaders,
	ExtractGrafanaClientFromHeaders,
	ExtractIncidentClientFromHeaders,
)
