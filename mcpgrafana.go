package mcpgrafana

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/textproto"
	"net/url"
	"os"
	"reflect"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-openapi/runtime"
	openapiclient "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/incident-go"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/sync/singleflight"
)

const (
	defaultGrafanaHost = "localhost:3000"
	defaultGrafanaURL  = "http://" + defaultGrafanaHost

	grafanaURLEnvVar                     = "GRAFANA_URL"
	grafanaServiceAccountTokenEnvVar     = "GRAFANA_SERVICE_ACCOUNT_TOKEN"
	grafanaServiceAccountTokenFileEnvVar = "GRAFANA_SERVICE_ACCOUNT_TOKEN_FILE"
	grafanaAPIEnvVar                     = "GRAFANA_API_KEY" // Deprecated: use GRAFANA_SERVICE_ACCOUNT_TOKEN instead
	grafanaOrgIDEnvVar                   = "GRAFANA_ORG_ID"

	grafanaUsernameEnvVar = "GRAFANA_USERNAME"
	grafanaPasswordEnvVar = "GRAFANA_PASSWORD"

	grafanaExtraHeadersEnvVar   = "GRAFANA_EXTRA_HEADERS"
	grafanaForwardHeadersEnvVar = "GRAFANA_FORWARD_HEADERS"

	grafanaURLHeader                 = "X-Grafana-URL"
	grafanaServiceAccountTokenHeader = "X-Grafana-Service-Account-Token"
	grafanaAPIKeyHeader              = "X-Grafana-API-Key" // Deprecated: use X-Grafana-Service-Account-Token instead
)

func urlAndAPIKeyFromEnv(logger *slog.Logger) (string, string) {
	u := strings.TrimRight(os.Getenv(grafanaURLEnvVar), "/")

	// Check for the new service account token environment variable first.
	apiKey := os.Getenv(grafanaServiceAccountTokenEnvVar)
	if apiKey != "" {
		return u, apiKey
	}

	// Next, check for a file-based service account token. This is read fresh on
	// every call so that rotated tokens (e.g. a Kubernetes Secret mounted as a
	// volume) are picked up without restarting the server. See issue #800.
	if tokenFile := os.Getenv(grafanaServiceAccountTokenFileEnvVar); tokenFile != "" {
		token, err := os.ReadFile(tokenFile)
		if err != nil {
			logger.Warn("Failed to read GRAFANA_SERVICE_ACCOUNT_TOKEN_FILE, ignoring", "path", tokenFile, "error", err)
		} else if apiKey = strings.TrimSpace(string(token)); apiKey != "" {
			return u, apiKey
		}
	}

	// Fall back to the deprecated API key environment variable
	apiKey = os.Getenv(grafanaAPIEnvVar)
	if apiKey != "" {
		logger.Warn("GRAFANA_API_KEY is deprecated, please use GRAFANA_SERVICE_ACCOUNT_TOKEN instead. See https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana for details on creating service account tokens.")
	}

	return u, apiKey
}

func userAndPassFromEnv() *url.Userinfo {
	username := os.Getenv(grafanaUsernameEnvVar)
	password, exists := os.LookupEnv(grafanaPasswordEnvVar)
	if username == "" && password == "" {
		return nil
	}
	if !exists {
		return url.User(username)
	}
	return url.UserPassword(username, password)
}

func orgIdFromEnv(logger *slog.Logger) int64 {
	orgIDStr := os.Getenv(grafanaOrgIDEnvVar)
	if orgIDStr == "" {
		return 0
	}
	orgID, err := strconv.ParseInt(orgIDStr, 10, 64)
	if err != nil {
		logger.Warn("Invalid GRAFANA_ORG_ID value, ignoring", "value", orgIDStr, "error", err)
		return 0
	}
	return orgID
}

func extraHeadersFromEnv(logger *slog.Logger) map[string]string {
	headersJSON := os.Getenv(grafanaExtraHeadersEnvVar)
	if headersJSON == "" {
		return nil
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		logger.Warn("invalid GRAFANA_EXTRA_HEADERS value, ignoring", "value", headersJSON, "error", err)
		return nil
	}
	return headers
}

func forwardHeaderNamesFromEnv() []string {
	raw := os.Getenv(grafanaForwardHeadersEnvVar)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			names = append(names, p)
		}
	}
	return names
}

// forwardedHeadersFromRequest reads GRAFANA_FORWARD_HEADERS and copies matching
// headers from the incoming HTTP request. Returns nil when no headers match.
func forwardedHeadersFromRequest(req *http.Request) map[string]string {
	names := forwardHeaderNamesFromEnv()
	if len(names) == 0 {
		return nil
	}
	var forwarded map[string]string
	for _, name := range names {
		if v := req.Header.Get(name); v != "" {
			if forwarded == nil {
				forwarded = make(map[string]string, len(names))
			}
			forwarded[name] = v
		}
	}
	return forwarded
}

// mergeHeaders returns a new map containing all entries from base, with entries
// from override taking precedence. When both maps are non-empty, header names
// are canonicalized (via textproto.CanonicalMIMEHeaderKey) so that
// case-insensitive matches are merged correctly and the documented
// guarantee—incoming request wins—is upheld. When only one side is present the
// original key casing is preserved.
func mergeHeaders(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	if len(override) == 0 {
		return base
	}
	if len(base) == 0 {
		return override
	}
	merged := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		merged[textproto.CanonicalMIMEHeaderKey(k)] = v
	}
	for k, v := range override {
		merged[textproto.CanonicalMIMEHeaderKey(k)] = v
	}
	return merged
}

func orgIdFromHeaders(req *http.Request, logger *slog.Logger) int64 {
	orgIDStr := req.Header.Get(client.OrgIDHeader)
	if orgIDStr == "" {
		return 0
	}
	orgID, err := strconv.ParseInt(orgIDStr, 10, 64)
	if err != nil {
		logger.Warn("Invalid X-Grafana-Org-Id header value, ignoring", "value", orgIDStr, "error", err)
		return 0
	}
	return orgID
}

func urlAndAPIKeyFromHeaders(req *http.Request) (string, string) {
	u := strings.TrimRight(req.Header.Get(grafanaURLHeader), "/")

	// Check for the new service account token header first
	apiKey := req.Header.Get(grafanaServiceAccountTokenHeader)
	if apiKey != "" {
		return u, apiKey
	}

	// Fall back to the deprecated API key header
	apiKey = req.Header.Get(grafanaAPIKeyHeader)
	return u, apiKey
}

// grafanaConfigKey is the context key for Grafana configuration.
type grafanaConfigKey struct{}

// TLSConfig holds TLS configuration for Grafana clients.
// It supports mutual TLS authentication with client certificates, custom CA certificates for server verification, and development options like skipping certificate verification.
type TLSConfig struct {
	CertFile   string
	KeyFile    string
	CAFile     string
	SkipVerify bool
}

