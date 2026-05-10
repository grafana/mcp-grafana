package auth

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// CallbackResult carries everything an Upstream needs to communicate from a
// successful upstream authentication.
type CallbackResult struct {
	// Canonical user identity.
	Identity Identity

	// HasCred reports whether the upstream supplied a credential to use
	// directly against Grafana (Mode A). Mode C always reports false (the
	// credential is bootstrapped separately via /bootstrap).
	HasCred bool

	// Credential plaintext (e.g. Grafana-issued access token). Empty when
	// HasCred is false. The caller encrypts this before persisting.
	UpstreamCreds []byte

	// Refresh-token plaintext, if the upstream issued one. May be nil for
	// upstreams that don't support refresh (Mode C).
	UpstreamRefresh []byte

	// When the upstream credential expires. Zero when the credential has
	// no known expiry (e.g. SA tokens in Mode C — those expire only when
	// revoked by the user/admin in Grafana).
	UpstreamExpiresAt time.Time
}

// Upstream is the contract every identity-provider implementation satisfies.
// Phase 1 ships OIDCUpstream (Mode oauth-oidc); Phase 3 adds GrafanaUpstream
// (Mode oauth-grafana).
type Upstream interface {
	// AuthorizeURL returns the URL the user-agent should be redirected to in
	// order to begin upstream authentication. state is opaque; the upstream
	// will return it untouched on callback.
	AuthorizeURL(redirectURI, state string) string

	// HandleCallback consumes the upstream's callback parameters
	// (typically code+state) and returns a CallbackResult describing what
	// the upstream knows about the user and which credentials were issued.
	HandleCallback(ctx context.Context, params url.Values) (CallbackResult, error)

	// Refresh exchanges a refresh token for a new credential pair. It is
	// only meaningful in modes where the upstream issues credentials
	// directly with finite expiry (Mode A). Modes that don't use upstream
	// refresh (Mode C) return ErrRefreshNotSupported.
	Refresh(ctx context.Context, refreshToken []byte) (CallbackResult, error)

	// Mode reports the auth mode this upstream represents.
	Mode() Mode
}

// ErrRefreshNotSupported indicates that the active upstream doesn't perform
// upstream-credential refresh. Returned by Mode C's OIDC upstream.
var ErrRefreshNotSupported = stringError("upstream does not support refresh")

// SAMLValidator is an optional extension implemented by SAML upstreams.
// Server.RegisterRoutes type-asserts the active Upstream to SAMLValidator
// and mounts the /saml/* endpoints when the assertion succeeds.
type SAMLValidator interface {
	// MetadataXML returns the SP entity metadata as XML. Served verbatim
	// at /saml/metadata.
	MetadataXML() ([]byte, error)

	// ValidateAssertion validates a POSTed SAMLResponse on the ACS request,
	// returning identity, attributes, and the original RelayState. The
	// attributes map keys follow the configured attribute names (email,
	// groups). Returns ErrSAMLInvalidAssertion (or wrapped) on any
	// signature, audience, conditions, or replay failure.
	ValidateAssertion(r *http.Request) (samlAssertion, error)

	// BuildLogoutResponseURL parses an inbound LogoutRequest, validates it
	// against the IdP's signing key, and returns the URL the user-agent
	// should be redirected to (the IdP's SLO endpoint with the LogoutResponse).
	// Returns the user identity that should have its session removed.
	BuildLogoutResponseURL(r *http.Request) (Identity, string, error)
}

// samlAssertion is the validated payload returned by SAMLValidator.
type samlAssertion struct {
	Identity   Identity
	Attributes map[string][]string
	RelayState string
}

// ErrSAMLInvalidAssertion indicates a SAMLResponse was rejected during
// signature, audience, conditions, or replay validation.
var ErrSAMLInvalidAssertion = stringError("saml: invalid assertion")
