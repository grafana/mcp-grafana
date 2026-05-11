package auth

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// Middleware returns an HTTP middleware that resolves the Bearer token to a
// session, decrypts the upstream creds, and stores them in the context as
// part of the existing GrafanaConfig.
func (s *Server) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := bearerFrom(r)
			if tok == "" {
				s.unauthorized(w, "", "Bearer token required")
				return
			}
			sess, err := s.Store.GetSessionByTokenHash(r.Context(), HashToken(tok))
			if err != nil {
				s.unauthorized(w, "invalid_token", "unknown access token")
				return
			}
			if !sess.ExpiresAt.IsZero() && time.Now().After(sess.ExpiresAt) {
				// Don't delete the session on access-token expiry. The
				// refresh token is still valid (access TTL is 1h vs refresh
				// TTL 30d by default), and DeleteSession would also drop
				// the sessByRefresh mapping, breaking the standard
				// "401 → use refresh_token → get a new access token" flow.
				// Replacement of the stale access-token-hash index entry
				// happens atomically inside PutSession when refresh runs;
				// SessionRevoked fires there for the replaced hash.
				//
				// Trade-off: a client that gets a 401 and never returns
				// (crashed, lost the refresh token) leaves a session row
				// in memory until — well, today nothing prunes it.
				// A periodic reaper that drops rows past RefreshExpiresAt
				// is the right long-term fix; tracked separately so this
				// refresh-flow correctness fix can ship on its own.
				s.unauthorized(w, "invalid_token", "access token expired")
				return
			}
			pt, err := s.Encryptor.Open(sess.UpstreamCredsCT)
			if err != nil {
				s.unauthorized(w, "invalid_token", "session credential decrypt failed")
				return
			}

			cfg := mcpgrafana.GrafanaConfigFromContext(r.Context())
			cfg.APIKey = string(pt)
			// Pin the Grafana URL to the operator-configured value before any
			// downstream context func (ExtractGrafanaInfoFromHeaders, etc.)
			// has a chance to read X-Grafana-URL. Otherwise a client holding
			// a valid bearer token could redirect the just-decrypted session
			// API key to an attacker-controlled host.
			if s.GrafanaURL != "" {
				cfg.URL = s.GrafanaURL
			}
			ctx := mcpgrafana.WithGrafanaConfig(r.Context(), cfg)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireHTTPS rejects auth-endpoint requests that arrived over plain HTTP.
// A request is considered HTTPS if any of the following is true:
//   - The TLS field on the *http.Request is non-nil (direct TLS termination).
//   - trustForwarded is true AND the X-Forwarded-Proto header is "https".
//
// trustForwarded gates the X-Forwarded-Proto trust under the same opt-in
// as XFF/X-Real-IP rate-limit bucketing — without a proxy that strips
// inbound X-Forwarded-Proto, an attacker can spoof "https" over a plain
// HTTP socket and bypass this guard. When allowInsecure is true the
// guard is a no-op; intended for dev only.
func RequireHTTPS(allowInsecure, trustForwarded bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowInsecure || isSecure(r, trustForwarded) {
				next.ServeHTTP(w, r)
				return
			}
			httpError(w, http.StatusForbidden, "insecure_transport", "auth endpoints require https; set --allow-insecure-auth for dev only")
		})
	}
}

func isSecure(r *http.Request, trustForwarded bool) bool {
	if r.TLS != nil {
		return true
	}
	if trustForwarded && r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return false
}

func bearerFrom(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	// RFC 7235 §2.1: authentication scheme matching is case-insensitive.
	// A client sending "bearer foo" or "BEARER foo" is spec-compliant
	// and should be accepted.
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func (s *Server) unauthorized(w http.ResponseWriter, errCode, desc string) {
	parts := []string{`Bearer realm="mcp-grafana"`, fmt.Sprintf(`resource_metadata="%s/.well-known/oauth-protected-resource"`, s.PublicURL)}
	if errCode != "" {
		parts = append(parts, fmt.Sprintf(`error="%s"`, errCode))
		if desc != "" {
			parts = append(parts, fmt.Sprintf(`error_description="%s"`, desc))
		}
	}
	w.Header().Set("WWW-Authenticate", strings.Join(parts, ", "))
	// Set Content-Type explicitly so clients don't fall back to Go's
	// http.DetectContentType, which returns text/plain for short bodies
	// starting with `{`. httpError sets this elsewhere in the package;
	// the 401 path was the only one missing it.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}