// GrafanaConfig represents the full configuration for Grafana clients.
// It includes connection details, authentication credentials, debug settings, and TLS options used throughout the MCP server's lifecycle.
type GrafanaConfig struct {
	// Debug enables debug mode for the Grafana client.
	Debug bool

	// IncludeArgumentsInSpans enables logging of tool arguments in OpenTelemetry spans.
	// This should only be enabled in non-production environments or when you're certain
	// the arguments don't contain PII. Defaults to false for safety.
	// Note: OpenTelemetry spans are always created for context propagation, but arguments
	// are only included when this flag is enabled.
	IncludeArgumentsInSpans bool

	// URL is the URL of the Grafana instance.
	URL string

	// APIKey is the API key or service account token for the Grafana instance.
	// It may be empty if we are using on-behalf-of auth.
	APIKey string

	// Credentials if user is using basic auth
	BasicAuth *url.Userinfo

	// OrgID is the organization ID to use for multi-org support.
	// When set, it will be sent as X-Grafana-Org-Id header regardless of authentication method.
	// Works with service account tokens, API keys, and basic authentication.
	OrgID int64

	// AccessToken is the Grafana Cloud access policy token used for on-behalf-of auth in Grafana Cloud.
	AccessToken string
	// IDToken is an ID token identifying the user for the current request.
	// It comes from the `X-Grafana-Id` header sent from Grafana to plugin backends.
	// It is used for on-behalf-of auth in Grafana Cloud.
	IDToken string

	// TLSConfig holds TLS configuration for all Grafana clients.
	TLSConfig *TLSConfig

	// Timeout specifies a time limit for requests made by the Grafana client.
	// A Timeout of zero means no timeout.
	// Default is 10 seconds.
	Timeout time.Duration

	// ExtraHeaders contains additional HTTP headers to send with all Grafana API requests.
	// Parsed from GRAFANA_EXTRA_HEADERS environment variable as JSON object.
	ExtraHeaders map[string]string

	// MaxLokiLogLimit is the maximum number of log lines that can be returned
	// from Loki queries.
	MaxLokiLogLimit int

	// BaseTransport is an optional base HTTP transport used as the innermost
	// layer of the middleware chain in NewGrafanaClient. When set, it replaces
	// the default http.Transport that NewGrafanaClient would otherwise create.
	// The caller can use this to provide a pre-configured transport with custom
	// connection pooling, timeouts, or tracing instrumentation.
	// Note: NewGrafanaClient still wraps this transport with ExtraHeaders,
	// OrgID, UserAgent, and otelhttp layers.
	BaseTransport http.RoundTripper

	// Logger is an optional structured logger. When set, functions that have
	// access to the GrafanaConfig will use this logger instead of the global
	// slog.Default(). This allows callers (e.g. the hosted Cloud MCP server)
	// to inject their own slog.Logger for consistent structured logging with
	// per-request context such as tenant_id.
	Logger *slog.Logger
}

// HTTPTransport returns the base HTTP transport for this config.
// If BaseTransport is set it is returned; otherwise http.DefaultTransport.
func (c GrafanaConfig) HTTPTransport() http.RoundTripper {
	if c.BaseTransport != nil {
		return c.BaseTransport
	}
	return http.DefaultTransport
}

// LoggerOrDefault returns the configured logger, or slog.Default() if none is set.
func (c GrafanaConfig) LoggerOrDefault() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// LoggerFromContext extracts the logger from the GrafanaConfig in the context.
// Returns slog.Default() if no config or logger is set.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	return GrafanaConfigFromContext(ctx).LoggerOrDefault()
}

const (
	// DefaultGrafanaClientTimeout is the default timeout for Grafana HTTP client requests.
	DefaultGrafanaClientTimeout = 10 * time.Second
)

// WithGrafanaConfig adds Grafana configuration to the context.
// This configuration includes API credentials, debug settings, and TLS options that will be used by all Grafana clients created from this context.
func WithGrafanaConfig(ctx context.Context, config GrafanaConfig) context.Context {
	config.URL = normalizeGrafanaURL(config.URL)
	return context.WithValue(ctx, grafanaConfigKey{}, config)
}

// normalizeGrafanaURL cleans up a configured Grafana URL so that requests built
// from it are well-formed and don't trip avoidable redirects. It trims
// surrounding whitespace and trailing slashes, and supplies a scheme when none
// is provided — a schemeless value like "grafana.example.com" would otherwise
// be parsed as a relative path and produce broken request URLs. Schemeless
// local addresses default to http (local Grafana rarely serves TLS); everything
// else defaults to https. An empty input is returned unchanged.
func normalizeGrafanaURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if u == "" {
		return ""
	}
	if !hasScheme(u) {
		if isLocalHostPort(u) {
			u = "http://" + u
		} else {
			u = "https://" + u
		}
	}
	return u
}

// hasScheme reports whether u begins with a URL scheme (e.g. "https://"). It
// looks for "://" in scheme position rather than anywhere in the string, so a
// schemeless URL whose path or query happens to contain "://" (e.g. a query
// parameter holding another URL) is still recognised as needing a scheme.
func hasScheme(u string) bool {
	i := strings.Index(u, "://")
	if i <= 0 {
		return false
	}
	// Anything path-, query-, or fragment-like before the "://" means it isn't
	// a real scheme separator.
	return !strings.ContainsAny(u[:i], "/?#")
}

// isLocalHostPort reports whether a schemeless URL points at a local address,
// e.g. "localhost:3000", "127.0.0.1", or "[::1]:3000".
func isLocalHostPort(hostPort string) bool {
	// Drop any path/query so only the host[:port] is inspected.
	if i := strings.IndexAny(hostPort, "/?"); i >= 0 {
		hostPort = hostPort[:i]
	}
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	return host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		host == "127.0.0.1" || host == "::1"
}

// GrafanaConfigFromContext extracts Grafana configuration from the context.
// If no config is found, returns a zero-value GrafanaConfig. This function is typically used by internal components to access configuration set earlier in the request lifecycle.
func GrafanaConfigFromContext(ctx context.Context) GrafanaConfig {
	if config, ok := ctx.Value(grafanaConfigKey{}).(GrafanaConfig); ok {
		return config
	}
	return GrafanaConfig{}
}

// CreateTLSConfig creates a *tls.Config from TLSConfig.
// It supports client certificates, custom CA certificates, and the option to skip TLS verification for development environments.
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
// It clones the provided transport and applies the TLS settings, preserving other transport configurations like timeouts and connection pools.
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

// UserAgentTransport wraps an http.RoundTripper to add a custom User-Agent header.
// This ensures all HTTP requests from the MCP server are properly identified with version information for debugging and analytics.
type UserAgentTransport struct {
	rt        http.RoundTripper
	UserAgent string
}

