package mcpgrafana

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

	promURL    = "PROM_URL"
	promSecret = "PROM_SECRET"
	promCAPath = "PROM_CA_PATH"

	grafanaURLEnvVar = "GRAFANA_URL"
	grafanaAPIEnvVar = "GRAFANA_API_KEY"

	authorizationHeader = "Authorization"
	grafanaURLHeader    = "X-Grafana-URL"
	grafanaAPIKeyHeader = "X-Grafana-API-Key"
)

func urlAndAPIKeyFromEnv() (string, string) {
	u := strings.TrimRight(os.Getenv(grafanaURLEnvVar), "/")
	apiKey := os.Getenv(grafanaAPIEnvVar)
	return u, apiKey
}

func urlAndAPIKeyFromHeaders(req *http.Request) (string, string) {
	u := strings.TrimRight(req.Header.Get(grafanaURLHeader), "/")
	apiKey := req.Header.Get(grafanaAPIKeyHeader)
	return u, apiKey
}

// grafanaConfigKey is the context key for local configuration.
type localConfigKey struct{}

// authorizationKey is the context key for Authorization header.
type authorizationKey struct{}

// grafanaConfigKey is the context key for Grafana configuration.
type grafanaConfigKey struct{}

// TLSConfig holds TLS configuration for Grafana clients.
type TLSConfig struct {
	CertFile   string
	KeyFile    string
	CAFile     string
	SkipVerify bool
}

// LocalConfig represents the full configuration for local usage.
type LocalConfig struct {
	// PromURL is the URL of the prometheus instance.
	PromURL string
	// PromSecret is the authorization credentials provided to the round tripper
	// when Authorization header is not set
	PromSecret string
	// PromCAPath is the path of the prometheus CA certificate file.
	PromCAPath string
}

// GrafanaConfig represents the full configuration for Grafana clients.
type GrafanaConfig struct {
	// Debug enables debug mode for the Grafana client.
	Debug bool

	// URL is the URL of the Grafana instance.
	URL string

	// APIKey is the API key or service account token for the Grafana instance.
	// It may be empty if we are using on-behalf-of auth.
	APIKey string

	// AccessToken is the Grafana Cloud access policy token used for on-behalf-of auth in Grafana Cloud.
	AccessToken string
	// IDToken is an ID token identifying the user for the current request.
	// It comes from the `X-Grafana-Id` header sent from Grafana to plugin backends.
	// It is used for on-behalf-of auth in Grafana Cloud.
	IDToken string

	// TLSConfig holds TLS configuration for all Grafana clients.
	TLSConfig *TLSConfig
}

// WithAuthorization adds Authorization header to the context.
func WithAuthorization(ctx context.Context, authorization string) context.Context {
	return context.WithValue(ctx, authorizationKey{}, authorization)
}

// AuthorizationFromContext retrieves the Authorization header from the context.
func AuthorizationFromContext(ctx context.Context) string {
	c, ok := ctx.Value(authorizationKey{}).(string)
	if !ok {
		return ""
	}
	return c
}

func HasAuthorization(ctx context.Context) bool {
	_, ok := ctx.Value(authorizationKey{}).(string)
	return ok
}

// LocalConfigFromContext retrieves the Authorization header from the context.
func LocalConfigFromContext(ctx context.Context) LocalConfig {
	c, ok := ctx.Value(localConfigKey{}).(LocalConfig)
	if !ok {
		return LocalConfig{}
	}
	return c
}

// WithLocalConfig adds local configuration to the context.
func WithLocalConfig(ctx context.Context, config LocalConfig) context.Context {
	return context.WithValue(ctx, localConfigKey{}, config)
}

func HasLocalConfig(ctx context.Context) bool {
	_, ok := ctx.Value(localConfigKey{}).(LocalConfig)
	return ok
}

