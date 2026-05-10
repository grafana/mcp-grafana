package auth

import (
	"context"
	"net/url"
)

// Upstream is the contract every identity-provider implementation satisfies.
// Phase 1 ships one implementation: OIDCUpstream (Mode oauth-oidc).
type Upstream interface {
	// AuthorizeURL returns the URL the user-agent should be redirected to in
	// order to begin upstream authentication. state is opaque; the upstream
	// will return it untouched on callback.
	AuthorizeURL(redirectURI, state string) string

	// HandleCallback consumes the upstream's callback parameters
	// (typically code+state) and returns the canonical user identity. It
	// also returns true if the upstream has supplied an upstream credential
	// directly (Mode A) — Mode C always returns false here, since the
	// credential is bootstrapped separately.
	HandleCallback(ctx context.Context, params url.Values) (Identity, []byte, bool, error)

	// Mode reports the auth mode this upstream represents.
	Mode() Mode
}
