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

	grafanaURLEnvVar                 = "GRAFANA_URL"
	grafanaServiceAccountTokenEnvVar = "GRAFANA_SERVICE_ACCOUNT_TOKEN"
	grafanaAPIKeyEnvVar              = "GRAFANA_API_KEY" // Deprecated fallback for backward compatibility
	grafanaOrgIDEnvVar               = "GRAFANA_ORG_ID"

	grafanaUsernameEnvVar = "GRAFANA_USERNAME"
	grafanaPasswordEnvVar = "GRAFANA_PASSWORD"

	grafanaExtraHeadersEnvVar = "GRAFANA_EXTRA_HEADERS"

	grafanaURLHeader                 = "X-Grafana-URL"
	grafanaServiceAccountTokenHeader = "X-Grafana-Service-Account-Token"
	grafanaAPIKeyHeader              = "X-Grafana-API-Key"

	// OAuth2 environment variables
	oauth2EnabledEnvVar            = "OAUTH2_ENABLED"
	oauth2ProviderURLEnvVar        = "OAUTH2_PROVIDER_URL"
	oauth2UserInfoEndpointEnvVar   = "OAUTH2_USERINFO_ENDPOINT"
	oauth2TokenCacheTTLEnvVar      = "OAUTH2_TOKEN_CACHE_TTL"
	oauth2TokenForwardToGrafanaEnabledEnvVar         = "OAUTH2_TOKEN_FORWARD_TO_GRAFANA_ENABLED"
	oauth2TokenForwardToGrafanaUseCloudHeadersEnvVar = "OAUTH2_TOKEN_FORWARD_TO_GRAFANA_USE_CLOUD_HEADERS"

	// Auth Proxy environment variables
	grafanaProxyAuthEnabledEnvVar  = "GRAFANA_PROXY_AUTH_ENABLED"
	grafanaProxyUserHeaderEnvVar   = "GRAFANA_PROXY_USER_HEADER"
	grafanaProxyEmailHeaderEnvVar  = "GRAFANA_PROXY_EMAIL_HEADER"
	grafanaProxyNameHeaderEnvVar   = "GRAFANA_PROXY_NAME_HEADER"
	grafanaProxyRoleHeaderEnvVar   = "GRAFANA_PROXY_ROLE_HEADER"

	// Default proxy header names
	defaultProxyUserHeader  = "X-WEBAUTH-USER"
	defaultProxyEmailHeader = "X-WEBAUTH-EMAIL"
	defaultProxyNameHeader  = "X-WEBAUTH-NAME"
	defaultProxyRoleHeader  = "X-WEBAUTH-ROLE"
)

