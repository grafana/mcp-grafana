// Package auth implements per-user authentication for the HTTP and SSE
// transports of mcp-grafana. Phase 1 supports Mode "oauth-oidc" only.
package auth

import (
	"errors"
	"fmt"
	"strings"
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

	// OIDC config (Mode oauth-oidc).
	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCScopes       []string
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
	return nil
}

// DecodeKey is implemented in crypto.go (Task 2). Placeholder so the binary
// compiles between tasks.
func DecodeKey(s string) ([]byte, error) {
	return nil, errors.New("crypto not implemented yet (Task 2)")
}
