package auth

import "time"

// Identity is the canonical key for a user across all auth modes.
// String representation: "{mode}:{id}", e.g. "oauth-oidc:user@example.com".
type Identity struct {
	Mode Mode
	ID   string // OIDC sub, SAML NameID, or Grafana user id
}

func (i Identity) String() string { return string(i.Mode) + ":" + i.ID }

// Session binds an MCP access token to a user identity and the upstream
// credential we use to call Grafana on their behalf.
type Session struct {
	// Hashed MCP access token (SHA-256). The plaintext token never leaves
	// the response to /token; we only ever match against the hash.
	TokenHash string

	// Hashed refresh token. May be empty for initial-issuance fixtures.
	RefreshHash string

	// ClientID of the DCR client this session was issued to. Used by
	// /token's refresh-token grant to enforce that a refresh token can
	// only be exchanged by the same client that received it (RFC 6749
	// §10.4): a malicious sibling client that captures the refresh token
	// must not be able to mint new access tokens against it.
	ClientID string

	// When the access token expires.
	ExpiresAt time.Time

	// When the refresh token expires.
	RefreshExpiresAt time.Time

	// Canonical identity.
	Identity Identity

	// AES-GCM(SA token) for Mode oauth-oidc.
	// AES-GCM(Grafana access token) for Mode oauth-grafana.
	UpstreamCredsCT []byte

	// AES-GCM(refresh token) for Mode oauth-grafana. nil for Mode oauth-oidc
	// (SA tokens don't rotate).
	UpstreamRefreshCT []byte

	// When the upstream credential expires. Zero for Mode oauth-oidc
	// (SA-token expiry is set by the user in Grafana and not communicated
	// to mcp-grafana). The auth middleware refreshes the credential when
	// non-zero and within 60s of expiry.
	UpstreamExpiresAt time.Time

	// Auditing.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DCRClient is a dynamically-registered MCP client.
type DCRClient struct {
	ClientID         string
	ClientSecretHash string // empty for public PKCE clients
	RedirectURIs     []string
	ClientName       string
	CreatedAt        time.Time
}

// AuthCode is a short-lived one-shot authorization code redeemable at /token.
type AuthCode struct {
	Code                string // hashed when stored
	ClientID            string
	RedirectURI         string
	CodeChallenge       string // PKCE S256 challenge from /authorize
	CodeChallengeMethod string // always "S256"
	Identity            Identity
	UpstreamCredsCT     []byte    // already-encrypted SA token captured at /callback or /bootstrap
	UpstreamRefreshCT   []byte    // Mode A only
	UpstreamExpiresAt   time.Time // Mode A only; zero for Mode C
	ExpiresAt           time.Time
}
