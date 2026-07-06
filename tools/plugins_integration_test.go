// Requires a Grafana instance running on localhost:3000.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"context"
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

// TestSearchPluginInformation hits the real Grafana.com plugin catalog and does not
// require a local Grafana instance.
func TestSearchPluginInformation(t *testing.T) {
	t.Run("returns results for a known keyword", func(t *testing.T) {
		result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "clickhouse"})

		require.NoError(t, err)
		require.NotEmpty(t, result.Results)
		assert.Greater(t, result.Total, 0)

		var foundGrafana bool
		for _, r := range result.Results {
			if r.PluginID == "grafana-clickhouse-datasource" {
				foundGrafana = true
				assert.Equal(t, "grafana", r.SignatureType)
				assert.NotEmpty(t, r.Version)
				assert.NotEmpty(t, r.Name)
			}
		}
		assert.True(t, foundGrafana, "grafana-clickhouse-datasource should appear in clickhouse results")
	})

	t.Run("grafana-signed plugin ranks before community for same keyword", func(t *testing.T) {
		result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "clickhouse"})

		require.NoError(t, err)
		require.GreaterOrEqual(t, len(result.Results), 1)

		// The first result must be grafana-signed since grafana-clickhouse-datasource exists
		assert.Equal(t, "grafana", result.Results[0].SignatureType)
	})

	t.Run("returns empty results for unknown keyword", func(t *testing.T) {
		result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "xyznonexistentpluginqwerty"})

		require.NoError(t, err)
		assert.Empty(t, result.Results)
		assert.Equal(t, 0, result.Total)
		assert.Empty(t, result.Note)
	})
}

func TestInstallPluginTools(t *testing.T) {
	t.Run("no version prompts for confirmation with latest version from catalog", func(t *testing.T) {
		// grafana-clickhouse-datasource is a known plugin in the catalog; this only
		// hits grafana.com and does not modify the Grafana instance.
		ctx := newTestContext()
		result, err := installPlugin(ctx, InstallPluginParams{PluginID: "grafana-clickhouse-datasource"})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.ConfirmationRequired)
		assert.NotEmpty(t, result.LatestVersion, "should have resolved a version from the catalog")
		assert.Contains(t, result.Message, result.LatestVersion)
	})
}
