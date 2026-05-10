package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// refreshWindow is the duration before upstream credential expiry at which the
// middleware proactively refreshes the token.
const refreshWindow = 60 * time.Second

type sessionKeyCtx struct{}

// WithSessionKey stores an opaque session-cache key on the context.
// Used by the RBAC engine to look up the user's permission snapshot.
func WithSessionKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, sessionKeyCtx{}, key)
}

// SessionKeyFromContext returns the session-cache key set by Middleware.
func SessionKeyFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(sessionKeyCtx{}).(string)
	return v, ok && v != ""
}

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

			// Refresh upstream credentials when near expiry (Mode A only;
			// Mode C has zero UpstreamExpiresAt).
			if !sess.UpstreamExpiresAt.IsZero() && time.Until(sess.UpstreamExpiresAt) < refreshWindow {
				updated, err := s.refreshUpstream(r.Context(), sess)
				if err != nil {
					// Proactive refresh: a transient upstream blip while the
					// existing creds are still valid (within window but not
					// yet expired) shouldn't permanently destroy the session.
					// Log and serve with the existing credential; the next
					// request will retry the refresh. Only delete + 401 when
					// the credential has actually expired and we have no
					// usable token to fall back on.
					//
					// Re-read the session from the store before deciding —
					// the refresh call is bounded by refreshUpstreamTimeout
					// (30s), and a singleflight winner whose call took close
					// to that bound could find sess.UpstreamExpiresAt is now
					// in the past even though it was inside the refreshWindow
					// at flight entry. The store also reflects any concurrent
					// successful refresh by a sibling goroutine.
					latest, lerr := s.Store.GetSessionByTokenHash(r.Context(), sess.TokenHash)
					if lerr == nil {
						sess = latest
					}
					if !time.Now().After(sess.UpstreamExpiresAt) {
						s.logger().Warn("auth.upstream_refresh_failed_but_creds_still_valid",
							"user_id", sess.Identity.String(), "error", err.Error(),
							"expires_in", time.Until(sess.UpstreamExpiresAt).String())
					} else {
						// Same race as the expired-token branch above: gate
						// the SessionRevoked decrement on whether THIS request
						// actually deleted the row.
						deleted, _ := s.Store.DeleteSession(r.Context(), sess.TokenHash)
						if deleted {
							s.Metrics.SessionRevoked(r.Context(), sess.Identity.Mode)
						}
						s.unauthorized(w, "invalid_token", "upstream refresh failed")
						return
					}
				} else {
					sess = updated
				}
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
			ctx = WithSessionKey(ctx, sess.TokenHash)

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

// refreshUpstreamTimeout bounds how long the upstream refresh call (run
// inside singleflight) is allowed to take. The work executes on a context
// detached from the winning request so a client disconnect doesn't cancel
// the in-flight refresh and cascade into spurious session deletion for
// every coalesced caller.
const refreshUpstreamTimeout = 30 * time.Second

// refreshUpstream coalesces concurrent upstream refresh calls for the same
// session via singleflight, then delegates to doRefreshUpstream. At most one
// call per session token reaches the upstream at a time; concurrent callers
// share the result. The work runs on a fresh context derived from
// context.Background so a single client disconnect cannot cancel the
// upstream call and revoke the session for all coalesced callers.
func (s *Server) refreshUpstream(_ context.Context, sess Session) (Session, error) {
	v, err, _ := s.refreshGroup.Do(sess.TokenHash, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), refreshUpstreamTimeout)
		defer cancel()
		// After winning the flight, re-read the session from the store —
		// a concurrent refresh may have already updated it.
		fresh, err := s.Store.GetSessionByTokenHash(ctx, sess.TokenHash)
		if err != nil {
			return nil, err
		}
		if !fresh.UpstreamExpiresAt.IsZero() && time.Until(fresh.UpstreamExpiresAt) >= refreshWindow {
			// Another goroutine refreshed while we were waiting.
			return fresh, nil
		}
		return s.doRefreshUpstream(ctx, fresh)
	})
	if err != nil {
		return Session{}, err
	}
	return v.(Session), nil
}

// doRefreshUpstream calls the configured Upstream.Refresh, encrypts the new
// credentials, and persists the updated session. Returns the updated session
// or an error.
func (s *Server) doRefreshUpstream(ctx context.Context, sess Session) (Session, error) {
	if s.Upstream == nil {
		return sess, fmt.Errorf("no upstream configured")
	}
	rt, err := s.Encryptor.Open(sess.UpstreamRefreshCT)
	if err != nil {
		return sess, fmt.Errorf("decrypt refresh token: %w", err)
	}
	result, err := s.Upstream.Refresh(ctx, rt)
	if err != nil {
		return sess, err
	}
	// A refresh that surfaces a different Identity than the original session's
	// would break the one-session-per-identity invariant if persisted, and
	// almost certainly indicates a misbehaving upstream (or an unstable
	// identity derivation, e.g. an access-token-hash fallback). Reject
	// rather than silently rewrite — doRefreshUpstream keeps the existing
	// sess.Identity below, but this guards future regressions and makes
	// the contract explicit.
	if result.Identity != (Identity{}) && result.Identity != sess.Identity {
		return sess, fmt.Errorf("upstream refresh returned different identity %q (session bound to %q)",
			result.Identity.String(), sess.Identity.String())
	}
	credCT, err := s.Encryptor.Seal(result.UpstreamCreds)
	if err != nil {
		return sess, fmt.Errorf("encrypt new creds: %w", err)
	}

	sess.UpstreamCredsCT = credCT
	// Per OAuth 2 §6, upstreams MAY return a new refresh token or omit it
	// (the existing one stays valid). Only re-seal and overwrite the stored
	// ciphertext when the upstream actually returned one — otherwise we'd
	// replace a working refresh token with an encrypted empty value and
	// break the next refresh.
	if len(result.UpstreamRefresh) > 0 {
		refreshCT, err := s.Encryptor.Seal(result.UpstreamRefresh)
		if err != nil {
			return sess, fmt.Errorf("encrypt new refresh: %w", err)
		}
		sess.UpstreamRefreshCT = refreshCT
	}
	sess.UpstreamExpiresAt = result.UpstreamExpiresAt
	sess.UpdatedAt = time.Now()

	if _, err := s.Store.PutSession(ctx, sess); err != nil {
		return sess, fmt.Errorf("persist refreshed session: %w", err)
	}

	if s.RBAC != nil {
		// The MCP session key didn't change, but the underlying credential
		// did. Drop the cached permission snapshot so the next tools/list
		// re-fetches with the new bearer.
		s.RBAC.InvalidateSessionCache(sess.TokenHash)
	}
	return sess, nil
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