func (t *UserAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	clonedReq := req.Clone(req.Context())

	// Add or update the User-Agent header
	if clonedReq.Header.Get("User-Agent") == "" {
		clonedReq.Header.Set("User-Agent", t.UserAgent)
	}

	return t.rt.RoundTrip(clonedReq)
}

// version is set at build time via ldflags:
//
//	-X github.com/grafana/mcp-grafana.version=v1.2.3
var version string

// Version returns the version of the mcp-grafana binary.
// It prefers an ldflags-injected value, then falls back to runtime/debug build info,
// and finally returns "(devel)" for local development builds.
var Version = sync.OnceValue(func() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
})

// UserAgent returns the user agent string for HTTP requests.
// It includes the mcp-grafana identifier and version number for proper request attribution and debugging.
func UserAgent() string {
	return fmt.Sprintf("mcp-grafana/%s", Version())
}

// NewUserAgentTransport creates a new UserAgentTransport with the specified user agent.
// If no user agent is provided, it uses the default UserAgent() with version information.
// The transport wraps the provided RoundTripper, defaulting to http.DefaultTransport if nil.
func NewUserAgentTransport(rt http.RoundTripper, userAgent ...string) *UserAgentTransport {
	if rt == nil {
		rt = http.DefaultTransport
	}

	ua := UserAgent() // default
	if len(userAgent) > 0 {
		ua = userAgent[0]
	}

	return &UserAgentTransport{
		rt:        rt,
		UserAgent: ua,
	}
}

// OrgIDRoundTripper wraps an http.RoundTripper to add the X-Grafana-Org-Id header.
type OrgIDRoundTripper struct {
	underlying http.RoundTripper
	orgID      int64
}

func (t *OrgIDRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clonedReq := req.Clone(req.Context())

	orgID := t.orgID
	if cfg := GrafanaConfigFromContext(req.Context()); cfg.OrgID > 0 {
		orgID = cfg.OrgID
	}
	if orgID > 0 {
		clonedReq.Header.Set(client.OrgIDHeader, strconv.FormatInt(orgID, 10))
	}

	return t.underlying.RoundTrip(clonedReq)
}

func NewOrgIDRoundTripper(rt http.RoundTripper, orgID int64) *OrgIDRoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}

	return &OrgIDRoundTripper{
		underlying: rt,
		orgID:      orgID,
	}
}

type ExtraHeadersRoundTripper struct {
	underlying http.RoundTripper
	headers    map[string]string
}

func (t *ExtraHeadersRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clonedReq := req.Clone(req.Context())
	headers := t.headers
	if cfg := GrafanaConfigFromContext(req.Context()); len(cfg.ExtraHeaders) > 0 {
		headers = mergeHeaders(t.headers, cfg.ExtraHeaders)
	}
	for k, v := range headers {
		clonedReq.Header.Set(k, v)
	}
	return t.underlying.RoundTrip(clonedReq)
}

func NewExtraHeadersRoundTripper(rt http.RoundTripper, headers map[string]string) *ExtraHeadersRoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &ExtraHeadersRoundTripper{
		underlying: rt,
		headers:    headers,
	}
}

// AuthRoundTripper wraps an http.RoundTripper to add authentication headers.
// It supports on-behalf-of (OBO) auth via access/ID tokens, API key bearer
// auth, and HTTP basic auth, in that priority order.
type AuthRoundTripper struct {
	accessToken string
	idToken     string
	apiKey      string
	basicAuth   *url.Userinfo
	underlying  http.RoundTripper
}

func (rt *AuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clonedReq := req.Clone(req.Context())

	accessToken, idToken, apiKey, basicAuth := rt.accessToken, rt.idToken, rt.apiKey, rt.basicAuth
	cfg := GrafanaConfigFromContext(req.Context())
	if cfg.AccessToken != "" {
		accessToken = cfg.AccessToken
	}
	if cfg.IDToken != "" {
		idToken = cfg.IDToken
	}
	if cfg.APIKey != "" {
		apiKey = cfg.APIKey
	}
	if cfg.BasicAuth != nil {
		basicAuth = cfg.BasicAuth
	}

	if accessToken != "" && idToken != "" {
		clonedReq.Header.Set("X-Access-Token", accessToken)
		clonedReq.Header.Set("X-Grafana-Id", idToken)
	} else if apiKey != "" {
		clonedReq.Header.Set("Authorization", "Bearer "+apiKey)
	} else if basicAuth != nil {
		password, _ := basicAuth.Password()
		clonedReq.SetBasicAuth(basicAuth.Username(), password)
	}

	return rt.underlying.RoundTrip(clonedReq)
}

func NewAuthRoundTripper(rt http.RoundTripper, accessToken, idToken, apiKey string, basicAuth *url.Userinfo) *AuthRoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &AuthRoundTripper{
		accessToken: accessToken,
		idToken:     idToken,
		apiKey:      apiKey,
		basicAuth:   basicAuth,
		underlying:  rt,
	}
}

// sensitiveHeaders lists HTTP header names whose values must be redacted in
// debug logs to prevent credential leakage (see #919).
var sensitiveHeaders = map[string]bool{
	"Authorization":  true,
	"X-Access-Token": true,
	"X-Grafana-Id":   true,
	"Cookie":         true,
}

// redactHeaderValue masks the middle portion of a credential value,
// preserving the first 4 and last 4 characters for identification.
// Values shorter than 12 characters are fully replaced.
func redactHeaderValue(v string) string {
	if len(v) < 12 {
		return "[REDACTED]"
	}
	return v[:4] + "***" + v[len(v)-4:]
}

// debugLoggingRoundTripper logs HTTP requests and responses with sensitive
// headers redacted. It replaces the go-openapi Debug flag, which uses
// httputil.DumpRequestOut and exposes credentials in plaintext.
type debugLoggingRoundTripper struct {
	underlying http.RoundTripper
	logger     *slog.Logger
}

func (rt *debugLoggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	redacted := req.Clone(req.Context())
	redacted.Body = nil
	for name := range sensitiveHeaders {
		if v := redacted.Header.Get(name); v != "" {
			redacted.Header.Set(name, redactHeaderValue(v))
		}
	}
	if dump, err := httputil.DumpRequestOut(redacted, false); err == nil {
		rt.logger.Debug(string(dump))
	}

	resp, err := rt.underlying.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	if dump, dumpErr := httputil.DumpResponse(resp, false); dumpErr == nil {
		rt.logger.Debug(string(dump))
	}
	return resp, nil
}

// transportOptions controls which middleware layers BuildTransport includes.
type transportOptions struct {
	withoutAuth      bool
	withoutOrgID     bool
	withoutOtel      bool
	withoutUserAgent bool
}

// TransportOption configures optional behaviour of BuildTransport.
type TransportOption func(*transportOptions)

