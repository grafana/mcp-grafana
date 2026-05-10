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
	"time"

	"golang.org/x/oauth2"
)

// grafanaPendingTTL bounds how long a stored PKCE verifier waits for its
// matching /callback. Aligned with the global pending-flow TTL in
// authorize.go so abandoned flows are reaped on the same cadence.
const grafanaPendingTTL = 15 * time.Minute

// GrafanaUpstream implements the Upstream interface for Mode A
// (oauth-grafana). It treats Grafana's experimental oauth2_server as a
// standard OAuth2 authorization server. The access token returned by the
// upstream IS the credential we use to call Grafana on the user's behalf.
type GrafanaUpstream struct {
	oauth *oauth2.Config

	mu        sync.Mutex
	pendings  map[string]*grafanaPending // state -> PKCE verifier
	lastSwept time.Time
}

type grafanaPending struct {
	verifier  string
	createdAt time.Time
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
			// "openid" makes Grafana's oauth2_server return an id_token whose
			// "sub" we use as the stable identity. Without it identity falls
			// back to a hash of the access token, which rotates on refresh
			// and breaks one-session-per-identity dedup.
			Scopes: []string{"openid", "email", "profile"},
		},
		pendings: make(map[string]*grafanaPending),
	}, nil
}

// sweepPendingsLocked drops verifiers older than grafanaPendingTTL. The
// caller must hold u.mu. Runs at most once per TTL window so the amortised
// per-call cost stays O(1) under sustained traffic.
func (u *GrafanaUpstream) sweepPendingsLocked(now time.Time) {
	if now.Sub(u.lastSwept) < grafanaPendingTTL {
		return
	}
	cutoff := now.Add(-grafanaPendingTTL)
	for k, p := range u.pendings {
		if p.createdAt.Before(cutoff) {
			delete(u.pendings, k)
		}
	}
	u.lastSwept = now
}

func (u *GrafanaUpstream) Mode() Mode { return ModeOAuthGrafana }

// AuthorizeURL stores a per-state PKCE verifier and returns the upstream
// authorize URL. If redirectURI is non-empty it overrides the configured
// RedirectURL for this flow; otherwise the configured value (set during
// NewGrafanaUpstream) is used. Mirrors OIDCUpstream's AuthorizeURL so
// callers that vary the redirect per flow (e.g. multi-tenant deployments)
// get consistent behaviour across upstreams.
func (u *GrafanaUpstream) AuthorizeURL(redirectURI, state string) string {
	verifier := randURL(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	u.mu.Lock()
	u.sweepPendingsLocked(time.Now())
	u.pendings[state] = &grafanaPending{verifier: verifier, createdAt: time.Now()}
	u.mu.Unlock()

	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}
	if redirectURI != "" {
		opts = append(opts, oauth2.SetAuthURLParam("redirect_uri", redirectURI))
	}
	return u.oauth.AuthCodeURL(state, opts...)
}

// HandleCallback exchanges the upstream code and packages the result.
func (u *GrafanaUpstream) HandleCallback(ctx context.Context, params url.Values) (CallbackResult, error) {
	state := params.Get("state")
	code := params.Get("code")
	if state == "" || code == "" {
		return CallbackResult{}, fmt.Errorf("missing state or code")
	}
	u.mu.Lock()
	u.sweepPendingsLocked(time.Now())
	p, ok := u.pendings[state]
	if ok {
		delete(u.pendings, state)
	}
	u.mu.Unlock()
	if !ok {
		return CallbackResult{}, fmt.Errorf("unknown or replayed state")
	}
	// Treat past-TTL entries as missing — caller restarts the flow.
	if time.Since(p.createdAt) > grafanaPendingTTL {
		return CallbackResult{}, fmt.Errorf("pending flow expired")
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
// Identity is intentionally left zero on the result: doRefreshUpstream
// reuses the original session's Identity, and identityFromGrafanaToken's
// access-token-hash fallback would otherwise produce a different value
// every refresh (the access token rotates), which would silently break
// the one-session-per-identity invariant if a future caller started
// trusting result.Identity here.
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

	return CallbackResult{
		HasCred:           true,
		UpstreamCreds:     []byte(tok.AccessToken),
		UpstreamRefresh:   []byte(tok.RefreshToken),
		UpstreamExpiresAt: tok.Expiry,
	}, nil
}

// identityFromGrafanaToken extracts a stable user identifier from a Grafana
// oauth2 token. Grafana's oauth2_server returns access tokens that are
// opaque to the client; the id_token (returned because we request the
// "openid" scope in NewGrafanaUpstream) carries the subject. We check for
// id_token first and fall back to a stable hash of the access token if the
// upstream omits the id_token.
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
