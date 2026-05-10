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

	if err := s.Store.PutSession(r.Context(), Session{
		TokenHash:        atHash,
		RefreshHash:      rtHash,
		ExpiresAt:        now.Add(atTTL),
		RefreshExpiresAt: now.Add(rtTTL),
		Identity:         c.Identity,
		UpstreamCredsCT:  c.UpstreamCredsCT,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		httpError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	s.logger().Info("auth.session_created", "user_id", c.Identity.String(), "mode", string(c.Identity.Mode), "client_id", clientID)
	writeTokenResponse(w, at, rt, atTTL)
}

func (s *Server) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	plain := r.FormValue("refresh_token")
	if plain == "" {
		httpError(w, http.StatusBadRequest, "invalid_request", "refresh_token required")
		return
	}
	sess, err := s.Store.GetSessionByRefreshHash(r.Context(), HashToken(plain))
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid_grant", "refresh_token unknown")
		return
	}
	if !sess.RefreshExpiresAt.IsZero() && time.Now().After(sess.RefreshExpiresAt) {
		_ = s.Store.DeleteSession(r.Context(), sess.TokenHash)
		httpError(w, http.StatusBadRequest, "invalid_grant", "refresh_token expired")
		return
	}

	// Rotate both tokens. Delete the old session and write a new one.
	if err := s.Store.DeleteSession(r.Context(), sess.TokenHash); err != nil {
		httpError(w, http.StatusInternalServerError, "server_error", err.Error())
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
	if err := s.Store.PutSession(r.Context(), Session{
		TokenHash:        atHash,
		RefreshHash:      rtHash,
		ExpiresAt:        now.Add(atTTL),
		RefreshExpiresAt: now.Add(rtTTL),
		Identity:         sess.Identity,
		UpstreamCredsCT:  sess.UpstreamCredsCT,
		CreatedAt:        sess.CreatedAt,
		UpdatedAt:        now,
	}); err != nil {
		httpError(w, http.StatusInternalServerError, "server_error", err.Error())
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