// WithoutAuth skips the authentication middleware layer.
// Use this when the HTTP client library handles auth itself (e.g. OnCall, incident).
func WithoutAuth() TransportOption {
	return func(o *transportOptions) { o.withoutAuth = true }
}

// WithoutOrgID skips the X-Grafana-Org-Id header layer.
func WithoutOrgID() TransportOption {
	return func(o *transportOptions) { o.withoutOrgID = true }
}

// WithoutOtel skips the otelhttp tracing wrapper.
func WithoutOtel() TransportOption {
	return func(o *transportOptions) { o.withoutOtel = true }
}

// WithoutUserAgent skips the User-Agent header layer.
func WithoutUserAgent() TransportOption {
	return func(o *transportOptions) { o.withoutUserAgent = true }
}

// BuildTransport constructs an http.RoundTripper with the standard middleware
// chain derived from cfg. The default chain (innermost to outermost) is:
//
//	base → TLS → debugLogging → Auth → ExtraHeaders → OrgID → UserAgent → otelhttp
//
// Auth is innermost among the header-setting layers so that credentials take
// precedence over any forwarded/extra headers with the same keys.
//
// When cfg.Debug is true a debug-logging layer is added just above the base
// transport. It sees the fully-decorated request (all headers set by outer
// layers) and redacts sensitive values (Authorization, X-Access-Token, etc.)
// before writing request/response details to the logger.
//
// Individual layers can be disabled with WithoutAuth, WithoutOrgID, etc.
func BuildTransport(cfg *GrafanaConfig, base http.RoundTripper, opts ...TransportOption) (http.RoundTripper, error) {
	var options transportOptions
	for _, o := range opts {
		o(&options)
	}

	if base == nil {
		base = cfg.HTTPTransport()
	}
	transport := base

	// TLS
	if cfg.TLSConfig != nil {
		t, ok := base.(*http.Transport)
		if !ok {
			t = http.DefaultTransport.(*http.Transport).Clone()
		}
		var err error
		transport, err = cfg.TLSConfig.HTTPTransport(t)
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS transport: %w", err)
		}
	}

	// Debug logging with redacted credentials (innermost among the
	// non-TLS layers so it sees the final request with all headers).
	if cfg.Debug {
		transport = &debugLoggingRoundTripper{
			underlying: transport,
			logger:     cfg.LoggerOrDefault(),
		}
	}

	// Auth (innermost header layer — wins on conflicts with ExtraHeaders)
	if !options.withoutAuth {
		transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	}

	// Extra headers (always included so per-request context overrides work)
	transport = NewExtraHeadersRoundTripper(transport, cfg.ExtraHeaders)

	// Org ID (always included so per-request context overrides work)
	if !options.withoutOrgID {
		transport = NewOrgIDRoundTripper(transport, cfg.OrgID)
	}

	// User-Agent
	if !options.withoutUserAgent {
		transport = NewUserAgentTransport(transport)
	}

	// OpenTelemetry HTTP tracing (outermost)
	if !options.withoutOtel {
		transport = otelhttp.NewTransport(transport)
	}

	return transport, nil
}

// Gets info from environment
func extractKeyGrafanaInfoFromEnv(logger *slog.Logger) (url, apiKey string, auth *url.Userinfo, orgId int64) {
	url, apiKey = urlAndAPIKeyFromEnv(logger)
	if url == "" {
		url = defaultGrafanaURL
	}
	auth = userAndPassFromEnv()
	orgId = orgIdFromEnv(logger)
	return
}

// Tries to get grafana info from a request.
// Gets info from environment if it can't get it from request.
//
// envCredsAllowed reports whether environment-configured credentials were
// eligible to be used for this request. Environment credentials are bound to the
// environment-configured Grafana URL: if a request supplies an X-Grafana-URL
// header pointing at a different instance, environment credentials are withheld
// so that a caller-controlled URL cannot receive them.
func extractKeyGrafanaInfoFromReq(req *http.Request, logger *slog.Logger) (grafanaUrl, apiKey string, auth *url.Userinfo, orgId int64, envCredsAllowed bool) {
	eUrl, eApiKey, eAuth, eOrgId := extractKeyGrafanaInfoFromEnv(logger)
	username, password, _ := req.BasicAuth()

	grafanaUrl, apiKey = urlAndAPIKeyFromHeaders(req)
	envCredsAllowed = requestTargetsEnvURL(grafanaUrl, eUrl)

	// If no URL was supplied in the request, use the environment URL.
	if grafanaUrl == "" {
		grafanaUrl = eUrl
	}

	// Fall back to the environment token only when it is bound to the target URL.
	if apiKey == "" && envCredsAllowed {
		apiKey = eApiKey
	}

	// Use request basic auth if supplied; otherwise fall back to the environment
	// credentials only when they are bound to the target URL.
	if username == "" && password == "" {
		if envCredsAllowed {
			auth = eAuth
		}
	} else {
		auth = url.UserPassword(username, password)
	}

	// extract org ID from header, fall back to environment.
	// The org ID is not a secret, so it is not gated on the target URL.
	orgId = orgIdFromHeaders(req, logger)
	if orgId == 0 {
		orgId = eOrgId
	}

	return
}

// requestTargetsEnvURL reports whether a request's X-Grafana-URL points at the
// same Grafana instance as the environment configuration, meaning it is safe to
// attach environment-configured credentials. An empty requestURL means the
// caller did not override the URL, so the environment URL (and its credentials)
// apply. A non-empty requestURL only matches when it normalizes to the same
// value as the environment URL; anything else is treated as a foreign target and
// environment credentials are withheld.
func requestTargetsEnvURL(requestURL, envURL string) bool {
	if requestURL == "" {
		return true
	}
	return normalizeGrafanaURL(requestURL) == normalizeGrafanaURL(envURL)
}

// ExtractGrafanaInfoFromEnv is a StdioContextFunc that extracts Grafana configuration from environment variables.
// It reads GRAFANA_URL and GRAFANA_SERVICE_ACCOUNT_TOKEN (or deprecated GRAFANA_API_KEY) environment variables and adds the configuration to the context for use by Grafana clients.
var ExtractGrafanaInfoFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	// Get existing config or create a new one.
	// This will respect the existing debug flag, if set.
	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()

	u, apiKey, basicAuth, orgID := extractKeyGrafanaInfoFromEnv(logger)
	parsedURL, err := url.Parse(u)
	if err != nil {
		panic(fmt.Errorf("invalid Grafana URL %s: %w", u, err))
	}

	extraHeaders := extraHeadersFromEnv(logger)

	logger.Info("Using Grafana configuration", "url", parsedURL.Redacted(), "api_key_set", apiKey != "", "basic_auth_set", basicAuth != nil, "org_id", orgID, "extra_headers_count", len(extraHeaders))
	config.URL = u
	config.APIKey = apiKey
	config.BasicAuth = basicAuth
	config.OrgID = orgID
	config.ExtraHeaders = extraHeaders
	return WithGrafanaConfig(ctx, config)
}

