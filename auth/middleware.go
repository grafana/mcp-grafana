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
				s.Metrics.SessionRevoked(sess.Identity.Mode)
				_ = s.Store.DeleteSession(r.Context(), sess.TokenHash)
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
			ctx := mcpgrafana.WithGrafanaConfig(r.Context(), cfg)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireHTTPS rejects auth-endpoint requests that arrived over plain HTTP.
// A request is considered HTTPS if any of the following is true:
//   - The TLS field on the *http.Request is non-nil (direct TLS termination).
//   - The X-Forwarded-Proto header is "https" (terminating proxy in front).
//
// When allowInsecure is true the guard is a no-op; intended for dev only.
func RequireHTTPS(allowInsecure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowInsecure || isSecure(r) {
				next.ServeHTTP(w, r)
				return
			}
			httpError(w, http.StatusForbidden, "insecure_transport", "auth endpoints require https; set --allow-insecure-auth for dev only")
		})
	}
}

func isSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return false
}

func bearerFrom(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, prefix))
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
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}
