package mcpgrafana

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	coboAuthJWTSecretEnvVar   = "COBO_AUTH_JWT_SECRET"
	coboAuthJWTAudienceEnvVar = "COBO_AUTH_JWT_AUDIENCE"
	coboOAuthBaseURLEnvVar    = "COBO_OAUTH_SERVER_BASE_URL"
	oauthBaseURLEnvVar        = "OAUTH_SERVER_BASE_URL"
	coboOAuthProviderEnvVar   = "COBO_OAUTH_PROVIDER"
	coboOAuthScopesEnvVar     = "COBO_OAUTH_SCOPES"

	grafanaAuthModeEnvVar         = "GRAFANA_AUTH_MODE"
	grafanaJWTRSAPrivateKeyEnvVar = "GRAFANA_JWT_RSA_PRIVATE_KEY"
	grafanaJWTKeyIDEnvVar         = "GRAFANA_JWT_KEY_ID"
	grafanaJWTIssuerEnvVar        = "GRAFANA_JWT_ISSUER"
	grafanaJWTAudienceEnvVar      = "GRAFANA_JWT_AUDIENCE"
	grafanaJWTTTLSecondsEnvVar    = "GRAFANA_JWT_TTL_SECONDS"
	grafanaJWTHeaderName          = "X-Cobo-JWT"

	defaultGrafanaJWTKeyID     = "grafana-jwt-key-1"
	defaultGrafanaJWTIssuer    = "grafana_mcp_server"
	defaultGrafanaJWTAudience  = "grafana"
	defaultGrafanaJWTTTLSecond = 60
	defaultCoboOAuthProvider   = "cobo_agent_oauth"

	// OAuthProtectedResourceMetadataPath is the RFC 9728 well-known prefix. The
	// path-suffixed variant (prefix + resource path) is the canonical location
	// for a resource that is not served at the origin root.
	OAuthProtectedResourceMetadataPath = "/.well-known/oauth-protected-resource"
)

type coboIdentityKey struct{}

// CoboIdentity is the authenticated caller extracted from the Cobo OAuth JWT.
type CoboIdentity struct {
	Email     string
	Name      string
	SessionID string
	Token     string
}

// WithCoboIdentity attaches the authenticated Cobo caller to a context.
func WithCoboIdentity(ctx context.Context, identity CoboIdentity) context.Context {
	return context.WithValue(ctx, coboIdentityKey{}, identity)
}

// CoboIdentityFromContext returns the authenticated Cobo caller from a context.
func CoboIdentityFromContext(ctx context.Context) (CoboIdentity, bool) {
	identity, ok := ctx.Value(coboIdentityKey{}).(CoboIdentity)
	return identity, ok
}

// PropagateCoboIdentityFromRequest copies the inbound HTTP identity into the MCP context.
func PropagateCoboIdentityFromRequest(ctx context.Context, req *http.Request) context.Context {
	if identity, ok := CoboIdentityFromContext(req.Context()); ok {
		return WithCoboIdentity(ctx, identity)
	}
	return ctx
}

// CoboAuthConfig controls inbound MCP JWT validation.
type CoboAuthConfig struct {
	Enabled   bool
	JWTSecret string
	// JWTAudience, when non-empty, requires the inbound token "aud" claim to
	// contain this value. Empty keeps the previous behaviour (no aud check).
	JWTAudience string
	ExemptPaths []string
	Realm       string
	// ResourcePath is the MCP entry path (e.g. "/sse" or "/mcp"). It selects the
	// path-suffixed protected resource metadata advertised in WWW-Authenticate.
	ResourcePath string
}

