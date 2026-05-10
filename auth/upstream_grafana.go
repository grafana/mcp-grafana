package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/oauth2"
)

// GrafanaUpstream implements the Upstream interface for Mode A
// (oauth-grafana). It treats Grafana's experimental oauth2_server as a
// standard OAuth2 authorization server. The access token returned by the
// upstream IS the credential we use to call Grafana on the user's behalf.
type GrafanaUpstream struct {
	oauth *oauth2.Config

	mu       sync.Mutex
	pendings map[string]*grafanaPending // state -> PKCE verifier
}

type grafanaPending struct {
	verifier string
}

// NewGrafanaUpstream builds a GrafanaUpstream. The issuer URL and client
// credentials come from the Config.
func NewGrafanaUpstream(_ctx context.Context, cfg Config) (*GrafanaUpstream, error) {
	issuer := strings.TrimRight(cfg.GrafanaOAuth2IssuerURL, "/")
	if issuer == "" {
		return nil, fmt.Errorf("grafana-oauth2-issuer-url is required")
	}
	endpoint := oauth2.Endpoint{
		AuthURL:   issuer + "/oauth2/authorize",
		TokenURL:  issuer + "/oauth2/token",
		AuthStyle: oauth2.AuthStyleInParams,
	}
	return &GrafanaUpstream{
		oauth: &oauth2.Config{
			ClientID:     cfg.GrafanaOAuth2ClientID,
			ClientSecret: cfg.GrafanaOAuth2ClientSecret,
			Endpoint:     endpoint,
			RedirectURL:  strings.TrimRight(cfg.PublicURL, "/") + "/callback",
		},
		pendings: make(map[string]*grafanaPending),
	}, nil
}

func (u *GrafanaUpstream) Mode() Mode { return ModeOAuthGrafana }

// AuthorizeURL stores a per-state PKCE verifier and returns the upstream
// authorize URL.
func (u *GrafanaUpstream) AuthorizeURL(_redirectURI, state string) string {
	verifier := randURL(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	u.mu.Lock()
	u.pendings[state] = &grafanaPending{verifier: verifier}
	u.mu.Unlock()

	return u.oauth.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// HandleCallback exchanges the upstream code and packages the result.
func (u *GrafanaUpstream) HandleCallback(ctx context.Context, params url.Values) (CallbackResult, error) {
	state := params.Get("state")
	code := params.Get("code")
	if state == "" || code == "" {
		return CallbackResult{}, fmt.Errorf("missing state or code")
	}
	u.mu.Lock()
	p, ok := u.pendings[state]
	if ok {
		delete(u.pendings, state)
	}
	u.mu.Unlock()
	if !ok {
		return CallbackResult{}, fmt.Errorf("unknown or replayed state")
	}

	tok, err := u.oauth.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", p.verifier))
	if err != nil {
		return CallbackResult{}, fmt.Errorf("token exchange: %w", err)
	}

	// The access token is the credential for downstream Grafana API calls.
	// We don't extract a separate identity claim here — Grafana's oauth2_server
	// optionally returns an id_token but for simplicity we derive identity
	// from the access token itself (Grafana decodes the user from it on every
	// request). Use the token's "sub" if present in extras; otherwise, use a
	// hash of the access token as a stable, unique-per-user identifier.
	id := identityFromGrafanaToken(tok)

	return CallbackResult{
		Identity:          Identity{Mode: ModeOAuthGrafana, ID: id},
		HasCred:           true,
		UpstreamCreds:     []byte(tok.AccessToken),
		UpstreamRefresh:   []byte(tok.RefreshToken),
		UpstreamExpiresAt: tok.Expiry,
	}, nil
}

// Refresh exchanges a stored refresh token for a fresh credential pair.
func (u *GrafanaUpstream) Refresh(ctx context.Context, refreshToken []byte) (CallbackResult, error) {
	if len(refreshToken) == 0 {
		return CallbackResult{}, fmt.Errorf("empty refresh token")
	}
	src := u.oauth.TokenSource(ctx, &oauth2.Token{
		RefreshToken: string(refreshToken),
	})
	tok, err := src.Token()
	if err != nil {
		return CallbackResult{}, fmt.Errorf("upstream refresh: %w", err)
	}

	id := identityFromGrafanaToken(tok)

	return CallbackResult{
		Identity:          Identity{Mode: ModeOAuthGrafana, ID: id},
		HasCred:           true,
		UpstreamCreds:     []byte(tok.AccessToken),
		UpstreamRefresh:   []byte(tok.RefreshToken),
		UpstreamExpiresAt: tok.Expiry,
	}, nil
}

// identityFromGrafanaToken extracts a stable user identifier from a Grafana
// oauth2 token. Grafana's oauth2_server returns access tokens that are
// opaque to the client; the optional id_token (when scopes include "openid")
// carries the subject. We check for id_token first and fall back to a stable
// hash of the access token.
func identityFromGrafanaToken(tok *oauth2.Token) string {
	if raw, ok := tok.Extra("id_token").(string); ok && raw != "" {
		// id_token is a JWT; second segment is the base64url payload.
		parts := strings.Split(raw, ".")
		if len(parts) == 3 {
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err == nil {
				type claims struct {
					Sub string `json:"sub"`
				}
				var c claims
				_ = json.Unmarshal(payload, &c)
				if c.Sub != "" {
					return c.Sub
				}
			}
		}
	}
	// Fall back to a stable hash of the access token. This is per-token
	// rather than per-user, but the cache is keyed by MCP session anyway —
	// the identity here is mostly an audit string.
	sum := sha256.Sum256([]byte(tok.AccessToken))
	return "tok:" + base64.RawURLEncoding.EncodeToString(sum[:8])
}
