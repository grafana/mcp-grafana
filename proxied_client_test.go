//go:build unit
// +build unit

package mcpgrafana

import (
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProxiedClientAuthHeaders(t *testing.T) {
	t.Run("uses obo headers when access and id tokens are set", func(t *testing.T) {
		headers := proxiedClientAuthHeaders(GrafanaConfig{
			AccessToken: "access-token",
			IDToken:     "id-token",
			APIKey:      "api-key",
		})

		assert.Equal(t, "access-token", headers["X-Access-Token"])
		assert.Equal(t, "id-token", headers["X-Grafana-Id"])
		assert.Empty(t, headers["Authorization"])
	})

	t.Run("falls back to bearer api key", func(t *testing.T) {
		headers := proxiedClientAuthHeaders(GrafanaConfig{APIKey: "api-key"})

		assert.Equal(t, "Bearer api-key", headers["Authorization"])
		assert.Empty(t, headers["X-Access-Token"])
		assert.Empty(t, headers["X-Grafana-Id"])
	})

	t.Run("uses id token as bearer when access token is absent", func(t *testing.T) {
		headers := proxiedClientAuthHeaders(GrafanaConfig{IDToken: "user-token"})

		assert.Equal(t, "Bearer user-token", headers["Authorization"])
		assert.Empty(t, headers["X-Access-Token"])
		assert.Empty(t, headers["X-Grafana-Id"])
	})

	t.Run("falls back to basic auth", func(t *testing.T) {
		headers := proxiedClientAuthHeaders(GrafanaConfig{BasicAuth: url.UserPassword("user", "pass")})

		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
		assert.Equal(t, expected, headers["Authorization"])
	})

	t.Run("adds auth proxy identity headers when enabled", func(t *testing.T) {
		headers := proxiedClientAuthHeaders(GrafanaConfig{
			ProxyAuthEnabled: true,
			ProxyUserHeader:  "X-WEBAUTH-USER",
			ProxyEmailHeader: "X-WEBAUTH-EMAIL",
			ProxyNameHeader:  "X-WEBAUTH-NAME",
			ProxyRoleHeader:  "X-WEBAUTH-ROLE",
			AuthenticatedUser: &OAuth2UserInfo{
				Username: "john.doe",
				Email:    "john@example.com",
				Name:     "John Doe",
				Roles:    []string{"Editor", "Viewer"},
			},
		})

		assert.Equal(t, "john.doe", headers["X-WEBAUTH-USER"])
		assert.Equal(t, "john@example.com", headers["X-WEBAUTH-EMAIL"])
		assert.Equal(t, "John Doe", headers["X-WEBAUTH-NAME"])
		assert.Equal(t, "Editor,Viewer", headers["X-WEBAUTH-ROLE"])
	})
}
