package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"go.opentelemetry.io/otel"
	"golang.org/x/oauth2"
)

// OIDCUpstream is Mode oauth-oidc: a generic OIDC-compliant IdP authenticates
// the user; the resulting identity is bound to a session that will be
// completed via the SA-token bootstrap flow.
type OIDCUpstream struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config

	mu       sync.Mutex
	pendings map[string]*oidcPending // state -> pending PKCE verifier
}

type oidcPending struct {
	verifier string
	nonce    string
}

// NewOIDCUpstream performs OIDC discovery and returns a usable upstream.
func NewOIDCUpstream(ctx context.Context, cfg Config) (*OIDCUpstream, error) {
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	scopes := cfg.OIDCScopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	}
	oauth := &oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  cfg.PublicURL + "/callback",
		Scopes:       scopes,
	}
	return &OIDCUpstream{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID}),
		oauth:    oauth,
		pendings: make(map[string]*oidcPending),
	}, nil
}

func (u *OIDCUpstream) Mode() Mode { return ModeOAuthOIDC }

// AuthorizeURL stores a per-state PKCE verifier and returns the upstream
// authorize URL with PKCE + nonce parameters. If redirectURI is non-empty
// it overrides the configured RedirectURL for this flow; otherwise the
// configured value (set during NewOIDCUpstream) is used.
func (u *OIDCUpstream) AuthorizeURL(redirectURI, state string) string {
	verifier := randURL(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	nonce := randURL(16)

	u.mu.Lock()
	u.pendings[state] = &oidcPending{verifier: verifier, nonce: nonce}
	u.mu.Unlock()

	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oidc.Nonce(nonce),
	}
	if redirectURI != "" {
		opts = append(opts, oauth2.SetAuthURLParam("redirect_uri", redirectURI))
	}
	return u.oauth.AuthCodeURL(state, opts...)
}

// HandleCallback exchanges the upstream code, validates the ID token, and
// returns the user's canonical identity.
func (u *OIDCUpstream) HandleCallback(ctx context.Context, params url.Values) (Identity, []byte, bool, error) {
	state := params.Get("state")
	code := params.Get("code")
	if state == "" || code == "" {
		return Identity{}, nil, false, fmt.Errorf("missing state or code")
	}
	u.mu.Lock()
	p, ok := u.pendings[state]
	if ok {
		delete(u.pendings, state)
	}
	u.mu.Unlock()
	if !ok {
		return Identity{}, nil, false, fmt.Errorf("unknown or replayed state")
	}

	exchangeCtx, span := otel.Tracer("mcp-grafana-auth").Start(ctx, "auth.upstream_token_exchange")
	tok, err := u.oauth.Exchange(exchangeCtx, code, oauth2.SetAuthURLParam("code_verifier", p.verifier))
	if err != nil {
		span.RecordError(err)
	}
	span.End()
	if err != nil {
		return Identity{}, nil, false, fmt.Errorf("token exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return Identity{}, nil, false, fmt.Errorf("upstream returned no id_token")
	}
	idTok, err := u.verifier.Verify(ctx, rawID)
	if err != nil {
		return Identity{}, nil, false, fmt.Errorf("verify id_token: %w", err)
	}
	if idTok.Nonce != p.nonce {
		return Identity{}, nil, false, fmt.Errorf("nonce mismatch")
	}
	if idTok.Subject == "" {
		return Identity{}, nil, false, fmt.Errorf("id_token has no sub")
	}
	return Identity{Mode: ModeOAuthOIDC, ID: idTok.Subject}, nil, false, nil
}

// randURL returns a base64url-no-pad random string of length n bytes.
func randURL(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("rng: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// stateToken returns a hex random state value.
func stateToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
