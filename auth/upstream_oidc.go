package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"go.opentelemetry.io/otel"
	"golang.org/x/oauth2"
)

// oidcPendingTTL bounds how long an upstream OIDC pending entry waits for
// its matching /callback. Aligned with pendingFlowTTL/bootstrapTTL so all
// pending maps reap on the same cadence.
const oidcPendingTTL = 15 * time.Minute

// OIDCUpstream is Mode oauth-oidc: a generic OIDC-compliant IdP authenticates
// the user; the resulting identity is bound to a session that will be
// completed via the SA-token bootstrap flow.
type OIDCUpstream struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config

	pendings *pendingRegistry[*oidcPending]
}

type oidcPending struct {
	verifier string
	nonce    string
	// redirectURI is the per-flow redirect_uri override that
	// AuthorizeURL sent to the IdP, if any. The token-exchange call in
	// HandleCallback must echo the same value back; otherwise the IdP
	// rejects with redirect_uri_mismatch (RFC 6749 §4.1.3). Empty
	// means the caller didn't override and the configured RedirectURL
	// applies.
	redirectURI string
}

// NewOIDCUpstream performs OIDC discovery and returns a usable upstream.
func NewOIDCUpstream(ctx context.Context, cfg Config) (*OIDCUpstream, error) {
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	// OIDCScopes is set by the CLI default (--oidc-scopes defaults to
	// "openid profile email"). A programmatic caller that supplies an
	// empty slice gets it through unchanged; the upstream will fail at
	// the OAuth call site with a clear error rather than us papering
	// over a misconfiguration with a hidden second default that could
	// silently diverge from the CLI's.
	oauth := &oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  cfg.PublicURL + "/callback",
		Scopes:       cfg.OIDCScopes,
	}
	return &OIDCUpstream{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID}),
		oauth:    oauth,
		pendings: newPendingRegistry[*oidcPending](oidcPendingTTL),
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

	u.pendings.Store(state, &oidcPending{verifier: verifier, nonce: nonce, redirectURI: redirectURI})

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
func (u *OIDCUpstream) HandleCallback(ctx context.Context, params url.Values) (CallbackResult, error) {
	state := params.Get("state")
	code := params.Get("code")
	if state == "" || code == "" {
		return CallbackResult{}, fmt.Errorf("missing state or code")
	}
	p, ok := u.pendings.Consume(state)
	if !ok {
		return CallbackResult{}, fmt.Errorf("unknown or expired state")
	}

	exchangeCtx, span := otel.Tracer("mcp-grafana-auth").Start(ctx, "auth.upstream_token_exchange")
	exchangeOpts := []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("code_verifier", p.verifier)}
	if p.redirectURI != "" {
		// Echo the same redirect_uri the IdP saw on /authorize. RFC 6749
		// §4.1.3 says the token-exchange call must repeat it; otherwise
		// the IdP rejects with redirect_uri_mismatch.
		exchangeOpts = append(exchangeOpts, oauth2.SetAuthURLParam("redirect_uri", p.redirectURI))
	}
	tok, err := u.oauth.Exchange(exchangeCtx, code, exchangeOpts...)
	if err != nil {
		span.RecordError(err)
	}
	span.End()
	if err != nil {
		return CallbackResult{}, fmt.Errorf("token exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return CallbackResult{}, fmt.Errorf("upstream returned no id_token")
	}
	idTok, err := u.verifier.Verify(ctx, rawID)
	if err != nil {
		return CallbackResult{}, fmt.Errorf("verify id_token: %w", err)
	}
	if idTok.Nonce != p.nonce {
		return CallbackResult{}, fmt.Errorf("nonce mismatch")
	}
	if idTok.Subject == "" {
		return CallbackResult{}, fmt.Errorf("id_token has no sub")
	}
	return CallbackResult{
		Identity: Identity{Mode: ModeOAuthOIDC, ID: idTok.Subject},
		HasCred:  false,
	}, nil
}

// Refresh is not supported in Mode C: SA tokens have user-controlled expiry
// and don't rotate on a known schedule. Returns ErrRefreshNotSupported.
func (u *OIDCUpstream) Refresh(_ context.Context, _ []byte) (CallbackResult, error) {
	return CallbackResult{}, ErrRefreshNotSupported
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
	if _, err := rand.Read(b[:]); err != nil {
		panic("rng: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