// CoboAuthMiddleware validates inbound Cobo HS256 bearer tokens for HTTP transports.
func CoboAuthMiddleware(cfg CoboAuthConfig, next http.Handler) http.Handler {
	if !cfg.Enabled {
		return next
	}
	exempt := make(map[string]struct{}, len(cfg.ExemptPaths))
	for _, path := range cfg.ExemptPaths {
		if path != "" {
			exempt[path] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := exempt[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}
		identity, err := ExtractCoboIdentityFromRequestWithAudience(r, cfg.JWTSecret, cfg.JWTAudience)
		if err != nil {
			realm := cfg.Realm
			if realm == "" {
				realm = "Grafana MCP"
			}
			metadataURL := requestExternalBaseURL(r) + ProtectedResourceMetadataPath(cfg.ResourcePath)
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(
				`Bearer realm="%s", error="invalid_token", error_description="Authentication required", resource_metadata="%s"`,
				realm,
				metadataURL,
			))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithCoboIdentity(r.Context(), identity)))
	})
}

// OAuthMetadataConfig describes the OAuth authorization server used by MCP clients.
type OAuthMetadataConfig struct {
	ServerBaseURL string
	Provider      string
	Scopes        []string
}

func (cfg OAuthMetadataConfig) withDefaults() OAuthMetadataConfig {
	cfg.ServerBaseURL = strings.TrimRight(strings.TrimSpace(cfg.ServerBaseURL), "/")
	if cfg.Provider == "" {
		cfg.Provider = defaultCoboOAuthProvider
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"profile", "email", "openid"}
	}
	return cfg
}

// OAuthMetadataConfigFromEnv loads Cobo OAuth metadata settings, matching the Python runner env names.
func OAuthMetadataConfigFromEnv() OAuthMetadataConfig {
	baseURL := os.Getenv(coboOAuthBaseURLEnvVar)
	if strings.TrimSpace(baseURL) == "" {
		baseURL = os.Getenv(oauthBaseURLEnvVar)
	}
	return OAuthMetadataConfig{
		ServerBaseURL: baseURL,
		Provider:      os.Getenv(coboOAuthProviderEnvVar),
		Scopes:        strings.Fields(os.Getenv(coboOAuthScopesEnvVar)),
	}.withDefaults()
}

// NormalizeResourcePath returns a clean absolute MCP entry path ("" for root).
func NormalizeResourcePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(path, "/")
}

// ProtectedResourceMetadataPath returns the RFC 9728 metadata path for an MCP entry path,
// e.g. "/sse" -> "/.well-known/oauth-protected-resource/sse".
func ProtectedResourceMetadataPath(resourcePath string) string {
	return OAuthProtectedResourceMetadataPath + NormalizeResourcePath(resourcePath)
}