// WithGrafanaConfig adds Grafana configuration to the context.
func WithGrafanaConfig(ctx context.Context, config GrafanaConfig) context.Context {
	return context.WithValue(ctx, grafanaConfigKey{}, config)
}

// GrafanaConfigFromContext extracts Grafana configuration from the context.
// If no config is found, returns a zero-value GrafanaConfig.
func GrafanaConfigFromContext(ctx context.Context) GrafanaConfig {
	if config, ok := ctx.Value(grafanaConfigKey{}).(GrafanaConfig); ok {
		return config
	}
	return GrafanaConfig{}
}

// CreateTLSConfig creates a *tls.Config from TLSConfig.
func (tc *TLSConfig) CreateTLSConfig() (*tls.Config, error) {
	if tc == nil {
		return nil, nil
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: tc.SkipVerify,
	}

	// Load client certificate if both cert and key files are provided
	if tc.CertFile != "" && tc.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(tc.CertFile, tc.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Load CA certificate if provided
	if tc.CAFile != "" {
		caCert, err := os.ReadFile(tc.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}

// HTTPTransport creates an HTTP transport with custom TLS configuration.
func (tc *TLSConfig) HTTPTransport(defaultTransport *http.Transport) (http.RoundTripper, error) {
	transport := defaultTransport.Clone()

	if tc != nil {
		tlsCfg, err := tc.CreateTLSConfig()
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsCfg
	}

	return transport, nil
}

// ExtractStdioLocalInfoFromEnv is a StdioContextFunc that extracts local configuration
// from environment variables and injects a configured client into the context.
var ExtractStdioLocalInfoFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	return WithLocalConfig(ctx, LocalConfig{
		PromURL:    os.Getenv(promURL),
		PromSecret: os.Getenv(promSecret),
		PromCAPath: os.Getenv(promCAPath),
	})
}

// ExtractHttpLocalInfoFromEnv is a StdioContextFunc that extracts local configuration
// from environment variables and injects a configured client into the context.
var ExtractHttpLocalInfoFromEnv httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	return WithLocalConfig(ctx, LocalConfig{
		PromURL:    os.Getenv(promURL),
		PromSecret: os.Getenv(promSecret),
		PromCAPath: os.Getenv(promCAPath),
	})
}

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

	// Get existing config or create a new one.
	// This will respect the existing debug flag, if set.
	config := GrafanaConfigFromContext(ctx)
	config.URL = u
	config.APIKey = apiKey
	return WithGrafanaConfig(ctx, config)
}

// httpContextFunc is a function that can be used as a `server.HTTPContextFunc` or a
// `server.SSEContextFunc`. It is necessary because, while the two types are functionally
// identical, they have distinct types and cannot be passed around interchangeably.
type httpContextFunc func(ctx context.Context, req *http.Request) context.Context

// ExtractAuthorizationFromHeaders is a HTTPContextFunc that extracts Authorization
// from request headers and injects a configured client into the context.
var ExtractAuthorizationFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	return WithAuthorization(ctx, req.Header.Get(authorizationHeader))
}

// ExtractGrafanaInfoFromHeaders is a HTTPContextFunc that extracts Grafana configuration
// from request headers and injects a configured client into the context.
var ExtractGrafanaInfoFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
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

	// Get existing config or create a new one.
	// This will respect the existing debug flag, if set.
	config := GrafanaConfigFromContext(ctx)
	config.URL = u
	config.APIKey = apiKey
	return WithGrafanaConfig(ctx, config)
}

// WithOnBehalfOfAuth adds the Grafana access token and user token to the
// Grafana config. These tokens are used for on-behalf-of auth in Grafana Cloud.
func WithOnBehalfOfAuth(ctx context.Context, accessToken, userToken string) (context.Context, error) {
	if accessToken == "" || userToken == "" {
		return nil, fmt.Errorf("neither accessToken nor userToken can be empty")
	}
	cfg := GrafanaConfigFromContext(ctx)
	cfg.AccessToken = accessToken
	cfg.IDToken = userToken
	return WithGrafanaConfig(ctx, cfg), nil
}

