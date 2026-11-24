package mcpgrafana

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// DefaultCallerAuthHeader is the header the caller token is read from unless
// the operator picks another one.
const DefaultCallerAuthHeader = "Authorization"

// RequireBearerToken is RequireCallerToken on the Authorization header.
func RequireBearerToken(expected string, logger *slog.Logger) func(http.Handler) http.Handler {
	return RequireCallerToken(DefaultCallerAuthHeader, expected, logger)
}

// RequireCallerToken returns middleware that authenticates the *caller* of the
// MCP server (not the credentials the server uses to reach Grafana). Requests
// must carry the token expected in header, or they are rejected with 401 before
// reaching the MCP handler. Authorization takes "Bearer <token>" only. Any other
// header takes the raw token or "Bearer <token>", so a proxy can keep
// Authorization for the user's own credential.
//
// Both sides are SHA-256 hashed and compared in constant time, so neither the
// token's value nor its length leaks via timing. A custom header that is sent
// more than once is rejected; Authorization uses its first value, as before.
// On success the header is stripped so the caller token can
// never be forwarded to Grafana or folded into a cache key (Grafana auth uses
// its own X-Grafana-* headers).
//
// CORS preflight (OPTIONS) passes through unauthenticated (no credentials, no
// MCP operation). If expected or header is empty the middleware fails closed and
// rejects every request. Rejections are logged at WARN with request metadata
// only, never the token. A nil logger falls back to slog.Default().
func RequireCallerToken(header, expected string, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	expectedHash := sha256.Sum256([]byte(expected))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Let CORS preflight through unauthenticated — it carries no bearer
			// token and triggers no MCP operation.
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			if expected == "" || header == "" {
				rejectUnauthorized(w, r, header, logger, "server misconfigured: empty auth token")
				return
			}
			token, reason := callerTokenFromRequest(r, header)
			if token == "" {
				rejectUnauthorized(w, r, header, logger, reason)
				return
			}
			presentedHash := sha256.Sum256([]byte(token))
			if subtle.ConstantTimeCompare(presentedHash[:], expectedHash[:]) != 1 {
				rejectUnauthorized(w, r, header, logger, "invalid bearer token")
				return
			}
			// Authentication succeeded. Strip the caller credential so it can
			// never reach Grafana or a cache key downstream.
			r.Header.Del(header)
			next.ServeHTTP(w, r)
		})
	}
}

// rejectUnauthorized logs a blocked request (metadata and reason only, never the
// token) and writes a 401.
func rejectUnauthorized(w http.ResponseWriter, r *http.Request, header string, logger *slog.Logger, reason string) {
	logger.Warn("Rejected unauthenticated request to MCP endpoint",
		"remote_addr", r.RemoteAddr,
		"method", r.Method,
		"path", r.URL.Path,
		"reason", reason,
	)
	if strings.EqualFold(header, DefaultCallerAuthHeader) {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	http.Error(w, "unauthorized: "+reason, http.StatusUnauthorized)
}

// callerTokenFromRequest extracts the token from header. It returns an empty
// token and the reason when the header is absent, repeated (custom
// header only), or (for Authorization) not a bearer credential. The scheme match is case-insensitive
// per RFC 7235.
func callerTokenFromRequest(r *http.Request, header string) (token, reason string) {
	const missing = "missing bearer token"
	values := r.Header.Values(header)
	if len(values) == 0 {
		return "", missing
	}
	isDefault := strings.EqualFold(header, DefaultCallerAuthHeader)
	if len(values) > 1 && !isDefault {
		return "", "multiple auth headers"
	}
	const prefix = "bearer "
	v := values[0]
	switch {
	case len(v) >= len(prefix) && strings.EqualFold(v[:len(prefix)], prefix):
		v = v[len(prefix):]
	case isDefault:
		return "", missing
	}
	if v = strings.TrimSpace(v); v == "" {
		return "", missing
	}
	return v, ""
}

// IsLoopbackOnlyBind reports whether a bind address is reachable only from the
// local host. Wildcard binds ("", "0.0.0.0", "::") and bare hostnames are
// treated as public, since they may accept remote connections.
func IsLoopbackOnlyBind(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// No port (or malformed); treat the whole value as the host.
		host = address
	}
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.Trim(host, "[]")

	switch host {
	case "", "0.0.0.0", "::":
		// Wildcard binds listen on all interfaces — public.
		return false
	case "localhost":
		return true
	}
	if strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	// A non-loopback hostname may resolve to a routable address; be safe.
	return false
}

// ForwardsAuthorizationHeader reports whether GRAFANA_FORWARD_HEADERS copies the
// incoming Authorization header to Grafana. That conflicts with built-in caller
// auth (which reserves Authorization for the caller token), so the server refuses
// to start when both are set. Proxy mode (no caller token) may keep forwarding it.
func ForwardsAuthorizationHeader() bool {
	return ForwardsHeader(DefaultCallerAuthHeader)
}

// ForwardsHeader reports whether GRAFANA_FORWARD_HEADERS lists name, ignoring case.
func ForwardsHeader(name string) bool {
	for _, forwarded := range forwardHeaderNamesFromEnv() {
		if strings.EqualFold(forwarded, name) {
			return true
		}
	}
	return false
}
