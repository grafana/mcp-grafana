package mcpgrafana

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// RequireBearerToken returns middleware that authenticates the *caller* of the
// MCP server (not the credentials the server uses to reach Grafana). Requests
// must carry "Authorization: Bearer <token>" matching expected, or they are
// rejected with 401 before reaching the MCP handler.
//
// Both sides are SHA-256 hashed and compared in constant time, so neither the
// token's value nor its length leaks via timing. On success the Authorization
// header is stripped so the caller token can never be forwarded to Grafana or
// folded into a cache key (Grafana auth uses its own X-Grafana-* headers).
//
// CORS preflight (OPTIONS) passes through unauthenticated (no credentials, no
// MCP operation). If expected is empty the middleware fails closed and rejects
// every request. Rejections are logged at WARN with request metadata only,
// never the token. A nil logger falls back to slog.Default().
func RequireBearerToken(expected string, logger *slog.Logger) func(http.Handler) http.Handler {
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
			if expected == "" {
				rejectUnauthorized(w, r, logger, "server misconfigured: empty auth token")
				return
			}
			token := bearerTokenFromRequest(r)
			if token == "" {
				rejectUnauthorized(w, r, logger, "missing bearer token")
				return
			}
			presentedHash := sha256.Sum256([]byte(token))
			if subtle.ConstantTimeCompare(presentedHash[:], expectedHash[:]) != 1 {
				rejectUnauthorized(w, r, logger, "invalid bearer token")
				return
			}
			// Authentication succeeded. Strip the caller credential so it can
			// never reach Grafana or a cache key downstream.
			r.Header.Del("Authorization")
			next.ServeHTTP(w, r)
		})
	}
}

// rejectUnauthorized logs a blocked request (metadata and reason only, never the
// token) and writes a 401.
func rejectUnauthorized(w http.ResponseWriter, r *http.Request, logger *slog.Logger, reason string) {
	logger.Warn("Rejected unauthenticated request to MCP endpoint",
		"remote_addr", r.RemoteAddr,
		"method", r.Method,
		"path", r.URL.Path,
		"reason", reason,
	)
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized: "+reason, http.StatusUnauthorized)
}

// bearerTokenFromRequest extracts the token from an "Authorization: Bearer ..."
// header. It returns an empty string when the header is absent or not a bearer
// credential. The scheme match is case-insensitive per RFC 7235.
func bearerTokenFromRequest(r *http.Request) string {
	const prefix = "bearer "
	h := r.Header.Get("Authorization")
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
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
	for _, name := range forwardHeaderNamesFromEnv() {
		if strings.EqualFold(name, "Authorization") {
			return true
		}
	}
	return false
}
