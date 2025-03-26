package mcpgrafana

import (
	"context"
	"fmt"
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

	grafanaOnCallURLEnvVar   = "GRAFANA_ONCALL_URL"
	grafanaOnCallTokenEnvVar = "GRAFANA_ONCALL_TOKEN"

	grafanaURLHeader    = "X-Grafana-URL"
	grafanaAPIKeyHeader = "X-Grafana-API-Key"

	grafanaOnCallURLHeader   = "X-Grafana-OnCall-URL"
	grafanaOnCallTokenHeader = "X-Grafana-OnCall-Token"
)

func urlAndAPIKeyFromEnv() (string, string) {
	u := strings.TrimRight(os.Getenv(grafanaURLEnvVar), "/")
	apiKey := os.Getenv(grafanaAPIEnvVar)
	return u, apiKey
}

// oncallURLAndTokenFromEnv retrieves OnCall URL and token from environment variables
func oncallURLAndTokenFromEnv() (string, string) {
	u := strings.TrimRight(os.Getenv(grafanaOnCallURLEnvVar), "/")
	token := os.Getenv(grafanaOnCallTokenEnvVar)
	return u, token
}

func urlAndAPIKeyFromHeaders(req *http.Request) (string, string) {
	u := req.Header.Get(grafanaURLHeader)
	apiKey := req.Header.Get(grafanaAPIKeyHeader)
	return u, apiKey
}

// oncallURLAndTokenFromHeaders retrieves OnCall URL and token from request headers
func oncallURLAndTokenFromHeaders(req *http.Request) (string, string) {
	u := req.Header.Get(grafanaOnCallURLHeader)
	token := req.Header.Get(grafanaOnCallTokenHeader)
	return u, token
}

type grafanaURLKey struct{}
type grafanaAPIKeyKey struct{}
type grafanaOnCallURLKey struct{}
type grafanaOnCallTokenKey struct{}

// ExtractGrafanaInfoFromEnv is a StdioContextFunc that extracts Grafana configuration
// from environment variables and injects a configured client into the context.
var ExtractGrafanaInfoFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	u, apiKey := urlAndAPIKeyFromEnv()
	if u == "" {
		u = defaultGrafanaURL
	}
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

// WithGrafanaOnCallURL adds the Grafana OnCall URL to the context.
func WithGrafanaOnCallURL(ctx context.Context, url string) context.Context {
	return context.WithValue(ctx, grafanaOnCallURLKey{}, url)
}

// WithGrafanaOnCallToken adds the Grafana OnCall token to the context.
func WithGrafanaOnCallToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, grafanaOnCallTokenKey{}, token)
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

// GrafanaOnCallURLFromContext extracts the Grafana OnCall URL from the context.
func GrafanaOnCallURLFromContext(ctx context.Context) string {
	if u, ok := ctx.Value(grafanaOnCallURLKey{}).(string); ok {
		return u
	}
	return ""
}

// GrafanaOnCallTokenFromContext extracts the Grafana OnCall token from the context.
func GrafanaOnCallTokenFromContext(ctx context.Context) string {
	if k, ok := ctx.Value(grafanaOnCallTokenKey{}).(string); ok {
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
	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-incident-app/resources/api/v1/", grafanaURL)
	client := incident.NewClient(incidentURL, apiKey)
	return context.WithValue(ctx, incidentClientKey{}, client)
}

var ExtractIncidentClientFromHeaders server.SSEContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	grafanaURL, apiKey := urlAndAPIKeyFromHeaders(req)
	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-incident-app/resources/api/v1/", grafanaURL)
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

// Extract predefined context functions
var ExtractOnCallInfoFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	onCallURL, onCallToken := oncallURLAndTokenFromEnv()

	// Add OnCall values if they exist
	if onCallURL != "" {
		ctx = WithGrafanaOnCallURL(ctx, onCallURL)
	}
	if onCallToken != "" {
		ctx = WithGrafanaOnCallToken(ctx, onCallToken)
	}

	return ctx
}

var ExtractOnCallInfoFromHeaders server.SSEContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	onCallURL, onCallToken := oncallURLAndTokenFromHeaders(req)

	// If headers don't have OnCall values, check environment
	onCallURLEnv, onCallTokenEnv := oncallURLAndTokenFromEnv()
	if onCallURL == "" {
		onCallURL = onCallURLEnv
	}
	if onCallToken == "" {
		onCallToken = onCallTokenEnv
	}

	// Add OnCall values if they exist
	if onCallURL != "" {
		ctx = WithGrafanaOnCallURL(ctx, onCallURL)
	}
	if onCallToken != "" {
		ctx = WithGrafanaOnCallToken(ctx, onCallToken)
	}

	return ctx
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
	ExtractOnCallInfoFromEnv,
)

// ComposedSSEContextFunc is a SSEContextFunc that comprises all predefined SSEContextFuncs.
var ComposedSSEContextFunc = ComposeSSEContextFuncs(
	ExtractGrafanaInfoFromHeaders,
	ExtractGrafanaClientFromHeaders,
	ExtractIncidentClientFromHeaders,
	ExtractOnCallInfoFromHeaders,
)
