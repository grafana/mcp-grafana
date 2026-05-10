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
	c, err := s.Store.ConsumeAuthCode(r.Context(), HashToken(plain))
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

	// Re-authentication for the same identity replaces the previous session
	// inside PutSession (one-session-per-identity invariant). Without this
	// pre-check the active-sessions gauge would only ever increment on
	// re-login: PutSession atomically removes the old row, but no
	// SessionRevoked is emitted, so the counter drifts upward. Look up
	// any existing session under the same identity and emit a paired
	// Revoked so the gauge nets to +1 across a full login (not +1 per).
	hadPrev := false
	if _, err := s.Store.GetSessionByIdentity(r.Context(), c.Identity); err == nil {
		hadPrev = true
	}

	if err := s.Store.PutSession(r.Context(), Session{
		TokenHash:        atHash,
		RefreshHash:      rtHash,
		ClientID:         clientID,
		ExpiresAt:        now.Add(atTTL),
		RefreshExpiresAt: now.Add(rtTTL),
		Identity:         c.Identity,
		UpstreamCredsCT:  c.UpstreamCredsCT,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		s.logger().Error("auth.session_persist_failed", "user_id", c.Identity.String(), "error", err.Error())
		httpError(w, http.StatusInternalServerError, "server_error", "session persist failed")
		return
	}

	if hadPrev {
		s.Metrics.SessionRevoked(r.Context(), c.Identity.Mode)
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
	if err := s.Store.PutSession(r.Context(), Session{
		TokenHash:        atHash,
		RefreshHash:      rtHash,
		ClientID:         sess.ClientID,
		ExpiresAt:        now.Add(atTTL),
		RefreshExpiresAt: now.Add(rtTTL),
		Identity:         sess.Identity,
		UpstreamCredsCT:  sess.UpstreamCredsCT,
		CreatedAt:        sess.CreatedAt,
		UpdatedAt:        now,
	}); err != nil {
		s.logger().Error("auth.session_persist_failed", "user_id", sess.Identity.String(), "error", err.Error())
		httpError(w, http.StatusInternalServerError, "server_error", "session persist failed")
		return
	}

	s.logger().Info("auth.session_refreshed", "user_id", sess.Identity.String(), "reason", "mcp_token")
	writeTokenResponse(w, at, rt, atTTL)
}

func writeTokenResponse(w http.ResponseWriter, accessToken, refreshToken string, atTTL time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(tokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(atTTL / time.Second),
		RefreshToken: refreshToken,
	})
}
