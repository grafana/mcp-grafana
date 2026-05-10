// Package auth implements per-user authentication for the HTTP and SSE
// transports of mcp-grafana. Phase 1 supports Mode "oauth-oidc" only.
package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Mode selects which upstream identity provider authenticates users.
type Mode string

const (
	ModeNone         Mode = "none"
	ModeOAuthOIDC    Mode = "oauth-oidc"
	ModeOAuthGrafana Mode = "oauth-grafana" // reserved for Phase 3
	ModeSAML         Mode = "saml"          // reserved for Phase 4
)

// Config is parsed from CLI flags / env vars. Phase 1 fields only.
type Config struct {
	Mode      Mode
	PublicURL string

	// AES-GCM key material; 32 raw bytes after decoding.
	EncryptionKey         []byte
	EncryptionKeyPrevious []byte

	// Empty → in-memory store. Set → file store rooted at this directory.
	StateDir string

	// Allow auth endpoints over plain HTTP. Dev-only.
	AllowInsecure bool

	// TrustForwardedHeaders enables honouring X-Forwarded-For / X-Real-IP /
	// X-Forwarded-Proto from inbound requests. Set ONLY when a header-
	// stripping reverse proxy fronts mcp-grafana — without one, attackers
	// can spoof these per request to bypass per-IP rate limits and the
	// auth-endpoint HTTPS guard.
	TrustForwardedHeaders bool

	// OIDC config (Mode oauth-oidc).
	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCScopes       []string

	// Grafana oauth2_server (Mode A) — when Mode == ModeOAuthGrafana.
	GrafanaOAuth2ClientID     string
	GrafanaOAuth2ClientSecret string
	GrafanaOAuth2IssuerURL    string // typically equal to GRAFANA_URL

	// SAML (Mode S) — when Mode == ModeSAML.
	SAMLIdPMetadataURL    string        // remote IdP metadata; mutually exclusive with file
	SAMLIdPMetadataFile   string        // local IdP metadata XML file
	SAMLSPCertFile        string        // path to SP X.509 cert (PEM)
	SAMLSPKeyFile         string        // path to SP private key (PEM)
	SAMLEntityID          string        // SP entity ID; defaults to ${PublicURL}/saml/metadata
	SAMLNameIDFormat      string        // default urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress
	SAMLAttributeEmail    string        // attribute name to extract user email; default "email"
	SAMLAttributeGroups   string        // attribute name to extract groups; default "groups"
	SAMLAllowIdPInitiated bool          // default false
	SAMLClockSkew         time.Duration // default 60 seconds
	SAMLEnableSLO         bool          // default false; mounts /saml/sls when true

	// RBACGating selects the RBAC tool gating mode.
	// "auto" (default), "enterprise", "basic", or "off".
	//
	// "auto" probes /api/access-control/user/permissions on first use.
	// When the response is non-empty the engine resolves to "enterprise"
	// for the rest of the process lifetime. An empty response is
	// ambiguous (real OSS-Basic install vs. Enterprise where the first
	// observed user happens to have no granted permissions), so "auto"
	// stays unresolved and the tools/list hook fails open — every tool
	// is visible until either some user yields non-empty perms or the
	// operator explicitly sets --rbac-gating=basic. Pure-OSS deployments
	// that want basic-role-only gating should set "basic" explicitly
	// rather than rely on auto-detection.
	RBACGating string

	// RBACCacheTTL is how long a per-session permission snapshot is reused
	// before refetching from Grafana. Default 5 minutes.
	RBACCacheTTL time.Duration
}

// ParseMode maps a CLI string to a Mode value.
func ParseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case ModeNone, "":
		return ModeNone, nil
	case ModeOAuthOIDC:
		return ModeOAuthOIDC, nil
	case ModeOAuthGrafana:
		return ModeOAuthGrafana, nil
	case ModeSAML:
		return ModeSAML, nil
	default:
		return "", fmt.Errorf("unknown auth mode %q (valid: %q, %q, %q, %q)", s, ModeNone, ModeOAuthOIDC, ModeOAuthGrafana, ModeSAML)
	}
}

// Validate ensures all fields required by the chosen mode are populated.
// Returns nil for ModeNone (no auth wiring required).
func (c Config) Validate() error {
	if c.Mode == ModeNone {
		return nil
	}
	if c.PublicURL == "" {
		return errors.New("--public-url is required when --auth-mode is not 'none'")
	}
	if !strings.HasPrefix(c.PublicURL, "https://") && !c.AllowInsecure {
		return fmt.Errorf("--public-url must use https:// (got %q); set --allow-insecure-auth for dev only", c.PublicURL)
	}
	if len(c.EncryptionKey) != 32 {
		return errors.New("--token-encryption-key must decode to exactly 32 bytes")
	}
	if c.EncryptionKeyPrevious != nil && len(c.EncryptionKeyPrevious) != 32 {
		return errors.New("--token-encryption-key-previous, when set, must decode to exactly 32 bytes")
	}
	if c.Mode == ModeOAuthOIDC {
		if c.OIDCIssuerURL == "" {
			return errors.New("--oidc-issuer-url is required for --auth-mode=oauth-oidc")
		}
		if c.OIDCClientID == "" {
			return errors.New("--oidc-client-id is required for --auth-mode=oauth-oidc")
		}
	}
	if c.Mode == ModeOAuthGrafana {
		if c.GrafanaOAuth2IssuerURL == "" {
			return errors.New("--grafana-oauth2-issuer-url is required for --auth-mode=oauth-grafana")
		}
		if c.GrafanaOAuth2ClientID == "" {
			return errors.New("--grafana-oauth2-client-id is required for --auth-mode=oauth-grafana")
		}
		// Mode A treats Grafana's oauth2_server as a confidential client and
		// sends credentials with oauth2.AuthStyleInParams; missing the secret
		// at startup would otherwise surface as a confusing token-exchange
		// failure on the first authorization-code redemption.
		if c.GrafanaOAuth2ClientSecret == "" {
			return errors.New("--grafana-oauth2-client-secret is required for --auth-mode=oauth-grafana")
		}
	}
	if c.Mode == ModeSAML {
		if c.SAMLIdPMetadataURL == "" && c.SAMLIdPMetadataFile == "" {
			return errors.New("--saml-idp-metadata-url or --saml-idp-metadata-file is required for --auth-mode=saml")
		}
		if c.SAMLIdPMetadataURL != "" && c.SAMLIdPMetadataFile != "" {
			return errors.New("--saml-idp-metadata-url and --saml-idp-metadata-file are mutually exclusive")
		}
		if c.SAMLSPCertFile == "" || c.SAMLSPKeyFile == "" {
			return errors.New("--saml-sp-cert-file and --saml-sp-key-file are required for --auth-mode=saml")
		}
	}
	// Normalize the same way rbac.ParseMode does (lower + trim) so a
	// programmatic caller that supplies "Auto" or " auto " isn't
	// rejected at validation while the downstream parser would accept
	// it. The CLI path normalizes at the flag boundary; this guards
	// programmatic callers.
	switch strings.ToLower(strings.TrimSpace(c.RBACGating)) {
	case "", "auto", "enterprise", "basic", "off":
		// ok
	default:
		return fmt.Errorf("--rbac-gating must be one of auto|enterprise|basic|off (got %q)", c.RBACGating)
	}
	if c.RBACCacheTTL < 0 {
		return errors.New("--rbac-cache-ttl must be >= 0")
	}
	return nil
}