// httpContextFunc is a function that can be used as a `server.HTTPContextFunc` or a
// `server.SSEContextFunc`. It is necessary because, while the two types are functionally
// identical, they have distinct types and cannot be passed around interchangeably.
type httpContextFunc func(ctx context.Context, req *http.Request) context.Context

// ExtractGrafanaInfoFromHeaders is a HTTPContextFunc that extracts Grafana configuration from HTTP request headers.
// It reads X-Grafana-URL and X-Grafana-API-Key headers, falling back to environment variables if headers are not present.
// Headers listed in GRAFANA_FORWARD_HEADERS are copied from the incoming request and merged with GRAFANA_EXTRA_HEADERS.
var ExtractGrafanaInfoFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	// Get existing config or create a new one.
	// This will respect the existing debug flag, if set.
	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()

	u, apiKey, basicAuth, orgID, envCredsAllowed := extractKeyGrafanaInfoFromReq(req, logger)

	config.URL = u
	config.APIKey = apiKey
	config.BasicAuth = basicAuth
	config.OrgID = orgID

	// Environment extra headers may carry credentials (e.g. an Authorization
	// header), so they are bound to the environment-configured URL just like the
	// service-account token. When a request targets a foreign URL, only headers
	// the operator explicitly opted to forward (GRAFANA_FORWARD_HEADERS) are sent.
	var envHeaders map[string]string
	if envCredsAllowed {
		envHeaders = extraHeadersFromEnv(logger)
	}
	config.ExtraHeaders = mergeHeaders(envHeaders, forwardedHeadersFromRequest(req))
	return WithGrafanaConfig(ctx, config)
}

// WithOnBehalfOfAuth adds the Grafana access token and user token to the Grafana config.
// These tokens enable on-behalf-of authentication in Grafana Cloud, allowing the MCP server to act on behalf of a specific user with their permissions.
func WithOnBehalfOfAuth(ctx context.Context, accessToken, userToken string) (context.Context, error) {
	if accessToken == "" || userToken == "" {
		return nil, fmt.Errorf("neither accessToken nor userToken can be empty")
	}
	cfg := GrafanaConfigFromContext(ctx)
	cfg.AccessToken = accessToken
	cfg.IDToken = userToken
	return WithGrafanaConfig(ctx, cfg), nil
}

// MustWithOnBehalfOfAuth adds the access and user tokens to the context, panicking if either are empty.
// This is a convenience wrapper around WithOnBehalfOfAuth for cases where token validation has already occurred.
func MustWithOnBehalfOfAuth(ctx context.Context, accessToken, userToken string) context.Context {
	ctx, err := WithOnBehalfOfAuth(ctx, accessToken, userToken)
	if err != nil {
		panic(err)
	}
	return ctx
}

type grafanaClientKey struct{}

// GrafanaClient wraps the Grafana HTTP API client with additional metadata
// fetched from the Grafana instance, such as the public URL.
// This allows the MCP server to generate user-facing links using the public URL
// even when it accesses Grafana via an internal URL.
type GrafanaClient struct {
	*client.GrafanaHTTPAPI

	// PublicURL is the public-facing URL of the Grafana instance, fetched from
	// /api/frontend/settings (the appUrl field). It may differ from the configured
	// URL when the MCP server accesses Grafana via an internal URL behind a load
	// balancer or reverse proxy.
	PublicURL string
}

func makeBasePath(path string) string {
	return strings.Join([]string{strings.TrimRight(path, "/"), "api"}, "/")
}

// publicURLCache caches successfully fetched public URLs per Grafana URL.
// Only non-empty (successful) results are cached; failures are retried on
// subsequent calls so that transient errors at startup don't permanently
// disable the feature.
var publicURLCache sync.Map // map[string]string (grafanaURL -> publicURL)

// publicURLFlight deduplicates concurrent fetchPublicURL calls for the same
// Grafana URL, preventing thundering-herd HTTP requests and race conditions
// where a failing goroutine could overwrite a successful result.
var publicURLFlight singleflight.Group

// fetchPublicURL fetches the public URL (appUrl) from Grafana's frontend settings API.
// It returns the appUrl if available, or an empty string if the request fails.
// Successful results are cached permanently; failures are retried on subsequent calls.
// Concurrent calls for the same grafanaURL are coalesced via singleflight.
func fetchPublicURL(ctx context.Context, cfg *GrafanaConfig) string {
	// Check cache first (only successful results are cached)
	if cached, ok := publicURLCache.Load(cfg.URL); ok {
		return cached.(string)
	}

	// Use singleflight to coalesce concurrent requests for the same URL
	result, _, _ := publicURLFlight.Do(cfg.URL, func() (any, error) {
		// Double-check cache inside singleflight (another goroutine may have populated it)
		if cached, ok := publicURLCache.Load(cfg.URL); ok {
			return cached.(string), nil
		}

		// Use a detached context with timeout so that a cancelled request
		// context from the first caller doesn't fail the fetch for all waiters.
		fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		publicURL := doFetchPublicURL(fetchCtx, cfg)

		// Only cache successful (non-empty) results so transient failures are retried
		if publicURL != "" {
			publicURLCache.Store(cfg.URL, publicURL)
		}

		return publicURL, nil
	})

	return result.(string)
}

// frontendSettings holds the subset of /api/frontend/settings that the MCP
// server cares about.
type frontendSettings struct {
	// AppURL is the public-facing URL (appUrl) of the Grafana instance.
	AppURL string
	// Namespace is the Kubernetes-style namespace for the requesting org,
	// computed server-side by Grafana's namespacer (e.g. "default", "org-2",
	// or "stacks-123" on Grafana Cloud). Empty if not reported.
	Namespace string
}

// doFetchPublicURL performs the actual HTTP request to fetch the public URL.
func doFetchPublicURL(ctx context.Context, cfg *GrafanaConfig) string {
	return doFetchFrontendSettings(ctx, cfg).AppURL
}

// doFetchFrontendSettings performs the actual HTTP request to fetch the
// Grafana frontend settings, returning the fields the MCP server uses.
// On any error it returns a zero-value frontendSettings.
func doFetchFrontendSettings(ctx context.Context, cfg *GrafanaConfig) frontendSettings {
	logger := cfg.LoggerOrDefault()
	settingsURL := cfg.URL + "/api/frontend/settings"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, settingsURL, nil)
	if err != nil {
		logger.Warn("Failed to create request for frontend settings", "error", err)
		return frontendSettings{}
	}

	transport, err := BuildTransport(cfg, nil)
	if err != nil {
		logger.Warn("Failed to build transport for frontend settings request", "error", err)
		return frontendSettings{}
	}

	httpClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Warn("Failed to fetch frontend settings", "error", err)
		return frontendSettings{}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		logger.Warn("Frontend settings request returned non-OK status", "status", resp.StatusCode)
		return frontendSettings{}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Warn("Failed to read frontend settings response", "error", err)
		return frontendSettings{}
	}

	var settings struct {
		AppURL    string `json:"appUrl"`
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		logger.Warn("Failed to parse frontend settings response", "error", err)
		return frontendSettings{}
	}

	publicURL := strings.TrimRight(settings.AppURL, "/")
	if publicURL != "" {
		logger.Info("Fetched public URL from Grafana frontend settings", "public_url", publicURL)
	}
	return frontendSettings{AppURL: publicURL, Namespace: settings.Namespace}
}

