package tools

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildLocalRenderURL(t *testing.T) {
	t.Run("basic dashboard URL", func(t *testing.T) {
		args := RenderPanelLocalParams{
			DashboardUID: "abc123",
		}
		result, err := buildLocalRenderURL("http://localhost:3000", args)
		require.NoError(t, err)
		assert.Contains(t, result, "http://localhost:3000/d/abc123")
		assert.Contains(t, result, "kiosk=")
		assert.Contains(t, result, "refresh=")
	})

	t.Run("with panel ID", func(t *testing.T) {
		panelID := 9
		args := RenderPanelLocalParams{
			DashboardUID: "abc123",
			PanelID:      &panelID,
		}
		result, err := buildLocalRenderURL("http://localhost:3000", args)
		require.NoError(t, err)
		assert.Contains(t, result, "viewPanel=9")
	})

	t.Run("with time range", func(t *testing.T) {
		args := RenderPanelLocalParams{
			DashboardUID: "abc123",
			TimeRange: &RenderTimeRange{
				From: "1773721800000",
				To:   "1773723000000",
			},
		}
		result, err := buildLocalRenderURL("http://localhost:3000", args)
		require.NoError(t, err)
		assert.Contains(t, result, "from=1773721800000")
		assert.Contains(t, result, "to=1773723000000")
	})

	t.Run("with variables", func(t *testing.T) {
		args := RenderPanelLocalParams{
			DashboardUID: "abc123",
			Variables: map[string]string{
				"var-datasource": "Coralogix-Metrics-Dev",
				"var-region":     "dev-2-euc1",
			},
		}
		result, err := buildLocalRenderURL("http://localhost:3000", args)
		require.NoError(t, err)
		assert.Contains(t, result, "var-datasource=Coralogix-Metrics-Dev")
		assert.Contains(t, result, "var-region=dev-2-euc1")
	})

	t.Run("with theme", func(t *testing.T) {
		theme := "light"
		args := RenderPanelLocalParams{
			DashboardUID: "abc123",
			Theme:        &theme,
		}
		result, err := buildLocalRenderURL("http://localhost:3000", args)
		require.NoError(t, err)
		assert.Contains(t, result, "theme=light")
	})

	t.Run("refresh is disabled", func(t *testing.T) {
		args := RenderPanelLocalParams{
			DashboardUID: "abc123",
		}
		result, err := buildLocalRenderURL("http://localhost:3000", args)
		require.NoError(t, err)

		// Verify refresh param exists with empty value (disables auto-refresh)
		u, err := url.Parse(result)
		require.NoError(t, err)
		_, hasRefresh := u.Query()["refresh"]
		assert.True(t, hasRefresh, "refresh param should be present to disable auto-refresh")
	})

	t.Run("kiosk mode enabled", func(t *testing.T) {
		args := RenderPanelLocalParams{
			DashboardUID: "abc123",
		}
		result, err := buildLocalRenderURL("http://localhost:3000", args)
		require.NoError(t, err)

		u, err := url.Parse(result)
		require.NoError(t, err)
		_, hasKiosk := u.Query()["kiosk"]
		assert.True(t, hasKiosk, "kiosk param should be present for clean rendering")
	})

	t.Run("trailing slash stripped from base URL", func(t *testing.T) {
		args := RenderPanelLocalParams{
			DashboardUID: "abc123",
		}
		result, err := buildLocalRenderURL("http://localhost:3000/", args)
		require.NoError(t, err)
		assert.Contains(t, result, "http://localhost:3000/d/abc123")
		assert.NotContains(t, result, "//d/")
	})
}