// MustWithOnBehalfOfAuth adds the access and user tokens to the context,
// panicking if either are empty.
func MustWithOnBehalfOfAuth(ctx context.Context, accessToken, userToken string) context.Context {
	ctx, err := WithOnBehalfOfAuth(ctx, accessToken, userToken)
	if err != nil {
		panic(err)
	}
	return ctx
}

type grafanaClientKey struct{}

func makeBasePath(path string) string {
	return strings.Join([]string{strings.TrimRight(path, "/"), "api"}, "/")
}

// NewGrafanaClient creates a Grafana client with the provided URL and API key,
// configured to use the correct scheme, debug mode, and TLS configuration.
func NewGrafanaClient(ctx context.Context, grafanaURL, apiKey string) *client.GrafanaHTTPAPI {
	cfg := client.DefaultTransportConfig()

	var parsedURL *url.URL
	var err error

	if grafanaURL == "" {
		grafanaURL = defaultGrafanaURL
	}

	parsedURL, err = url.Parse(grafanaURL)
	if err != nil {
		panic(fmt.Errorf("invalid Grafana URL: %w", err))
	}
	cfg.Host = parsedURL.Host
	cfg.BasePath = makeBasePath(parsedURL.Path)

	// The Grafana client will always prefer HTTPS even if the URL is HTTP,
	// so we need to limit the schemes to HTTP if the URL is HTTP.
	if parsedURL.Scheme == "http" {
		cfg.Schemes = []string{"http"}
	}

	if apiKey != "" {
		cfg.APIKey = apiKey
	}

	config := GrafanaConfigFromContext(ctx)
	cfg.Debug = config.Debug

	// Configure TLS if custom TLS configuration is provided
	if tlsConfig := config.TLSConfig; tlsConfig != nil {
		tlsCfg, err := tlsConfig.CreateTLSConfig()
		if err != nil {
			panic(fmt.Errorf("failed to create TLS config: %w", err))
		}
		cfg.TLSConfig = tlsCfg
		slog.Debug("Using custom TLS configuration",
			"cert_file", tlsConfig.CertFile,
			"ca_file", tlsConfig.CAFile,
			"skip_verify", tlsConfig.SkipVerify)
	}

	slog.Debug("Creating Grafana client", "url", parsedURL.Redacted(), "api_key_set", apiKey != "")
	return client.NewHTTPClientWithConfig(strfmt.Default, cfg)
}

// ExtractGrafanaClientFromEnv is a StdioContextFunc that extracts Grafana configuration
// from environment variables and injects a configured client into the context.
var ExtractGrafanaClientFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	// Extract transport config from env vars
	grafanaURL, ok := os.LookupEnv(grafanaURLEnvVar)
	if !ok {
		grafanaURL = defaultGrafanaURL
	}
	apiKey := os.Getenv(grafanaAPIEnvVar)

	grafanaClient := NewGrafanaClient(ctx, grafanaURL, apiKey)
	return context.WithValue(ctx, grafanaClientKey{}, grafanaClient)
}

// ExtractGrafanaClientFromHeaders is a HTTPContextFunc that extracts Grafana configuration
// from request headers and injects a configured client into the context.
var ExtractGrafanaClientFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	// Extract transport config from request headers, and set it on the context.
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

	grafanaClient := NewGrafanaClient(ctx, u, apiKey)
	return WithGrafanaClient(ctx, grafanaClient)
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

