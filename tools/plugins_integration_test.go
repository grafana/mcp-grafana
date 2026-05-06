// Requires a Grafana instance running on localhost:3000.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginTools(t *testing.T) {
	t.Run("installed plugin is detected", func(t *testing.T) {
		// grafana-clickhouse-datasource is provisioned via GF_INSTALL_PLUGINS in docker-compose.yaml.
		ctx := newTestContext()
		result, err := getPlugin(ctx, GetPluginParams{PluginID: "grafana-clickhouse-datasource"})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.Installed, "grafana-clickhouse-datasource should be installed")
		assert.Equal(t, "grafana-clickhouse-datasource", result.PluginID)
		assert.NotEmpty(t, result.Name)
		assert.NotEmpty(t, result.Version)
		assert.Equal(t, "datasource", result.Type)
	})

	t.Run("missing plugin returns installed=false", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getPlugin(ctx, GetPluginParams{PluginID: "grafana-nonexistent-plugin-xyz"})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.Installed)
		assert.Equal(t, "grafana-nonexistent-plugin-xyz", result.PluginID)
		assert.Empty(t, result.Name)
		assert.Empty(t, result.Version)
	})

	t.Run("whitespace in plugin id is trimmed", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getPlugin(ctx, GetPluginParams{PluginID: "  grafana-clickhouse-datasource  "})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.Installed)
		assert.Equal(t, "grafana-clickhouse-datasource", result.PluginID)
	})
}
