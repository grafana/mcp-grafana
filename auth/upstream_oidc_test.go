package auth

import (
	"net/url"
	"testing"

	"golang.org/x/oauth2"
)

// TestOIDCUpstream_AuthorizeURL_HonorsRedirectURIParam confirms that the
// caller-supplied redirectURI is propagated to the upstream as the
// redirect_uri query parameter, not silently discarded in favour of the
// configured RedirectURL. The Upstream interface contract names this
// argument; honouring it lets callers override per-flow.
func TestOIDCUpstream_AuthorizeURL_HonorsRedirectURIParam(t *testing.T) {
	// Construct OIDCUpstream directly (no network dependency on a real OIDC
	// IdP) so we can exercise AuthorizeURL in isolation.
	up := &OIDCUpstream{
		oauth: &oauth2.Config{
			ClientID:    "mcp",
			RedirectURL: "https://configured.example.com/callback",
			Endpoint:    oauth2.Endpoint{AuthURL: "https://idp.example.com/authorize"},
		},
		pendings: newPendingRegistry[*oidcPending](oidcPendingTTL),
	}

	got := up.AuthorizeURL("https://override.example.com/callback", "state-1")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if v := u.Query().Get("redirect_uri"); v != "https://override.example.com/callback" {
		t.Errorf("redirect_uri=%q, want override URL", v)
	}

	got2 := up.AuthorizeURL("", "state-2")
	u2, _ := url.Parse(got2)
	if v := u2.Query().Get("redirect_uri"); v != "https://configured.example.com/callback" {
		t.Errorf("empty override should fall back to configured URL, got %q", v)
	}
}

// Sweep / TTL behaviour is exercised at the registry level in
// pending_registry_test.go; OIDCUpstream just composes pendingRegistry.
