//go:build unit

package main

import (
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
)

func TestValidateGrafanaURLOverridePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, transport string
		cfg             mcpgrafana.GrafanaConfig
		wantError       string
	}{
		{name: "disabled by default", transport: "streamable-http"},
		{name: "allowlist needs opt in", transport: "sse", cfg: mcpgrafana.GrafanaConfig{AllowedGrafanaURLs: []string{"https://grafana.example.com"}}, wantError: "requires --allow-grafana-url-override"},
		{name: "unrestricted without caller auth", transport: "streamable-http", cfg: mcpgrafana.GrafanaConfig{AllowGrafanaURLOverride: true}},
		{name: "restricted with opt in", transport: "sse", cfg: mcpgrafana.GrafanaConfig{AllowGrafanaURLOverride: true, AllowedGrafanaURLs: []string{"https://grafana.example.com"}}},
		{name: "stdio unaffected", transport: "stdio", cfg: mcpgrafana.GrafanaConfig{AllowGrafanaURLOverride: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGrafanaURLOverridePolicy(tc.transport, tc.cfg)
			if tc.wantError == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.wantError)
			}
		})
	}
}

func TestGrafanaURLOverrideFlagPrecedence(t *testing.T) {
	t.Setenv("GRAFANA_ALLOW_URL_OVERRIDE", "true")
	t.Setenv("GRAFANA_ALLOWED_URLS", "https://grafana.example.com")

	fromEnv := grafanaConfig{}
	assert.NoError(t, fromEnv.applyGrafanaURLOverrideEnv(nil))
	assert.True(t, fromEnv.allowURLOverride)
	assert.Equal(t, "https://grafana.example.com", fromEnv.allowedURLs)

	fromFlags := grafanaConfig{allowURLOverride: false, allowedURLs: ""}
	assert.NoError(t, fromFlags.applyGrafanaURLOverrideEnv(map[string]bool{
		"allow-grafana-url-override": true,
		"allowed-grafana-urls":       true,
	}))
	assert.False(t, fromFlags.allowURLOverride)
	assert.Empty(t, fromFlags.allowedURLs)
}

func TestGrafanaURLOverrideInvalidEnv(t *testing.T) {
	t.Setenv("GRAFANA_ALLOW_URL_OVERRIDE", "not-a-boolean")
	var cfg grafanaConfig
	assert.ErrorContains(t, cfg.applyGrafanaURLOverrideEnv(nil), "GRAFANA_ALLOW_URL_OVERRIDE")
}
