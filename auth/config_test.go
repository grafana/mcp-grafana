package auth

import (
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"", ModeNone, false},
		{"none", ModeNone, false},
		{"NONE", ModeNone, false},
		{"oauth-oidc", ModeOAuthOIDC, false},
		{"oauth-grafana", ModeOAuthGrafana, false},
		{"saml", ModeSAML, false},
		{"bogus", "", true},
	} {
		got, err := ParseMode(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseMode(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("ParseMode(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	good := func() Config {
		return Config{
			Mode:          ModeOAuthOIDC,
			PublicURL:     "https://mcp.example.com",
			EncryptionKey: make([]byte, 32),
			OIDCIssuerURL: "https://idp.example.com",
			OIDCClientID:  "abc",
		}
	}
	if err := good().Validate(); err != nil {
		t.Fatalf("good config failed: %v", err)
	}

	mustFail := func(name string, mutate func(*Config), substr string) {
		t.Helper()
		c := good()
		mutate(&c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), substr) {
			t.Errorf("%s: err=%v want substring %q", name, err, substr)
		}
	}

	mustFail("missing public url", func(c *Config) { c.PublicURL = "" }, "public-url")
	mustFail("http public url", func(c *Config) { c.PublicURL = "http://x" }, "https://")
	mustFail("short key", func(c *Config) { c.EncryptionKey = make([]byte, 16) }, "32 bytes")
	mustFail("missing issuer", func(c *Config) { c.OIDCIssuerURL = "" }, "oidc-issuer-url")
	mustFail("missing client id", func(c *Config) { c.OIDCClientID = "" }, "oidc-client-id")

	none := Config{Mode: ModeNone}
	if err := none.Validate(); err != nil {
		t.Errorf("ModeNone should validate without other fields: %v", err)
	}
}

func TestConfigValidate_ModeSAML(t *testing.T) {
	good := func() Config {
		return Config{
			Mode:               ModeSAML,
			PublicURL:          "https://mcp.example.com",
			EncryptionKey:      make([]byte, 32),
			SAMLIdPMetadataURL: "https://idp.example.com/metadata",
			SAMLSPCertFile:     "/etc/mcp/sp.crt",
			SAMLSPKeyFile:      "/etc/mcp/sp.key",
		}
	}
	if err := good().Validate(); err != nil {
		t.Fatalf("good Mode S config failed: %v", err)
	}

	c := good()
	c.SAMLIdPMetadataURL = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "saml-idp-metadata") {
		t.Errorf("expected idp-metadata error, got %v", err)
	}

	c = good()
	c.SAMLIdPMetadataFile = "/etc/mcp/idp.xml" // both set
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutual-exclusion error, got %v", err)
	}

	c = good()
	c.SAMLSPCertFile = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "saml-sp") {
		t.Errorf("expected SP cert error, got %v", err)
	}
}

func TestConfigValidate_ModeOAuthGrafana(t *testing.T) {
	good := func() Config {
		return Config{
			Mode:                      ModeOAuthGrafana,
			PublicURL:                 "https://mcp.example.com",
			EncryptionKey:             make([]byte, 32),
			GrafanaOAuth2IssuerURL:    "https://grafana.example.com",
			GrafanaOAuth2ClientID:     "mcp",
			GrafanaOAuth2ClientSecret: "shh",
		}
	}
	if err := good().Validate(); err != nil {
		t.Fatalf("good Mode A config failed: %v", err)
	}

	c := good()
	c.GrafanaOAuth2IssuerURL = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "grafana-oauth2-issuer-url") {
		t.Errorf("expected issuer-url error, got %v", err)
	}

	c = good()
	c.GrafanaOAuth2ClientID = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "grafana-oauth2-client-id") {
		t.Errorf("expected client-id error, got %v", err)
	}

	c = good()
	c.GrafanaOAuth2ClientSecret = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "grafana-oauth2-client-secret") {
		t.Errorf("expected client-secret error, got %v", err)
	}
}