// namespaceCache caches the Kubernetes-style namespace per (Grafana URL, OrgID).
// Unlike the public URL, the namespace depends on the org, so the cache key
// includes the OrgID. Only non-empty results from frontend settings are cached;
// the OrgID-derived fallback is cheap and recomputed on each miss.
var namespaceCache sync.Map // map[string]string ("URL|orgID" -> namespace)

// namespaceFlight deduplicates concurrent frontend-settings fetches for the
// same (URL, OrgID).
var namespaceFlight singleflight.Group

// orgNamespace derives a Kubernetes-style namespace from an org ID, matching
// Grafana authlib's OrgNamespaceFormatter (org 1 / unset maps to "default").
func orgNamespace(orgID int64) string {
	if orgID <= 1 {
		return "default"
	}
	return fmt.Sprintf("org-%d", orgID)
}

// nsResult is the resolved namespace plus whether it came from frontend
// settings (true) or the OrgID-derived fallback (false).
type nsResult struct {
	namespace    string
	fromSettings bool
}

// DashboardNamespace returns the Kubernetes-style namespace to use for
// dashboard.grafana.app API calls, given the Grafana config in ctx, and whether
// it was resolved from Grafana's /api/frontend/settings (fromSettings=true) or
// fell back to the OrgID-derived value (fromSettings=false).
//
// It prefers the namespace reported by /api/frontend/settings, which is correct
// for both single-tenant ("default" / "org-N") and Grafana Cloud ("stacks-{id}"),
// caching successful results per (URL, OrgID). If the settings endpoint is
// unavailable or omits the namespace, it falls back to deriving the namespace
// from the OrgID — which is correct on-prem but may be wrong on Grafana Cloud,
// so callers can use fromSettings to qualify a subsequent not-found.
func DashboardNamespace(ctx context.Context) (namespace string, fromSettings bool) {
	cfg := GrafanaConfigFromContext(ctx)
	fallback := orgNamespace(cfg.OrgID)

	key := fmt.Sprintf("%s|%d", cfg.URL, cfg.OrgID)
	if cached, ok := namespaceCache.Load(key); ok {
		return cached.(string), true
	}

	result, _, _ := namespaceFlight.Do(key, func() (any, error) {
		// Double-check cache inside singleflight.
		if cached, ok := namespaceCache.Load(key); ok {
			return nsResult{cached.(string), true}, nil
		}

		// Detached context with timeout so a cancelled caller doesn't fail the
		// fetch for all waiters; re-inject the GrafanaConfig so the request
		// carries the right auth and Org-ID header.
		fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		fetchCtx = WithGrafanaConfig(fetchCtx, cfg)

		ns := doFetchFrontendSettings(fetchCtx, &cfg).Namespace
		if ns != "" {
			namespaceCache.Store(key, ns)
			return nsResult{ns, true}, nil
		}
		// Don't cache the fallback, so a transient settings failure is retried.
		return nsResult{fallback, false}, nil
	})

	r := result.(nsResult)
	return r.namespace, r.fromSettings
}

// NewGrafanaClient creates a Grafana client with the provided URL and API key.
// The client is automatically configured with the correct HTTP scheme, debug settings from context, custom TLS configuration if present, and OpenTelemetry instrumentation for distributed tracing.
// It also fetches the Grafana instance's public URL from /api/frontend/settings for use in deep link generation.
// The org ID is read from the GrafanaConfig in the context, which should be set by ExtractGrafanaInfoFromEnv or ExtractGrafanaInfoFromHeaders before calling this function.
func NewGrafanaClient(ctx context.Context, grafanaURL, apiKey string, auth *url.Userinfo) *GrafanaClient {
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

	if auth != nil {
		cfg.BasicAuth = auth
	}

	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()
	// NOTE: we intentionally do NOT set cfg.Debug here. The go-openapi
	// runtime's Debug mode uses httputil.DumpRequestOut which prints
	// credentials in plaintext. Instead, BuildTransport adds a redacting
	// debug-logging layer when config.Debug is true (see #919).

	if config.OrgID > 0 {
		cfg.OrgID = config.OrgID
	}

	// Configure TLS if custom TLS configuration is provided
	if tlsConfig := config.TLSConfig; tlsConfig != nil {
		tlsCfg, err := tlsConfig.CreateTLSConfig()
		if err != nil {
			panic(fmt.Errorf("failed to create TLS config: %w", err))
		}
		cfg.TLSConfig = tlsCfg
		logger.Debug("Using custom TLS configuration",
			"cert_file", tlsConfig.CertFile,
			"ca_file", tlsConfig.CAFile,
			"skip_verify", tlsConfig.SkipVerify)
	}

	// Determine timeout - use config value if set, otherwise use default
	timeout := config.Timeout
	if timeout == 0 {
		timeout = DefaultGrafanaClientTimeout
	}

	logger.Debug("Creating Grafana client", "url", parsedURL.Redacted(), "api_key_set", apiKey != "", "basic_auth_set", config.BasicAuth != nil, "org_id", cfg.OrgID, "timeout", timeout, "extra_headers_count", len(config.ExtraHeaders))
	grafanaClient := client.NewHTTPClientWithConfig(strfmt.Default, cfg)

	// Some Grafana versions (v12+) and reverse proxies return JSON responses
	// with text/plain or text/html content-type headers. The default
	// TextConsumer cannot deserialize these into typed Go structs. Override
	// with JSONConsumer so the client can parse the response body correctly.
	// See: https://github.com/grafana/mcp-grafana/issues/635
	if rt, ok := grafanaClient.Transport.(*openapiclient.Runtime); ok {
		jsonConsumer := runtime.JSONConsumer()
		rt.Consumers[runtime.TextMime] = jsonConsumer
		rt.Consumers[runtime.HTMLMime] = jsonConsumer
	}

	// Replace the OpenAPI client's transport with one built by BuildTransport
	// so we get OTel tracing, user-agent, org-ID, and extra headers for free.
	// The OpenAPI client handles APIKey/BasicAuth itself, so we skip transport-
	// level auth and only inject OBO tokens (which the OpenAPI client doesn't
	// know about) via the AuthRoundTripper.
	v := reflect.ValueOf(grafanaClient.Transport)
	if v.Kind() == reflect.Ptr && !v.IsNil() {
		v = v.Elem()
		if v.Kind() == reflect.Struct {
			transportField := v.FieldByName("Transport")
			if transportField.IsValid() && transportField.CanSet() {
				if _, ok := transportField.Interface().(http.RoundTripper); ok {
					var base http.RoundTripper
					if config.BaseTransport != nil {
						base = config.BaseTransport
					} else {
						base = &http.Transport{
							Proxy: http.ProxyFromEnvironment,
							DialContext: (&net.Dialer{
								Timeout:   timeout,
								KeepAlive: 30 * time.Second,
							}).DialContext,
							TLSHandshakeTimeout:   timeout,
							ResponseHeaderTimeout: timeout,
							ExpectContinueTimeout: 1 * time.Second,
							ForceAttemptHTTP2:     true,
							MaxIdleConns:          100,
							IdleConnTimeout:       90 * time.Second,
						}
					}
					// Use BuildTransport but skip APIKey/BasicAuth auth
					// (handled by the OpenAPI client). OBO tokens still need
					// transport-level injection since the OpenAPI client
					// doesn't support them natively.
					oboConfig := GrafanaConfig{
						AccessToken:  config.AccessToken,
						IDToken:      config.IDToken,
						OrgID:        config.OrgID,
						TLSConfig:    config.TLSConfig,
						ExtraHeaders: config.ExtraHeaders,
						Debug:        config.Debug,
						Logger:       config.Logger,
					}
					// Panic matches the existing TLS error handling above
					// (line ~887). The only realistic failure is a TLS
					// misconfiguration, which can't happen here since base
					// is always an *http.Transport.
					wrapped, err := BuildTransport(&oboConfig, base)
					if err != nil {
						panic(fmt.Errorf("failed to build transport: %w", err))
					}
					transportField.Set(reflect.ValueOf(wrapped))
					logger.Debug("HTTP tracing, user agent tracking, and timeout enabled for Grafana client", "timeout", timeout)
				}
			}
		}
	}

	// Fetch the public URL from Grafana's frontend settings.
	fetchCfg := &GrafanaConfig{
		URL:          grafanaURL,
		APIKey:       apiKey,
		BasicAuth:    auth,
		AccessToken:  config.AccessToken,
		IDToken:      config.IDToken,
		TLSConfig:    config.TLSConfig,
		ExtraHeaders: config.ExtraHeaders,
		Logger:       config.Logger,
	}
	publicURL := fetchPublicURL(ctx, fetchCfg)

	return &GrafanaClient{
		GrafanaHTTPAPI: grafanaClient,
		PublicURL:      publicURL,
	}
}

