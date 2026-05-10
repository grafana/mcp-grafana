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
	case ModeOAuthGrafana, ModeSAML:
		return "", fmt.Errorf("auth mode %q is reserved for a later release; Phase 1 supports only %q and %q", s, ModeNone, ModeOAuthOIDC)
	default:
		return "", fmt.Errorf("unknown auth mode %q (valid: %q, %q)", s, ModeNone, ModeOAuthOIDC)
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
	switch c.RBACGating {
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
