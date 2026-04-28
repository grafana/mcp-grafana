//go:build unit

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---- matchesAuthIntent ----

func TestMatchesAuthIntent_Blocked(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		// datasourceOrGrafanaContext + authSetupVerbThenAuthPhrase
		{"add auth to prometheus", "add authentication to the prometheus datasource"},
		{"configure auth for grafana", "configure authentication for grafana"},
		{"enable credentials loki", "enable credentials for loki"},
		{"set up basic auth grafana", "set up basic auth for grafana"},
		{"implement auth datasource", "implement auth for my datasource"},
		{"turn on auth elasticsearch", "turn on authentication for elasticsearch"},

		// datasourceOrGrafanaContext + authPhraseTowardBackend
		{"auth for prometheus", "auth for the prometheus datasource"},
		{"authentication to grafana", "authentication to grafana"},
		{"auth on loki datasource", "auth on loki datasource"},
		{"authentication with tempo", "authentication with tempo"},

		// authIntentPatterns[0]: add … authentication
		{"add authentication", "add authentication"},
		{"add basic authentication", "add basic authentication"},
		{"add mTLS authentication", "add mTLS authentication"},

		// authIntentPatterns[1]: add/enable/configure/set up/turn on … basic auth
		{"add basic auth", "add basic auth"},
		{"enable basic auth", "enable basic auth"},
		{"configure basic authentication", "configure basic authentication"},
		{"set up basic auth", "set up basic auth"},
		{"turn on basic auth", "turn on basic auth"},

		// authIntentPatterns[2]: basic auth to/for/on datasource/grafana/instance
		{"basic auth to datasource", "basic auth to my datasource"},
		{"basic auth for grafana", "basic auth for grafana"},
		{"basic authentication on instance", "basic authentication on instance"},

		// authIntentPatterns[3]: authentication with … username/password/credential
		{"authentication with username", "authentication with username"},
		{"authentication with password", "authentication with password"},
		{"authentication with credential", "authentication with credential"},

		// authIntentPatterns[4]: username and password
		{"username and password", "username and password"},
		{"user name and password", "user name and password"},

		// authIntentPatterns[5]: basic/digest auth … with/using … user/pass/credential
		{"basic auth with user", "basic auth with user"},
		{"digest auth using password", "digest auth using password"},
		{"basic auth with credential", "basic auth with credential"},

		// authIntentPatterns[6]: basic/digest auth … datasource/grafana
		{"basic auth grafana", "basic auth grafana"},
		{"digest auth datasource", "digest auth datasource"},
		{"basic authentication data source", "basic authentication data source"},

		// authIntentPatterns[7]: enable … auth … password/credential/username
		{"enable auth password", "enable auth password"},
		{"enable basic auth with credential", "enable basic auth with credential"},
		{"enable auth with username", "enable auth with username"},

		// authIntentPatterns[8]: configure … credentials/auth … password/token/secret/username
		{"configure credentials with password", "configure credentials with password"},
		{"configure auth with token", "configure auth with token"},
		{"configure authentication with secret", "configure authentication with secret"},
		{"configure credentials with username", "configure credentials with username"},

		// authIntentPatterns[9]: store/save/paste/inject … password/api key/token/secret
		{"store password", "store my password"},
		{"save api key", "save api key"},
		{"paste access token", "paste access token"},
		{"inject bearer token", "inject bearer token"},
		{"paste secret", "paste my secret"},

		// authIntentPatterns[10]: log in with password/username
		{"log in with password", "log in with my password"},
		{"login with username", "login with username"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, matchesAuthIntent(tt.text), "expected auth intent match for: %q", tt.text)
		})
	}
}

func TestMatchesAuthIntent_Allowed(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"empty string", ""},
		{"whitespace", "   "},
		{"list datasources", "list all grafana datasources"},
		{"show prometheus config", "show me the prometheus configuration"},
		{"ask about url", "what is the URL of the loki datasource"},
		{"plain uid", "abc-123"},
		{"datasource name only", "prometheus"},
		{"update url field", "update the url of the datasource"},
		{"get datasource type", "what type is this datasource"},
		{"plain json data key", "httpMethod"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, matchesAuthIntent(tt.text), "unexpected auth intent match for: %q", tt.text)
		})
	}
}

// ---- matchesSecretLike ----

func TestMatchesSecretLike_Blocked(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		// RSA/EC/DSA/OPENSSH private key headers
		{"rsa private key", "-----BEGIN RSA PRIVATE KEY-----"},
		{"ec private key", "-----BEGIN EC PRIVATE KEY-----"},
		{"dsa private key", "-----BEGIN DSA PRIVATE KEY-----"},
		{"openssh private key", "-----BEGIN OPENSSH PRIVATE KEY-----"},
		{"generic private key", "-----BEGIN PRIVATE KEY-----"},
		{"private key block", "-----BEGIN PRIVATE KEY BLOCK-----"},

		// AWS access key IDs (AKIA + 16 uppercase alphanumeric)
		{"aws akia key", "AKIAIOSFODNN7EXAMPLE"},
		{"aws key in sentence", "my key is AKIAIOSFODNN7EXAMPLE and more"},

		// GitHub personal access token (ghp_ + 36 alphanumeric)
		{"github pat", "ghp_abcdefghijklmnopqrstuvwxyz1234567890"},
		{"github pat in sentence", "token: ghp_abcdefghijklmnopqrstuvwxyz1234567890"},

		// GitHub server-to-server token (ghs_ + 36 alphanumeric)
		{"github server token", "ghs_abcdefghijklmnopqrstuvwxyz1234567890"},

		// GitLab PAT (glpat- + 20+ alphanumeric/hyphen)
		{"gitlab pat", "glpat-abcdefghijklmnopqrst"},
		{"gitlab pat long", "glpat-abcdefghijklmnopqrstuvwxyz"},

		// Slack tokens (xox[baprs]- + 10+ alphanumeric/hyphen)
		{"slack bot token", "xoxb-1234567890-abcdefghij"},
		{"slack app token", "xoxa-1234567890-abcdefghij"},
		{"slack user token", "xoxp-1234567890-abcdefghij"},
		{"slack refresh token", "xoxr-1234567890-abcdefghij"},
		{"slack workspace token", "xoxs-1234567890-abcdefghij"},

		// password/passwd/api_key/secret_key/auth_token = value (8+ chars)
		{"password equals", "password=supersecret123"},
		{"passwd colon", "passwd: supersecret"},
		{"api key equals", "api_key=abcdefgh"},
		{"api-key equals", "api-key=abcdefgh"},
		{"secret key equals", "secret_key=abcdefgh"},
		{"auth token equals", "auth_token=abcdefgh"},
		{"password colon spaced", "password: my-long-password"},

		// Bearer token (20+ chars)
		{"bearer token", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
		{"bearer token short long value", "Bearer abcdefghijklmnopqrstu"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, matchesSecretLike(tt.text), "expected secret match for: %q", tt.text)
		})
	}
}

func TestMatchesSecretLike_Allowed(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"empty string", ""},
		{"plain url", "http://prometheus:9090"},
		{"datasource name", "My Prometheus"},
		{"plain uid", "abc-123-uid"},
		{"json data key", "httpMethod"},
		{"short password field", "password=abc"},  // under 8 chars after =
		{"aws key too short", "AKIAIOSFODNN7EXAMP"}, // only 18 chars after AKIA
		{"github pat too short", "ghp_abcdefghijklmnopqrstuvwxyz123456"},  // 35 chars, needs 36
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, matchesSecretLike(tt.text), "unexpected secret match for: %q", tt.text)
		})
	}
}