func HasGrafanaClient(ctx context.Context) bool {
	_, ok := ctx.Value(grafanaClientKey{}).(*client.GrafanaHTTPAPI)
	return ok
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

	// Configure custom TLS if available
	if tlsConfig := GrafanaConfigFromContext(ctx).TLSConfig; tlsConfig != nil {
		transport, err := tlsConfig.HTTPTransport(http.DefaultTransport.(*http.Transport))
		if err != nil {
			slog.Error("Failed to create custom transport for incident client, using default", "error", err)
		} else {
			client.HTTPClient.Transport = transport
			slog.Debug("Using custom TLS configuration for incident client",
				"cert_file", tlsConfig.CertFile,
				"ca_file", tlsConfig.CAFile,
				"skip_verify", tlsConfig.SkipVerify)
		}
	}

	return context.WithValue(ctx, incidentClientKey{}, client)
}

var ExtractIncidentClientFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	grafanaURL, apiKey := urlAndAPIKeyFromHeaders(req)
	grafanaURLEnv, apiKeyEnv := urlAndAPIKeyFromEnv()
	if grafanaURL == "" {
		grafanaURL = grafanaURLEnv
	}
	if grafanaURL == "" {
		grafanaURL = defaultGrafanaURL
	}
	if apiKey == "" {
		apiKey = apiKeyEnv
	}
	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/resources/api/v1/", grafanaURL)
	client := incident.NewClient(incidentURL, apiKey)

	// Configure custom TLS if available
	if tlsConfig := GrafanaConfigFromContext(ctx).TLSConfig; tlsConfig != nil {
		transport, err := tlsConfig.HTTPTransport(http.DefaultTransport.(*http.Transport))
		if err != nil {
			slog.Error("Failed to create custom transport for incident client, using default", "error", err)
		} else {
			client.HTTPClient.Transport = transport
			slog.Debug("Using custom TLS configuration for incident client",
				"cert_file", tlsConfig.CertFile,
				"ca_file", tlsConfig.CAFile,
				"skip_verify", tlsConfig.SkipVerify)
		}
	}

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
func ComposeSSEContextFuncs(funcs ...httpContextFunc) server.SSEContextFunc {
	return func(ctx context.Context, req *http.Request) context.Context {
		for _, f := range funcs {
			ctx = f(ctx, req)
		}
		return ctx
	}
}

// ComposeHTTPContextFuncs composes multiple HTTPContextFuncs into a single one.
func ComposeHTTPContextFuncs(funcs ...httpContextFunc) server.HTTPContextFunc {
	return func(ctx context.Context, req *http.Request) context.Context {
		for _, f := range funcs {
			ctx = f(ctx, req)
		}
		return ctx
	}
}

// ComposedStdioContextFunc returns a StdioContextFunc that comprises all predefined StdioContextFuncs,
// as well as the Grafana debug flag and TLS configuration.
func ComposedStdioContextFunc(config *GrafanaConfig) server.StdioContextFunc {
	return ComposeStdioContextFuncs(
		func(ctx context.Context) context.Context {
			return WithGrafanaConfig(ctx, *config)
		},
		ExtractGrafanaInfoFromEnv,
		ExtractGrafanaClientFromEnv,
		ExtractIncidentClientFromEnv,
	)
}

// ComposedSSEContextFunc is a SSEContextFunc that comprises all predefined SSEContextFuncs.
func ComposedSSEContextFunc(config *GrafanaConfig) server.SSEContextFunc {
	return ComposeSSEContextFuncs(
		func(ctx context.Context, req *http.Request) context.Context {
			return WithGrafanaConfig(ctx, *config)
		},
		ExtractGrafanaInfoFromHeaders,
		ExtractGrafanaClientFromHeaders,
		ExtractIncidentClientFromHeaders,
	)
}

// ComposedHTTPContextFunc is a HTTPContextFunc that comprises all predefined HTTPContextFuncs.
func ComposedHTTPContextFunc(config *GrafanaConfig) server.HTTPContextFunc {
	return ComposeHTTPContextFuncs(
		func(ctx context.Context, req *http.Request) context.Context {
			return WithGrafanaConfig(ctx, *config)
		},
		ExtractGrafanaInfoFromHeaders,
		ExtractGrafanaClientFromHeaders,
		ExtractIncidentClientFromHeaders,
	)
}