// OAuthProtectedResourceMetadataHandler returns MCP protected resource metadata for OAuth discovery.
// resourcePath is the MCP entry path; the advertised "resource" is the full entry URL.
func OAuthProtectedResourceMetadataHandler(cfg OAuthMetadataConfig, resourcePath string) http.HandlerFunc {
	resourcePath = NormalizeResourcePath(resourcePath)
	return func(w http.ResponseWriter, r *http.Request) {
		metadata, err := OAuthProtectedResourceMetadata(cfg, requestExternalBaseURL(r)+resourcePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(metadata); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// RegisterOAuthDiscoveryRoutes mounts protected resource metadata at both the root
// well-known path and the path-suffixed one for resourcePath, and returns the paths
// that must bypass bearer auth.
//
// Authorization server metadata is intentionally NOT served here: clients discover
// it from the authorization server listed in "authorization_servers" (auth-ai), so
// supported client auth methods (incl. "none") and revocation come from the source
// of truth instead of a hand-maintained copy.
func RegisterOAuthDiscoveryRoutes(mux *http.ServeMux, cfg OAuthMetadataConfig, resourcePath string) []string {
	handler := OAuthProtectedResourceMetadataHandler(cfg, resourcePath)
	paths := []string{OAuthProtectedResourceMetadataPath}
	if suffixed := ProtectedResourceMetadataPath(resourcePath); suffixed != OAuthProtectedResourceMetadataPath {
		paths = append(paths, suffixed)
	}
	for _, p := range paths {
		mux.Handle(p, handler)
	}
	return paths
}

// OAuthProtectedResourceMetadata describes this MCP server as an OAuth protected resource.
func OAuthProtectedResourceMetadata(cfg OAuthMetadataConfig, resourceURL string) (map[string]any, error) {
	cfg = cfg.withDefaults()
	if cfg.ServerBaseURL == "" {
		return nil, errors.New("OAUTH_SERVER_BASE_URL is required")
	}
	return map[string]any{
		"resource":              strings.TrimRight(strings.TrimSpace(resourceURL), "/"),
		"authorization_servers": []string{cfg.ServerBaseURL},
		"scopes_supported":      cfg.Scopes,
		"bearer_methods_supported": []string{
			"header",
		},
	}, nil
}

func requestExternalBaseURL(r *http.Request) string {
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if host == "" {
		host = "localhost"
	}
	return strings.TrimRight(proto+"://"+host, "/")
}

// ExtractCoboIdentityFromRequest validates the Authorization bearer token and returns its caller identity.
func ExtractCoboIdentityFromRequest(r *http.Request, secret string) (CoboIdentity, error) {
	return ExtractCoboIdentityFromRequestWithAudience(r, secret, "")
}

// ExtractCoboIdentityFromRequestWithAudience is ExtractCoboIdentityFromRequest with an optional aud check.
func ExtractCoboIdentityFromRequestWithAudience(r *http.Request, secret, audience string) (CoboIdentity, error) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return CoboIdentity{}, errors.New("missing bearer token")
	}
	return ParseCoboIdentityWithAudience(strings.TrimSpace(auth[len("Bearer "):]), secret, audience)
}

// ParseCoboIdentity validates a Cobo HS256 JWT and extracts the user identity.
func ParseCoboIdentity(tokenString, secret string) (CoboIdentity, error) {
	return ParseCoboIdentityWithAudience(tokenString, secret, "")
}

// ParseCoboIdentityWithAudience validates a Cobo HS256 JWT and, when audience is
// non-empty, requires its "aud" claim to contain audience.
func ParseCoboIdentityWithAudience(tokenString, secret, audience string) (CoboIdentity, error) {
	if strings.TrimSpace(secret) == "" {
		return CoboIdentity{}, errors.New("COBO_AUTH_JWT_SECRET is required")
	}
	opts := []jwt.ParserOption{jwt.WithExpirationRequired(), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()})}
	if audience = strings.TrimSpace(audience); audience != "" {
		opts = append(opts, jwt.WithAudience(audience))
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %s", token.Method.Alg())
		}
		return []byte(secret), nil
	}, opts...)
	if err != nil {
		return CoboIdentity{}, err
	}
	if !token.Valid {
		return CoboIdentity{}, errors.New("invalid token")
	}

	userInfo, _ := claims["user_info"].(map[string]any)
	email, _ := userInfo["email"].(string)
	if email == "" {
		email, _ = claims["email"].(string)
	}
	if email == "" {
		email, _ = claims["sub"].(string)
	}
	if email == "" {
		return CoboIdentity{}, errors.New("missing user email")
	}
	name, _ := userInfo["name"].(string)
	if name == "" {
		name = email
	}
	sessionID, _ := claims["session_id"].(string)

	return CoboIdentity{Email: email, Name: name, SessionID: sessionID, Token: tokenString}, nil
}

// GrafanaJWTConfig controls outbound Grafana auth.jwt token issuance.
type GrafanaJWTConfig struct {
	Enabled    bool
	PrivateKey *rsa.PrivateKey
	KeyID      string
	Issuer     string
	Audience   string
	TTL        time.Duration
}

func (cfg GrafanaJWTConfig) withDefaults() GrafanaJWTConfig {
	if cfg.KeyID == "" {
		cfg.KeyID = defaultGrafanaJWTKeyID
	}
	if cfg.Issuer == "" {
		cfg.Issuer = defaultGrafanaJWTIssuer
	}
	if cfg.Audience == "" {
		cfg.Audience = defaultGrafanaJWTAudience
	}
	if cfg.TTL <= 0 {
		cfg.TTL = time.Duration(defaultGrafanaJWTTTLSecond) * time.Second
	}
	return cfg
}