// ExtractGrafanaClientFromEnv is a StdioContextFunc that creates and injects a Grafana client into the context.
// It uses configuration from GRAFANA_URL, GRAFANA_SERVICE_ACCOUNT_TOKEN (or deprecated GRAFANA_API_KEY), GRAFANA_USERNAME/PASSWORD environment variables to initialize
// the client with proper authentication.
var ExtractGrafanaClientFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	// Extract transport config from env vars
	logger := LoggerFromContext(ctx)
	grafanaURL, apiKey := urlAndAPIKeyFromEnv(logger)
	if grafanaURL == "" {
		grafanaURL = defaultGrafanaURL
	}
	auth := userAndPassFromEnv()
	grafanaClient := NewGrafanaClient(ctx, grafanaURL, apiKey, auth)
	return WithGrafanaClient(ctx, grafanaClient)
}

// ExtractGrafanaClientFromHeaders is a HTTPContextFunc that creates and injects a Grafana client into the context.
// It prioritizes configuration from HTTP headers (X-Grafana-URL, X-Grafana-API-Key) over environment variables for multi-tenant scenarios.
var ExtractGrafanaClientFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()
	if config.OrgID == 0 {
		logger.Warn("No org ID found in request headers or environment variables, using default org. Set GRAFANA_ORG_ID or pass X-Grafana-Org-Id header to target a specific org.")
	}

	// Extract transport config from request headers, and set it on the context.
	u, apiKey, basicAuth, _, _ := extractKeyGrafanaInfoFromReq(req, logger)
	logger.Debug("Creating Grafana client", "url", u, "api_key_set", apiKey != "", "basic_auth_set", basicAuth != nil)

	grafanaClient := NewGrafanaClient(ctx, u, apiKey, basicAuth)
	return WithGrafanaClient(ctx, grafanaClient)
}

// WithGrafanaClient sets the Grafana client in the context.
// The client can be retrieved using GrafanaClientFromContext and will be used by all Grafana-related tools in the MCP server.
func WithGrafanaClient(ctx context.Context, c *GrafanaClient) context.Context {
	return context.WithValue(ctx, grafanaClientKey{}, c)
}

// GrafanaClientFromContext retrieves the Grafana client from the context.
// Returns nil if no client has been set, which tools should handle gracefully with appropriate error messages.
func GrafanaClientFromContext(ctx context.Context) *GrafanaClient {
	c, ok := ctx.Value(grafanaClientKey{}).(*GrafanaClient)
	if !ok {
		return nil
	}
	return c
}

type kubernetesClientKey struct{}

// ExtractKubernetesClientFromEnv is a StdioContextFunc that creates and injects a
// Kubernetes-style API client into the context, used by tools that talk to
// Grafana's app-platform APIs (e.g. dashboard.grafana.app). On failure it injects
// a nil client; callers fall back to the legacy API.
var ExtractKubernetesClientFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	logger := LoggerFromContext(ctx)
	client, err := NewKubernetesClient(ctx)
	if err != nil {
		logger.Warn("Failed to create Kubernetes client; k8s APIs will be unavailable", "error", err)
		return WithKubernetesClient(ctx, nil)
	}
	return WithKubernetesClient(ctx, client)
}

// ExtractKubernetesClientFromHeaders is a HTTPContextFunc that creates and injects a
// Kubernetes-style API client into the context for HTTP/SSE transports.
var ExtractKubernetesClientFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()
	client, err := NewKubernetesClient(ctx)
	if err != nil {
		logger.Warn("Failed to create Kubernetes client; k8s APIs will be unavailable", "error", err)
		return WithKubernetesClient(ctx, nil)
	}
	return WithKubernetesClient(ctx, client)
}

// WithKubernetesClient sets the Kubernetes-style API client in the context.
func WithKubernetesClient(ctx context.Context, c *KubernetesClient) context.Context {
	return context.WithValue(ctx, kubernetesClientKey{}, c)
}

// KubernetesClientFromContext retrieves the Kubernetes-style API client from the
// context. Returns nil if no client has been set (or creation failed); callers
// should handle nil by falling back to the legacy Grafana API.
func KubernetesClientFromContext(ctx context.Context) *KubernetesClient {
	c, ok := ctx.Value(kubernetesClientKey{}).(*KubernetesClient)
	if !ok {
		return nil
	}
	return c
}

