package mcpgrafana

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/incident-go"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultGrafanaHost = "localhost:3000"
	defaultGrafanaURL  = "http://" + defaultGrafanaHost

	grafanaURLEnvVar = "GRAFANA_URL"
	grafanaAPIEnvVar = "GRAFANA_API_KEY"

	grafanaURLHeader    = "X-Grafana-URL"
	grafanaAPIKeyHeader = "X-Grafana-API-Key"
)

func urlAndAPIKeyFromEnv() (string, string) {
	u := strings.TrimRight(os.Getenv(grafanaURLEnvVar), "/")
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
	if u == "" {
		u = defaultGrafanaURL
	}
	parsedURL, err := url.Parse(u)
	if err != nil {
		panic(fmt.Errorf("invalid Grafana URL %s: %w", u, err))
	}
	slog.Info("Using Grafana configuration", "url", parsedURL.Redacted(), "api_key_set", apiKey != "")
	return WithGrafanaURL(WithGrafanaAPIKey(ctx, apiKey), u)
}

// ExtractGrafanaInfoFromHeaders is a SSEContextFunc that extracts Grafana configuration
// from request headers and injects a configured client into the context.
var ExtractGrafanaInfoFromHeaders server.SSEContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	u, apiKey := urlAndAPIKeyFromHeaders(req)
	uEnv, apiKeyEnv := urlAndAPIKeyFromEnv()
	if u == "" {
		u = uEnv
	}
	if u == "" {
		u = defaultGrafanaURL
	}
	if apiKey == "" {
		apiKey = apiKeyEnv
	}
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
	if u, ok := ctx.Value(grafanaURLKey{}).(string); ok {
		return u
	}
	return defaultGrafanaURL
}

// GrafanaAPIKeyFromContext extracts the Grafana API key from the context.
func GrafanaAPIKeyFromContext(ctx context.Context) string {
	if k, ok := ctx.Value(grafanaAPIKeyKey{}).(string); ok {
		return k
	}
	return ""
}

type grafanaClientKey struct{}

// ExtractGrafanaClientFromEnv is a StdioContextFunc that extracts Grafana configuration
// from environment variables and injects a configured client into the context.
var ExtractGrafanaClientFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	cfg := client.DefaultTransportConfig()
	// Extract transport config from env vars, and set it on the context.
	var grafanaURL string
	var parsedURL *url.URL
	var ok bool
	var err error
	if grafanaURL, ok = os.LookupEnv(grafanaURLEnvVar); ok {
		parsedURL, err = url.Parse(grafanaURL)
		if err != nil {
			panic(fmt.Errorf("invalid %s: %w", grafanaURLEnvVar, err))
		}
		cfg.Host = parsedURL.Host
		// The Grafana client will always prefer HTTPS even if the URL is HTTP,
		// so we need to limit the schemes to HTTP if the URL is HTTP.
		if parsedURL.Scheme == "http" {
			cfg.Schemes = []string{"http"}
		}
	} else {
		parsedURL, _ = url.Parse(defaultGrafanaURL)
	}

	apiKey := os.Getenv(grafanaAPIEnvVar)
	if apiKey != "" {
		cfg.APIKey = apiKey
	}

	slog.Debug("Creating Grafana client", "url", parsedURL.Redacted(), "api_key_set", apiKey != "")
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
	if grafanaURL == "" {
		grafanaURL = defaultGrafanaURL
	}
	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/resources/api/v1/", grafanaURL)
	parsedURL, err := url.Parse(incidentURL)
	if err != nil {
		panic(fmt.Errorf("invalid incident URL %s: %w", incidentURL, err))
	}
	slog.Debug("Creating Incident client", "url", parsedURL.Redacted(), "api_key_set", apiKey != "")
	client := incident.NewClient(incidentURL, apiKey)
	return context.WithValue(ctx, incidentClientKey{}, client)
}

var ExtractIncidentClientFromHeaders server.SSEContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	grafanaURL, apiKey := urlAndAPIKeyFromHeaders(req)
	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/resources/api/v1/", grafanaURL)
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