func urlAndAPIKeyFromEnv() (string, string) {
	u := strings.TrimRight(os.Getenv(grafanaURLEnvVar), "/")

	// Prefer the documented service account token env var and fall back to the deprecated API key.
	apiKey := os.Getenv(grafanaServiceAccountTokenEnvVar)
	if apiKey == "" {
		apiKey = os.Getenv(grafanaAPIKeyEnvVar)
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

func orgIdFromEnv() int64 {
	orgIDStr := os.Getenv(grafanaOrgIDEnvVar)
	if orgIDStr == "" {
		return 0
	}
	orgID, err := strconv.ParseInt(orgIDStr, 10, 64)
	if err != nil {
		slog.Warn("Invalid GRAFANA_ORG_ID value, ignoring", "value", orgIDStr, "error", err)
		return 0
	}
	return orgID
}

func extraHeadersFromEnv() map[string]string {
	headersJSON := os.Getenv(grafanaExtraHeadersEnvVar)
	if headersJSON == "" {
		return nil
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		slog.Warn("invalid GRAFANA_EXTRA_HEADERS value, ignoring", "value", headersJSON, "error", err)
		return nil
	}
	return headers
}

func orgIdFromHeaders(req *http.Request) int64 {
	orgIDStr := req.Header.Get(client.OrgIDHeader)
	if orgIDStr == "" {
		return 0
	}
	orgID, err := strconv.ParseInt(orgIDStr, 10, 64)
	if err != nil {
		slog.Warn("Invalid X-Grafana-Org-Id header value, ignoring", "value", orgIDStr, "error", err)
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

// oauth2ConfigFromEnv extracts OAuth2 configuration from environment variables
func oauth2ConfigFromEnv() *OAuth2Config {
	return OAuth2ConfigFromEnv()
}

// OAuth2ConfigFromEnv extracts OAuth2 configuration from environment variables.
// Returns nil if OAuth2 is not enabled or OAUTH2_PROVIDER_URL is missing.
func OAuth2ConfigFromEnv() *OAuth2Config {
	enabled := strings.ToLower(os.Getenv(oauth2EnabledEnvVar)) == "true"
	if !enabled {
		return nil
	}

	providerURL := os.Getenv(oauth2ProviderURLEnvVar)
	if providerURL == "" {
		slog.Warn("OAUTH2_ENABLED is true but OAUTH2_PROVIDER_URL is empty, disabling OAuth2")
		return nil
	}

	tokenCacheTTL := 300 // Default 5 minutes
	if ttlStr := os.Getenv(oauth2TokenCacheTTLEnvVar); ttlStr != "" {
		if ttl, err := strconv.Atoi(ttlStr); err == nil {
			tokenCacheTTL = ttl
		} else {
			slog.Warn("Invalid OAUTH2_TOKEN_CACHE_TTL value, using default", "value", ttlStr, "error", err)
		}
	}

	userInfoEndpoint := os.Getenv(oauth2UserInfoEndpointEnvVar)
	if userInfoEndpoint == "" {
		// Default OpenID Connect standard endpoint
		userInfoEndpoint = "/protocol/openid-connect/userinfo"
	}

	return &OAuth2Config{
		Enabled:          true,
		ProviderURL:      providerURL,
		UserInfoEndpoint: userInfoEndpoint,
		TokenCacheTTL:    tokenCacheTTL,
	}
}

func envBoolValueIsTrue(envVar string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(envVar)), "true")
}

func oauth2TokenForwardToGrafanaEnabledFromEnv() bool {
	return OAuth2TokenForwardToGrafanaEnabledFromEnv()
}

// OAuth2TokenForwardToGrafanaEnabledFromEnv returns true when OAuth2 token forwarding to Grafana is enabled.
// Token forwarding is enabled by default when OAuth2 is configured.
// Set OAUTH2_TOKEN_FORWARD_TO_GRAFANA_ENABLED=false to explicitly disable it.
func OAuth2TokenForwardToGrafanaEnabledFromEnv() bool {
	// Check if explicitly disabled
	if val, exists := os.LookupEnv(oauth2TokenForwardToGrafanaEnabledEnvVar); exists {
		return strings.ToLower(strings.TrimSpace(val)) != "false"
	}
	// Default: enable token forwarding if OAuth2 is enabled
	return oauth2ConfigFromEnv() != nil
}

func oauth2TokenForwardToGrafanaUseCloudHeadersFromEnv() bool {
	return OAuth2TokenForwardToGrafanaUseCloudHeadersFromEnv()
}

// OAuth2TokenForwardToGrafanaUseCloudHeadersFromEnv returns true when Grafana Cloud-style forwarding headers should be used.
func OAuth2TokenForwardToGrafanaUseCloudHeadersFromEnv() bool {
	return envBoolValueIsTrue(oauth2TokenForwardToGrafanaUseCloudHeadersEnvVar)
}

// authProxyConfigFromEnv extracts Auth Proxy configuration from environment variables
// Auth Proxy is disabled by default and must be explicitly enabled.
func authProxyConfigFromEnv() (bool, string, string, string, string) {
	// Auth Proxy only enables if explicitly configured
	enabled := envBoolValueIsTrue(grafanaProxyAuthEnabledEnvVar)

	userHeader := os.Getenv(grafanaProxyUserHeaderEnvVar)
	if userHeader == "" {
		userHeader = defaultProxyUserHeader
	}

	emailHeader := os.Getenv(grafanaProxyEmailHeaderEnvVar)
	if emailHeader == "" {
		emailHeader = defaultProxyEmailHeader
	}

	nameHeader := os.Getenv(grafanaProxyNameHeaderEnvVar)
	if nameHeader == "" {
		nameHeader = defaultProxyNameHeader
	}

	roleHeader := os.Getenv(grafanaProxyRoleHeaderEnvVar)
	if roleHeader == "" {
		roleHeader = defaultProxyRoleHeader
	}

	return enabled, userHeader, emailHeader, nameHeader, roleHeader
}

// extractBearerToken extracts the bearer token from Authorization header
func extractBearerToken(authHeader string) string {
	const bearerScheme = "Bearer "
	if auth := strings.TrimSpace(authHeader); strings.HasPrefix(auth, bearerScheme) {
		return auth[len(bearerScheme):]
	}
	return ""
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

	// OAuth2Config holds OAuth2 provider configuration for token validation.
	OAuth2Config *OAuth2Config

	// OAuth2Client is the shared OAuth2 validation client used to validate
	// request bearer tokens. It is reused across requests so token cache entries
	// survive beyond a single HTTP request lifecycle.
	OAuth2Client *OAuth2Client

	// OAuth2TokenForwardToGrafanaEnabled enables forwarding validated incoming OAuth2
	// bearer tokens to Grafana for downstream authentication/identification.
	OAuth2TokenForwardToGrafanaEnabled bool

	// OAuth2TokenForwardToGrafanaUseCloudHeaders enables Grafana Cloud style token-forwarding
	// headers (X-Access-Token + X-Grafana-Id) when both service token and user token are available.
	// When disabled, token forwarding uses Authorization: Bearer <validated user token>.
	OAuth2TokenForwardToGrafanaUseCloudHeaders bool

	// ProxyAuthEnabled enables Auth Proxy mode for relaying user identity to Grafana.
	ProxyAuthEnabled bool

	// ProxyUserHeader is the HTTP header name for user identity (default: X-WEBAUTH-USER).
	ProxyUserHeader string

	// ProxyEmailHeader is the HTTP header name for user email (default: X-WEBAUTH-EMAIL).
	ProxyEmailHeader string

	// ProxyNameHeader is the HTTP header name for user display name (default: X-WEBAUTH-NAME).
	ProxyNameHeader string

	// ProxyRoleHeader is the HTTP header name for user roles (default: X-WEBAUTH-ROLE).
	ProxyRoleHeader string

	// AuthenticatedUser contains the OAuth2 user info from the current request.
	// Only populated when OAuth2 validation succeeds.
	AuthenticatedUser *OAuth2UserInfo
}

const (
	// DefaultGrafanaClientTimeout is the default timeout for Grafana HTTP client requests.
	DefaultGrafanaClientTimeout = 10 * time.Second
)

// WithGrafanaConfig adds Grafana configuration to the context.
// This configuration includes API credentials, debug settings, and TLS options that will be used by all Grafana clients created from this context.
func WithGrafanaConfig(ctx context.Context, config GrafanaConfig) context.Context {
	return context.WithValue(ctx, grafanaConfigKey{}, config)
}

// GrafanaConfigFromContext extracts Grafana configuration from the context.
// If no config is found, returns a zero-value GrafanaConfig. This function is typically used by internal components to access configuration set earlier in the request lifecycle.
func GrafanaConfigFromContext(ctx context.Context) GrafanaConfig {
	if config, ok := ctx.Value(grafanaConfigKey{}).(GrafanaConfig); ok {
		return config
	}
	return GrafanaConfig{}
}

func ensureOAuth2Client(config *GrafanaConfig) *OAuth2Client {
	if config == nil || config.OAuth2Config == nil {
		return nil
	}
	if config.OAuth2Client == nil {
		config.OAuth2Client = NewOAuth2Client(*config.OAuth2Config, nil)
	}
	return config.OAuth2Client
}

func prepareGrafanaConfig(config GrafanaConfig) GrafanaConfig {
	if config.OAuth2Config == nil {
		config.OAuth2Config = oauth2ConfigFromEnv()
	}
	ensureOAuth2Client(&config)
	return config
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

// Version returns the version of the mcp-grafana binary.
// It uses runtime/debug to fetch version information from the build, returning "(devel)" for local development builds.
// The version is computed once and cached for performance.
var Version = sync.OnceValue(func() string {
	// Default version string returned by `runtime/debug` if built
	// from the source repository rather than with `go install`.
	v := "(devel)"
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		v = bi.Main.Version
	}
	return v
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

// wrapWithUserAgent wraps an http.RoundTripper with user agent tracking
func wrapWithUserAgent(rt http.RoundTripper) http.RoundTripper {
	return NewUserAgentTransport(rt)
}

// OrgIDRoundTripper wraps an http.RoundTripper to add the X-Grafana-Org-Id header.
type OrgIDRoundTripper struct {
	underlying http.RoundTripper
	orgID      int64
}

func (t *OrgIDRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// clone the request to avoid modifying the original
	clonedReq := req.Clone(req.Context())

	if t.orgID > 0 {
		clonedReq.Header.Set(client.OrgIDHeader, strconv.FormatInt(t.orgID, 10))
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
	for k, v := range t.headers {
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

// AuthProxyRoundTripper adds Auth Proxy headers to relay user identity to Grafana
type AuthProxyRoundTripper struct {
	underlying http.RoundTripper
	userHeader string
	emailHeader string
	nameHeader string
	roleHeader string
	userInfo   *OAuth2UserInfo
}

func (t *AuthProxyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.userInfo == nil {
		// No user info, pass through unchanged
		return t.underlying.RoundTrip(req)
	}

	clonedReq := req.Clone(req.Context())

	// Add Auth Proxy headers for user identity
	if t.userInfo.Username != "" && t.userHeader != "" {
		clonedReq.Header.Set(t.userHeader, t.userInfo.Username)
	}

	if t.userInfo.Email != "" && t.emailHeader != "" {
		clonedReq.Header.Set(t.emailHeader, t.userInfo.Email)
	}

	if t.userInfo.Name != "" && t.nameHeader != "" {
		clonedReq.Header.Set(t.nameHeader, t.userInfo.Name)
	}

	// Add roles header if user has roles
	if len(t.userInfo.Roles) > 0 && t.roleHeader != "" {
		// Join roles with comma (Grafana convention)
		rolesStr := strings.Join(t.userInfo.Roles, ",")
		clonedReq.Header.Set(t.roleHeader, rolesStr)
	}

	return t.underlying.RoundTrip(clonedReq)
}

func NewAuthProxyRoundTripper(rt http.RoundTripper, userHeader, emailHeader, nameHeader, roleHeader string, userInfo *OAuth2UserInfo) *AuthProxyRoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &AuthProxyRoundTripper{
		underlying:  rt,
		userHeader:  userHeader,
		emailHeader: emailHeader,
		nameHeader:  nameHeader,
		roleHeader:  roleHeader,
		userInfo:    userInfo,
	}
}

func BuildTransport(cfg *GrafanaConfig, base http.RoundTripper) (http.RoundTripper, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	transport := base

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

	if len(cfg.ExtraHeaders) > 0 {
		transport = NewExtraHeadersRoundTripper(transport, cfg.ExtraHeaders)
	}

	return transport, nil
}

// Gets info from environment
func extractKeyGrafanaInfoFromEnv() (url, apiKey string, auth *url.Userinfo, orgId int64) {
	url, apiKey = urlAndAPIKeyFromEnv()
	if url == "" {
		url = defaultGrafanaURL
	}
	auth = userAndPassFromEnv()
	orgId = orgIdFromEnv()
	return
}

// Tries to get grafana info from a request.
// Gets info from environment if it can't get it from request
func extractKeyGrafanaInfoFromReq(req *http.Request) (grafanaUrl, apiKey string, auth *url.Userinfo, orgId int64) {
	eUrl, eApiKey, eAuth, eOrgId := extractKeyGrafanaInfoFromEnv()
	username, password, _ := req.BasicAuth()

	grafanaUrl, apiKey = urlAndAPIKeyFromHeaders(req)
	// If anything is missing, check if we can get it from the environment
	if grafanaUrl == "" {
		grafanaUrl = eUrl
	}

	if apiKey == "" {
		apiKey = eApiKey
	}

	// Use environment configured auth if nothing was passed in request
	if username == "" && password == "" {
		auth = eAuth
	} else {
		auth = url.UserPassword(username, password)
	}

	// extract org ID from header, fall back to environment
	orgId = orgIdFromHeaders(req)
	if orgId == 0 {
		orgId = eOrgId
	}

	return
}

// ExtractGrafanaInfoFromEnv is a StdioContextFunc that extracts Grafana configuration from environment variables.
// It reads GRAFANA_URL and GRAFANA_SERVICE_ACCOUNT_TOKEN (or deprecated GRAFANA_API_KEY) environment variables and adds the configuration to the context for use by Grafana clients.
var ExtractGrafanaInfoFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	u, apiKey, basicAuth, orgID := extractKeyGrafanaInfoFromEnv()
	parsedURL, err := url.Parse(u)
	if err != nil {
		panic(fmt.Errorf("invalid Grafana URL %s: %w", u, err))
	}

	extraHeaders := extraHeadersFromEnv()
	oauth2Config := oauth2ConfigFromEnv()
	proxyAuthEnabled, userHeader, emailHeader, nameHeader, roleHeader := authProxyConfigFromEnv()
	tokenForwardEnabled := oauth2TokenForwardToGrafanaEnabledFromEnv()
	tokenForwardUseCloudHeaders := oauth2TokenForwardToGrafanaUseCloudHeadersFromEnv()

	slog.Info("Using Grafana configuration",
		"url", parsedURL.Redacted(),
		"api_key_set", apiKey != "",
		"basic_auth_set", basicAuth != nil,
		"org_id", orgID,
		"extra_headers_count", len(extraHeaders),
		"oauth2_enabled", oauth2Config != nil,
		"oauth2_token_forward_to_grafana_enabled", tokenForwardEnabled,
		"oauth2_token_forward_use_cloud_headers", tokenForwardUseCloudHeaders,
		"proxy_auth_enabled", proxyAuthEnabled,
	)

	// Get existing config or create a new one.
	// This will respect the existing debug flag, if set.
	config := GrafanaConfigFromContext(ctx)
	config.URL = u
	config.APIKey = apiKey
	config.BasicAuth = basicAuth
	config.OrgID = orgID
	config.ExtraHeaders = extraHeaders
	config.OAuth2Config = oauth2Config
	config.OAuth2TokenForwardToGrafanaEnabled = tokenForwardEnabled
	config.OAuth2TokenForwardToGrafanaUseCloudHeaders = tokenForwardUseCloudHeaders
	config.ProxyAuthEnabled = proxyAuthEnabled
	config.ProxyUserHeader = userHeader
	config.ProxyEmailHeader = emailHeader
	config.ProxyNameHeader = nameHeader
	config.ProxyRoleHeader = roleHeader

	// Initialize OAuth2 client if enabled
	if oauth2Config != nil {
		oauth2Client := ensureOAuth2Client(&config)
		ctx = WithOAuth2Client(ctx, oauth2Client)
	}

	return WithGrafanaConfig(ctx, config)
}

// httpContextFunc is a function that can be used as a `server.HTTPContextFunc` or a
// `server.SSEContextFunc`. It is necessary because, while the two types are functionally
// identical, they have distinct types and cannot be passed around interchangeably.
type httpContextFunc func(ctx context.Context, req *http.Request) context.Context

// ExtractGrafanaInfoFromHeaders is a HTTPContextFunc that extracts Grafana configuration from HTTP request headers.
// It reads X-Grafana-URL and X-Grafana-API-Key headers, falling back to environment variables if headers are not present.
// It also extracts and validates OAuth2 tokens if configured.
var ExtractGrafanaInfoFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	u, apiKey, basicAuth, orgID := extractKeyGrafanaInfoFromReq(req)

	// Get existing config or create a new one.
	// This will respect the existing debug flag, if set.
	config := GrafanaConfigFromContext(ctx)
	config.URL = u
	config.APIKey = apiKey
	config.BasicAuth = basicAuth
	config.OrgID = orgID
	config.ExtraHeaders = extraHeadersFromEnv()
	config.OAuth2Config = oauth2ConfigFromEnv()
	config.OAuth2TokenForwardToGrafanaEnabled = oauth2TokenForwardToGrafanaEnabledFromEnv()
	config.OAuth2TokenForwardToGrafanaUseCloudHeaders = oauth2TokenForwardToGrafanaUseCloudHeadersFromEnv()
	proxyAuthEnabled, userHeader, emailHeader, nameHeader, roleHeader := authProxyConfigFromEnv()
	config.ProxyAuthEnabled = proxyAuthEnabled
	config.ProxyUserHeader = userHeader
	config.ProxyEmailHeader = emailHeader
	config.ProxyNameHeader = nameHeader
	config.ProxyRoleHeader = roleHeader

	// Initialize OAuth2 client if needed
	var oauth2Client *OAuth2Client
	if config.OAuth2Config != nil {
		oauth2Client = ensureOAuth2Client(&config)
		ctx = WithOAuth2Client(ctx, oauth2Client)

		// Extract and validate OAuth2 token from Authorization header
		if token := extractBearerToken(req.Header.Get("Authorization")); token != "" {
			userInfo, err := oauth2Client.ValidateToken(ctx, token)
			if err != nil {
				slog.Warn("OAuth2 token validation failed", "error", err)
				// Continue without user info - will fall back to service account
			} else {
				config.AuthenticatedUser = userInfo
				if config.OAuth2TokenForwardToGrafanaEnabled {
					// Always preserve validated user token for downstream forwarding.
					config.IDToken = token
					// If explicitly enabled and a service token is configured, use Grafana Cloud OBO headers.
					if config.OAuth2TokenForwardToGrafanaUseCloudHeaders && config.APIKey != "" {
						config.AccessToken = config.APIKey
					}
				}
				ctx = WithOAuth2UserInfo(ctx, userInfo)
				slog.Debug("OAuth2 token validated", "user", userInfo.Username)
			}
		}
	}

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

// proxyAuthParams holds Auth Proxy configuration needed to authenticate the public URL fetch.
type proxyAuthParams struct {
	userHeader  string
	emailHeader string
	nameHeader  string
	roleHeader  string
	userInfo    *OAuth2UserInfo
}

// fetchPublicURL fetches the public URL (appUrl) from Grafana's frontend settings API.
// It returns the appUrl if available, or an empty string if the request fails.
// Successful results are cached permanently; failures are retried on subsequent calls.
// Concurrent calls for the same grafanaURL are coalesced via singleflight.
func fetchPublicURL(ctx context.Context, grafanaURL, apiKey, idToken string, auth *url.Userinfo, tlsConfig *TLSConfig, extraHeaders map[string]string, proxyAuth *proxyAuthParams) string {
	// Check cache first (only successful results are cached)
	if cached, ok := publicURLCache.Load(grafanaURL); ok {
		return cached.(string)
	}

	// Use singleflight to coalesce concurrent requests for the same URL
	result, _, _ := publicURLFlight.Do(grafanaURL, func() (any, error) {
		// Double-check cache inside singleflight (another goroutine may have populated it)
		if cached, ok := publicURLCache.Load(grafanaURL); ok {
			return cached.(string), nil
		}

		// Use a detached context with timeout so that a cancelled request
		// context from the first caller doesn't fail the fetch for all waiters.
		fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		publicURL := doFetchPublicURL(fetchCtx, grafanaURL, apiKey, idToken, auth, tlsConfig, extraHeaders, proxyAuth)

		// Only cache successful (non-empty) results so transient failures are retried
		if publicURL != "" {
			publicURLCache.Store(grafanaURL, publicURL)
		}

		return publicURL, nil
	})

	return result.(string)
}

// doFetchPublicURL performs the actual HTTP request to fetch the public URL.
func doFetchPublicURL(ctx context.Context, grafanaURL, apiKey, idToken string, auth *url.Userinfo, tlsConfig *TLSConfig, extraHeaders map[string]string, proxyAuth *proxyAuthParams) string {
	settingsURL := strings.TrimRight(grafanaURL, "/") + "/api/frontend/settings"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, settingsURL, nil)
	if err != nil {
		slog.Warn("Failed to create request for frontend settings", "error", err)
		return ""
	}

	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	} else if idToken != "" {
		// Token-forwarding mode: use the validated incoming OAuth2 user token.
		req.Header.Set("Authorization", "Bearer "+idToken)
	} else if auth != nil {
		password, _ := auth.Password()
		req.SetBasicAuth(auth.Username(), password)
	} else if proxyAuth != nil && proxyAuth.userInfo != nil {
		// Auth Proxy mode: inject user identity headers so Grafana authenticates the request
		if proxyAuth.userInfo.Username != "" && proxyAuth.userHeader != "" {
			req.Header.Set(proxyAuth.userHeader, proxyAuth.userInfo.Username)
		}
		if proxyAuth.userInfo.Email != "" && proxyAuth.emailHeader != "" {
			req.Header.Set(proxyAuth.emailHeader, proxyAuth.userInfo.Email)
		}
		if proxyAuth.userInfo.Name != "" && proxyAuth.nameHeader != "" {
			req.Header.Set(proxyAuth.nameHeader, proxyAuth.userInfo.Name)
		}
		if len(proxyAuth.userInfo.Roles) > 0 && proxyAuth.roleHeader != "" {
			req.Header.Set(proxyAuth.roleHeader, strings.Join(proxyAuth.userInfo.Roles, ","))
		}
	}
	req.Header.Set("User-Agent", UserAgent())

	// Apply extra headers (e.g., for proxies requiring custom headers)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	if tlsConfig != nil {
		tlsCfg, err := tlsConfig.CreateTLSConfig()
		if err != nil {
			slog.Warn("Failed to create TLS config for frontend settings request", "error", err)
			return ""
		}
		httpClient.Transport = &http.Transport{
			TLSClientConfig: tlsCfg,
			Proxy:           http.ProxyFromEnvironment,
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Warn("Failed to fetch frontend settings", "error", err)
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("Frontend settings request returned non-OK status", "status", resp.StatusCode)
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("Failed to read frontend settings response", "error", err)
		return ""
	}

	var settings struct {
		AppURL string `json:"appUrl"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		slog.Warn("Failed to parse frontend settings response", "error", err)
		return ""
	}

	publicURL := strings.TrimRight(settings.AppURL, "/")
	if publicURL != "" {
		slog.Info("Fetched public URL from Grafana frontend settings", "public_url", publicURL)
	}
	return publicURL
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

	config := GrafanaConfigFromContext(ctx)

	if apiKey != "" {
		cfg.APIKey = apiKey
	} else if config.IDToken != "" {
		// OBO fallback: forward the validated incoming bearer token.
		cfg.APIKey = config.IDToken
	}

	if auth != nil {
		cfg.BasicAuth = auth
	}

	cfg.Debug = config.Debug

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
		slog.Debug("Using custom TLS configuration",
			"cert_file", tlsConfig.CertFile,
			"ca_file", tlsConfig.CAFile,
			"skip_verify", tlsConfig.SkipVerify)
	}

	// Determine timeout - use config value if set, otherwise use default
	timeout := config.Timeout
	if timeout == 0 {
		timeout = DefaultGrafanaClientTimeout
	}

	slog.Debug("Creating Grafana client", "url", parsedURL.Redacted(), "api_key_set", apiKey != "", "basic_auth_set", config.BasicAuth != nil, "org_id", cfg.OrgID, "timeout", timeout, "extra_headers_count", len(config.ExtraHeaders))
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

	// Always enable HTTP tracing for context propagation (no-op when no exporter configured)
	// Use reflection to wrap the transport without importing the runtime client package
	v := reflect.ValueOf(grafanaClient.Transport)
	if v.Kind() == reflect.Ptr && !v.IsNil() {
		v = v.Elem()
		if v.Kind() == reflect.Struct {
			transportField := v.FieldByName("Transport")
			if transportField.IsValid() && transportField.CanSet() {
				if _, ok := transportField.Interface().(http.RoundTripper); ok {
					// Wrap with timeout transport, then user agent, then otel
					timeoutTransport := &http.Transport{
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
					// Copy TLS config if present
					if cfg.TLSConfig != nil {
						timeoutTransport.TLSClientConfig = cfg.TLSConfig
					}
					var rt http.RoundTripper = timeoutTransport
					if len(config.ExtraHeaders) > 0 {
						rt = NewExtraHeadersRoundTripper(rt, config.ExtraHeaders)
					}
					if config.OrgID > 0 {
						rt = NewOrgIDRoundTripper(rt, config.OrgID)
					}
					// Add Auth Proxy headers if configured and user info is available
					if config.ProxyAuthEnabled {
						rt = NewAuthProxyRoundTripper(rt, config.ProxyUserHeader, config.ProxyEmailHeader, config.ProxyNameHeader, config.ProxyRoleHeader, config.AuthenticatedUser)
					}
					userAgentWrapped := wrapWithUserAgent(rt)
					wrapped := otelhttp.NewTransport(userAgentWrapped)
					transportField.Set(reflect.ValueOf(wrapped))
					slog.Debug("HTTP tracing, user agent tracking, and timeout enabled for Grafana client", "timeout", timeout)
				}
			}
		}
	}

	// Fetch the public URL from Grafana's frontend settings.
	var pa *proxyAuthParams
	if config.ProxyAuthEnabled && config.AuthenticatedUser != nil {
		pa = &proxyAuthParams{
			userHeader:  config.ProxyUserHeader,
			emailHeader: config.ProxyEmailHeader,
			nameHeader:  config.ProxyNameHeader,
			roleHeader:  config.ProxyRoleHeader,
			userInfo:    config.AuthenticatedUser,
		}
	}
	publicURL := fetchPublicURL(ctx, grafanaURL, apiKey, config.IDToken, auth, config.TLSConfig, config.ExtraHeaders, pa)

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
	grafanaURL, apiKey := urlAndAPIKeyFromEnv()
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
	if config.OrgID == 0 {
		slog.Warn("No org ID found in request headers or environment variables, using default org. Set GRAFANA_ORG_ID or pass X-Grafana-Org-Id header to target a specific org.")
	}

	// Extract transport config from request headers, and set it on the context.
	u, apiKey, basicAuth, _ := extractKeyGrafanaInfoFromReq(req)

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

type incidentClientKey struct{}

// ExtractIncidentClientFromEnv is a StdioContextFunc that creates and injects a Grafana Incident client into the context.
// It configures the client using environment variables and applies any custom TLS settings from the context.
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

	config := GrafanaConfigFromContext(ctx)
	transport, err := BuildTransport(&config, nil)
	if err != nil {
		slog.Error("Failed to create custom transport for incident client, using default", "error", err)
	} else {
		orgIDWrapped := NewOrgIDRoundTripper(transport, config.OrgID)
		client.HTTPClient.Transport = wrapWithUserAgent(orgIDWrapped)
		if config.TLSConfig != nil {
			slog.Debug("Using custom TLS configuration, user agent, and org ID support for incident client",
				"cert_file", config.TLSConfig.CertFile,
				"ca_file", config.TLSConfig.CAFile,
				"skip_verify", config.TLSConfig.SkipVerify)
		}
	}

	return context.WithValue(ctx, incidentClientKey{}, client)
}

// ExtractIncidentClientFromHeaders is a HTTPContextFunc that creates and injects a Grafana Incident client into the context.
// It uses HTTP headers for configuration with environment variable fallbacks, enabling per-request incident management configuration.
var ExtractIncidentClientFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	grafanaURL, apiKey, _, orgID := extractKeyGrafanaInfoFromReq(req)

	config := GrafanaConfigFromContext(ctx)
	// In token-forwarding mode no service account key is configured; use the
	// validated incoming OAuth2 user token so incident API calls are authenticated.
	if apiKey == "" && config.IDToken != "" {
		apiKey = config.IDToken
	}

	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/resources/api/v1/", grafanaURL)
	client := incident.NewClient(incidentURL, apiKey)

	transport, err := BuildTransport(&config, nil)
	if err != nil {
		slog.Error("Failed to create custom transport for incident client, using default", "error", err)
	} else {
		orgIDWrapped := NewOrgIDRoundTripper(transport, orgID)
		client.HTTPClient.Transport = wrapWithUserAgent(orgIDWrapped)
		if config.TLSConfig != nil {
			slog.Debug("Using custom TLS configuration, user agent, and org ID support for incident client",
				"cert_file", config.TLSConfig.CertFile,
				"ca_file", config.TLSConfig.CAFile,
				"skip_verify", config.TLSConfig.SkipVerify)
		}
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
	config = prepareGrafanaConfig(config)
	return ComposeStdioContextFuncs(
		func(ctx context.Context) context.Context {
			return WithGrafanaConfig(ctx, config)
		},
		ExtractGrafanaInfoFromEnv,
		ExtractGrafanaClientFromEnv,
		ExtractIncidentClientFromEnv,
	)
}

// ComposedSSEContextFunc returns a SSEContextFunc that comprises all predefined SSEContextFuncs.
// It sets up the complete context for SSE transport, extracting configuration from HTTP headers with environment variable fallbacks.
// If cache is non-nil, clients are cached by credentials to avoid per-request transport allocation.
func ComposedSSEContextFunc(config GrafanaConfig, cache ...*ClientCache) server.SSEContextFunc {
	config = prepareGrafanaConfig(config)
	grafanaExtractor, incidentExtractor := clientExtractors(cache)
	return ComposeSSEContextFuncs(
		func(ctx context.Context, req *http.Request) context.Context {
			return WithGrafanaConfig(ctx, config)
		},
		ExtractGrafanaInfoFromHeaders,
		grafanaExtractor,
		incidentExtractor,
	)
}

// ComposedHTTPContextFunc returns a HTTPContextFunc that comprises all predefined HTTPContextFuncs.
// It provides the complete context setup for HTTP transport, including header-based authentication and client configuration.
// If cache is non-nil, clients are cached by credentials to avoid per-request transport allocation.
func ComposedHTTPContextFunc(config GrafanaConfig, cache ...*ClientCache) server.HTTPContextFunc {
	config = prepareGrafanaConfig(config)
	grafanaExtractor, incidentExtractor := clientExtractors(cache)
	return ComposeHTTPContextFuncs(
		func(ctx context.Context, req *http.Request) context.Context {
			return WithGrafanaConfig(ctx, config)
		},
		ExtractGrafanaInfoFromHeaders,
		grafanaExtractor,
		incidentExtractor,
	)
}

// clientExtractors returns the appropriate client extraction functions,
// using cached versions if a cache is provided.
func clientExtractors(cache []*ClientCache) (httpContextFunc, httpContextFunc) {
	if len(cache) > 0 && cache[0] != nil {
		return extractGrafanaClientCached(cache[0]), extractIncidentClientCached(cache[0])
	}
	return ExtractGrafanaClientFromHeaders, ExtractIncidentClientFromHeaders
}