type incidentClientKey struct{}

// ExtractIncidentClientFromEnv is a StdioContextFunc that creates and injects a Grafana Incident client into the context.
// It configures the client using environment variables and applies any custom TLS settings from the context.
var ExtractIncidentClientFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()
	grafanaURL, apiKey := urlAndAPIKeyFromEnv(logger)
	if grafanaURL == "" {
		grafanaURL = defaultGrafanaURL
	}
	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/resources/api/v1/", grafanaURL)
	parsedURL, err := url.Parse(incidentURL)
	if err != nil {
		panic(fmt.Errorf("invalid incident URL %s: %w", incidentURL, err))
	}
	logger.Debug("Creating Incident client", "url", parsedURL.Redacted(), "api_key_set", apiKey != "")
	client := incident.NewClient(incidentURL, apiKey)

	transport, err := BuildTransport(&config, nil, WithoutAuth())
	if err != nil {
		logger.Error("Failed to create custom transport for incident client, using default", "error", err)
	} else {
		client.HTTPClient.Transport = transport
	}

	return context.WithValue(ctx, incidentClientKey{}, client)
}

// ExtractIncidentClientFromHeaders is a HTTPContextFunc that creates and injects a Grafana Incident client into the context.
// It uses HTTP headers for configuration with environment variable fallbacks, enabling per-request incident management configuration.
var ExtractIncidentClientFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()
	grafanaURL, apiKey, _, orgID, _ := extractKeyGrafanaInfoFromReq(req, logger)
	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/resources/api/v1/", grafanaURL)
	client := incident.NewClient(incidentURL, apiKey)

	// Use orgID from the request headers rather than config, since
	// the incident client may be created with a different org context.
	config.OrgID = orgID
	transport, err := BuildTransport(&config, nil, WithoutAuth())
	if err != nil {
		logger.Error("Failed to create custom transport for incident client, using default", "error", err)
	} else {
		client.HTTPClient.Transport = transport
	}

	return context.WithValue(ctx, incidentClientKey{}, client)
}

// WithIncidentClient sets the Grafana Incident client in the context.
// This client is used for managing incidents, activities, and other IRM (Incident Response Management) operations.
func WithIncidentClient(ctx context.Context, client *incident.Client) context.Context {
	return context.WithValue(ctx, incidentClientKey{}, client)
}

// IncidentClientFromContext retrieves the Grafana Incident client from the context.
// Returns nil if no client has been set, indicating that incident management features are not available.
func IncidentClientFromContext(ctx context.Context) *incident.Client {
	c, ok := ctx.Value(incidentClientKey{}).(*incident.Client)
	if !ok {
		return nil
	}
	return c
}

// ComposeStdioContextFuncs composes multiple StdioContextFuncs into a single one.
// Functions are applied in order, allowing each to modify the context before passing it to the next.
func ComposeStdioContextFuncs(funcs ...server.StdioContextFunc) server.StdioContextFunc {
	return func(ctx context.Context) context.Context {
		for _, f := range funcs {
			ctx = f(ctx)
		}
		return ctx
	}
}

// ComposeSSEContextFuncs composes multiple SSEContextFuncs into a single one.
// This enables chaining of context modifications for Server-Sent Events transport, such as extracting headers and setting up clients.
func ComposeSSEContextFuncs(funcs ...httpContextFunc) server.SSEContextFunc {
	return func(ctx context.Context, req *http.Request) context.Context {
		for _, f := range funcs {
			ctx = f(ctx, req)
		}
		return ctx
	}
}

// ComposeHTTPContextFuncs composes multiple HTTPContextFuncs into a single one.
// This enables chaining of context modifications for HTTP transport, allowing modular setup of authentication, clients, and configuration.
func ComposeHTTPContextFuncs(funcs ...httpContextFunc) server.HTTPContextFunc {
	return func(ctx context.Context, req *http.Request) context.Context {
		for _, f := range funcs {
			ctx = f(ctx, req)
		}
		return ctx
	}
}

// ComposedStdioContextFunc returns a StdioContextFunc that comprises all predefined StdioContextFuncs.
// It sets up the complete context for stdio transport including Grafana configuration, client initialization from environment variables, and incident management support.
func ComposedStdioContextFunc(config GrafanaConfig) server.StdioContextFunc {
	return ComposeStdioContextFuncs(
		func(ctx context.Context) context.Context {
			return WithGrafanaConfig(ctx, config)
		},
		ExtractGrafanaInfoFromEnv,
		ExtractGrafanaClientFromEnv,
		ExtractKubernetesClientFromEnv,
		ExtractIncidentClientFromEnv,
	)
}

// ComposedSSEContextFunc returns a SSEContextFunc that comprises all predefined SSEContextFuncs.
// It sets up the complete context for SSE transport, extracting configuration from HTTP headers with environment variable fallbacks.
// If cache is non-nil, clients are cached by credentials to avoid per-request transport allocation.
func ComposedSSEContextFunc(config GrafanaConfig, cache ...*ClientCache) server.SSEContextFunc {
	grafanaExtractor, k8sExtractor, incidentExtractor := clientExtractors(cache)
	return ComposeSSEContextFuncs(
		func(ctx context.Context, req *http.Request) context.Context {
			return WithGrafanaConfig(ctx, config)
		},
		ExtractGrafanaInfoFromHeaders,
		grafanaExtractor,
		k8sExtractor,
		incidentExtractor,
	)
}

// ComposedHTTPContextFunc returns a HTTPContextFunc that comprises all predefined HTTPContextFuncs.
// It provides the complete context setup for HTTP transport, including header-based authentication and client configuration.
// If cache is non-nil, clients are cached by credentials to avoid per-request transport allocation.
func ComposedHTTPContextFunc(config GrafanaConfig, cache ...*ClientCache) server.HTTPContextFunc {
	grafanaExtractor, k8sExtractor, incidentExtractor := clientExtractors(cache)
	return ComposeHTTPContextFuncs(
		func(ctx context.Context, req *http.Request) context.Context {
			return WithGrafanaConfig(ctx, config)
		},
		ExtractGrafanaInfoFromHeaders,
		grafanaExtractor,
		k8sExtractor,
		incidentExtractor,
	)
}

// clientExtractors returns the appropriate client extraction functions,
// using cached versions if a cache is provided.
func clientExtractors(cache []*ClientCache) (grafana, k8s, incident httpContextFunc) {
	if len(cache) > 0 && cache[0] != nil {
		return extractGrafanaClientCached(cache[0]), extractKubernetesClientCached(cache[0]), extractIncidentClientCached(cache[0])
	}
	return ExtractGrafanaClientFromHeaders, ExtractKubernetesClientFromHeaders, ExtractIncidentClientFromHeaders
}
