package auth

import (
	"encoding/json"
	"net/http"
	"time"
)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// TokenHandler implements RFC 6749 §3.2 for grant_type=authorization_code and
// grant_type=refresh_token. Public clients (PKCE) only — no client_secret check.
func (s *Server) TokenHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			// RFC 7231 §6.5.5 requires Allow on every 405 response so
			// clients can discover which methods are supported without
			// guessing.
			w.Header().Set("Allow", "POST")
			httpError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
			return
		}
		if err := r.ParseForm(); err != nil {
			httpError(w, http.StatusBadRequest, "invalid_request", "form parse")
			return
		}
		switch r.FormValue("grant_type") {
		case "authorization_code":
			s.handleAuthCodeGrant(w, r)
		case "refresh_token":
			s.handleRefreshGrant(w, r)
		default:
			httpError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		}
	})
}

func (s *Server) handleAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	plain := r.FormValue("code")
	verifier := r.FormValue("code_verifier")
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")

	if plain == "" || verifier == "" || clientID == "" {
		httpError(w, http.StatusBadRequest, "invalid_request", "code, code_verifier, client_id required")
		return
	}
	codeHash := HashToken(plain)
	c, err := s.Store.PeekAuthCode(r.Context(), codeHash)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid_grant", "code unknown or expired")
		return
	}
	if c.ClientID != clientID || c.RedirectURI != redirectURI {
		httpError(w, http.StatusBadRequest, "invalid_grant", "client/redirect mismatch")
		return
	}
	if err := VerifyPKCE(c.CodeChallengeMethod, c.CodeChallenge, verifier); err != nil {
		httpError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		return
	}
	// All checks passed; now consume one-shot. If a concurrent request
	// already burned the code (legitimate-retry race), ConsumeAuthCode
	// returns ErrNotFound and we surface the same "unknown or expired"
	// error — the second caller can't double-mint a session. Use the
	// returned AuthCode (the canonical consumed copy) rather than the
	// earlier Peek so the session is built from the value that was
	// actually atomically removed from the store.
	c, err = s.Store.ConsumeAuthCode(r.Context(), codeHash)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid_grant", "code unknown or expired")
		return
	}

	at, atHash := NewToken()
	rt, rtHash := NewToken()
	now := time.Now()
	atTTL := s.AccessTokenTTL
	if atTTL == 0 {
		atTTL = time.Hour
	}
	rtTTL := s.RefreshTokenTTL
	if rtTTL == 0 {
		rtTTL = 30 * 24 * time.Hour
	}

	// inside PutSession (one-session-per-identity invariant). PutSession
	// reports the replaced TokenHash atomically so we emit a paired
	// SessionRevoked here — using a pre-PutSession GetSessionByIdentity
	// check would race with the middleware's expired-token DeleteSession
	// path and could double-decrement the active-sessions gauge.
	replacedTokenHash, err := s.Store.PutSession(r.Context(), Session{
		TokenHash:         atHash,
		RefreshHash:       rtHash,
		ClientID:          clientID,
		ExpiresAt:         now.Add(atTTL),
		RefreshExpiresAt:  now.Add(rtTTL),
		Identity:          c.Identity,
		UpstreamCredsCT:   c.UpstreamCredsCT,
		UpstreamRefreshCT: c.UpstreamRefreshCT,
		UpstreamExpiresAt: c.UpstreamExpiresAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	if err != nil {
		s.logger().Error("auth.session_persist_failed", "user_id", c.Identity.String(), "error", err.Error())
		httpError(w, http.StatusInternalServerError, "server_error", "session persist failed")
		return
	}

	if replacedTokenHash != "" {
		s.Metrics.SessionRevoked(r.Context(), c.Identity.Mode)
		// Invalidate the previous session's RBAC permission-cache entry,
		// mirroring handleRefreshGrant. Without this, the old hash's
		// snapshot lingers until TTL — unreachable but wasting memory and
		// inconsistent with the refresh path.
		if s.RBAC != nil {
			s.RBAC.InvalidateSessionCache(replacedTokenHash)
		}
	}
	s.Metrics.SessionCreated(r.Context(), c.Identity.Mode)
	s.logger().Info("auth.session_created", "user_id", c.Identity.String(), "mode", string(c.Identity.Mode), "client_id", clientID)
	writeTokenResponse(w, at, rt, atTTL)
}

func (s *Server) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	plain := r.FormValue("refresh_token")
	clientID := r.FormValue("client_id")
	if plain == "" || clientID == "" {
		httpError(w, http.StatusBadRequest, "invalid_request", "refresh_token and client_id required")
		return
	}
	sess, err := s.Store.GetSessionByRefreshHash(r.Context(), HashToken(plain))
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid_grant", "refresh_token unknown")
		return
	}
	// RFC 6749 §10.4: a refresh token must be bound to the client it was
	// issued to. Rejecting a mismatch defends against a malicious DCR
	// client that obtained another client's refresh token (e.g. via a
	// log capture or compromised client storage) attempting to mint
	// fresh access tokens against it.
	if sess.ClientID != "" && sess.ClientID != clientID {
		s.logger().Warn("auth.refresh_client_mismatch",
			"user_id", sess.Identity.String(),
			"session_client_id", sess.ClientID,
			"request_client_id", clientID)
		httpError(w, http.StatusBadRequest, "invalid_grant", "refresh_token issued to a different client")
		return
	}
	if !sess.RefreshExpiresAt.IsZero() && time.Now().After(sess.RefreshExpiresAt) {
		// Same race-gating pattern as the middleware's expired-token branch:
		// emit SessionRevoked only if THIS request actually deleted the row
		// so concurrent refresh attempts against the same expired session
		// can't drive the gauge negative.
		deleted, _ := s.Store.DeleteSession(r.Context(), sess.TokenHash)
		if deleted {
			s.Metrics.SessionRevoked(r.Context(), sess.Identity.Mode)
		}
		httpError(w, http.StatusBadRequest, "invalid_grant", "refresh_token expired")
		return
	}

	// Rotate both tokens. PutSession with the new TokenHash creates a new
	// session row; the MemoryStore's identity-keyed cleanup atomically
	// drops the previous session's secondary-index entries (sessByToken
	// and sessByRefresh under the old hashes) under the same lock. Don't
	// DeleteSession first — if PutSession then fails the old session is
	// already gone and the client can never recover.
	at, atHash := NewToken()
	rt, rtHash := NewToken()
	now := time.Now()
	atTTL := s.AccessTokenTTL
	if atTTL == 0 {
		atTTL = time.Hour
	}
	rtTTL := s.RefreshTokenTTL
	if rtTTL == 0 {
		rtTTL = 30 * 24 * time.Hour
	}
	// PutSession returns the prior session's TokenHash on replacement.
	// Refresh always replaces (one-session-per-identity), but we don't
	// emit SessionRevoked here because the active-session GAUGE is
	// unchanged across a refresh — the user still has exactly one
	// session, just under a different access-token hash. The
	// auth-code grant emits the metric because it can replace a
	// session belonging to the same identity from a different
	// browser/client, which IS a revocation event. The discard is
	// intentional; named explicitly so a future contributor doesn't
	// silently flip the contract.
	replacedTokenHash, err := s.Store.PutSession(r.Context(), Session{
		TokenHash:         atHash,
		RefreshHash:       rtHash,
		ClientID:          sess.ClientID,
		ExpiresAt:         now.Add(atTTL),
		RefreshExpiresAt:  now.Add(rtTTL),
		Identity:          sess.Identity,
		UpstreamCredsCT:   sess.UpstreamCredsCT,
		UpstreamRefreshCT: sess.UpstreamRefreshCT,
		UpstreamExpiresAt: sess.UpstreamExpiresAt,
		CreatedAt:         sess.CreatedAt,
		UpdatedAt:         now,
	})
	if err != nil {
		s.logger().Error("auth.session_persist_failed", "user_id", sess.Identity.String(), "error", err.Error())
		httpError(w, http.StatusInternalServerError, "server_error", "session persist failed")
		return
	}
	_ = replacedTokenHash // refresh is rotation, not revocation; see comment above

	// Invalidate any RBAC permission-cache entry keyed by the old access
	// token. The new token has a new hash, so the old entry would otherwise
	// linger until TTL — and could leak a stale snapshot if the new session's
	// underlying credential ever differed.
	if s.RBAC != nil {
		s.RBAC.InvalidateSessionCache(sess.TokenHash)
	}

	s.logger().Info("auth.session_refreshed", "user_id", sess.Identity.String(), "reason", "mcp_token")
	writeTokenResponse(w, at, rt, atTTL)
}

func writeTokenResponse(w http.ResponseWriter, accessToken, refreshToken string, atTTL time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	// RFC 6749 §5.1 requires BOTH Cache-Control: no-store AND
	// Pragma: no-cache on any response containing tokens. HTTP/1.0
	// intermediaries only understand Pragma; without it a legacy
	// proxy could cache the access + refresh token and serve them
	// to a different client.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_ = json.NewEncoder(w).Encode(tokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(atTTL / time.Second),
		RefreshToken: refreshToken,
	})
}