// IssueGrafanaJWT signs a short-lived RS256 JWT accepted by Grafana auth.jwt.
func IssueGrafanaJWT(identity CoboIdentity, cfg GrafanaJWTConfig) (string, error) {
	cfg = cfg.withDefaults()
	if cfg.PrivateKey == nil {
		return "", errors.New("GRAFANA_JWT_RSA_PRIVATE_KEY is required")
	}
	if identity.Email == "" {
		return "", errors.New("missing Cobo identity email")
	}
	name := identity.Name
	if name == "" {
		name = identity.Email
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   identity.Email,
		"email": identity.Email,
		"user":  name,
		"iat":   now.Unix(),
		"exp":   now.Add(cfg.TTL).Unix(),
		"iss":   cfg.Issuer,
		"aud":   cfg.Audience,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = cfg.KeyID
	return token.SignedString(cfg.PrivateKey)
}

// GrafanaJWTRoundTripper injects X-Cobo-JWT for Grafana auth.jwt.
type GrafanaJWTRoundTripper struct {
	cfg        GrafanaJWTConfig
	underlying http.RoundTripper
}

func NewGrafanaJWTRoundTripper(rt http.RoundTripper, cfg GrafanaJWTConfig) *GrafanaJWTRoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &GrafanaJWTRoundTripper{cfg: cfg.withDefaults(), underlying: rt}
}

func (rt *GrafanaJWTRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	identity, ok := CoboIdentityFromContext(req.Context())
	if !ok {
		return nil, errors.New("Grafana JWT auth requires an authenticated Cobo identity")
	}
	token, err := IssueGrafanaJWT(identity, rt.cfg)
	if err != nil {
		return nil, err
	}
	clonedReq := req.Clone(req.Context())
	clonedReq.Header.Set(grafanaJWTHeaderName, token)
	clonedReq.Header.Del("Authorization")
	return rt.underlying.RoundTrip(clonedReq)
}

func parseRSAPrivateKeyPEM(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("invalid PEM private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return key, nil
}

// sseSessionIDPattern matches the session id in the go-sdk SSE "endpoint" event
// ("event: endpoint\ndata: /sse?sessionid=<id>\n\n").
var sseSessionIDPattern = regexp.MustCompile(`sessionid=([A-Za-z0-9_-]+)`)

// CoboSSESessionBinder pins each SSE session to the Cobo caller that opened it.
//
// The go-sdk SSE transport runs tool calls with the context of the GET that
// opened the stream, not the POST that carries the message, so without this a
// caller holding any valid token could POST into another user's session (if it
// learned the session id) and act as that user. The binder records the owner
// when the stream announces its session id and rejects POSTs from anyone else.
// Requests without a Cobo identity (auth disabled) pass through unchanged.
func CoboSSESessionBinder(ssePath string, next http.Handler) http.Handler {
	var (
		mu     sync.Mutex
		owners = map[string]string{}
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := CoboIdentityFromContext(r.Context())
		if !ok || r.URL.Path != ssePath {
			next.ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodPost:
			if sid := r.URL.Query().Get("sessionid"); sid != "" {
				mu.Lock()
				owner, known := owners[sid]
				mu.Unlock()
				if known && !strings.EqualFold(owner, identity.Email) {
					http.Error(w, "forbidden: session belongs to another user", http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		case http.MethodGet:
			bw := &sseBindingWriter{ResponseWriter: w}
			bw.onSession = func(sid string) {
				mu.Lock()
				owners[sid] = identity.Email
				mu.Unlock()
			}
			defer func() {
				if bw.sessionID != "" {
					mu.Lock()
					delete(owners, bw.sessionID)
					mu.Unlock()
				}
			}()
			next.ServeHTTP(bw, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

type sseBindingWriter struct {
	http.ResponseWriter
	sessionID string
	onSession func(string)
}

func (w *sseBindingWriter) Write(p []byte) (int, error) {
	if w.sessionID == "" && bytes.Contains(p, []byte("event: endpoint")) {
		if m := sseSessionIDPattern.FindSubmatch(p); m != nil {
			w.sessionID = string(m[1])
			// Register before the client can see the endpoint and POST to it.
			w.onSession(w.sessionID)
		}
	}
	return w.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the underlying Flusher.
func (w *sseBindingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
