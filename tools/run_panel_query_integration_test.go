//go:build integration

package tools

import (
	"encoding/json"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createMacroTestDashboard creates a temporary dashboard with panels that use
// Grafana temporal macros, and returns its UID. The caller should delete it afterward.
func createMacroTestDashboard(t *testing.T) string {
	t.Helper()
	ctx := newTestContext()

	dashJSON := map[string]interface{}{
		"uid":   "macro-test-dashboard",
		"title": "Macro Substitution Integration Test",
		"panels": []interface{}{
			// Panel 1: Prometheus with $__range (unbraced)
			map[string]interface{}{
				"id":    float64(1),
				"title": "Prom $__range",
				"type":  "timeseries",
				"datasource": map[string]interface{}{
					"type": "prometheus",
					"uid":  "prometheus",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"refId": "A",
						"expr":  "increase(up[$__range])",
					},
				},
			},
			// Panel 2: Prometheus with ${__range} (braced)
			map[string]interface{}{
				"id":    float64(2),
				"title": "Prom ${__range} braced",
				"type":  "timeseries",
				"datasource": map[string]interface{}{
					"type": "prometheus",
					"uid":  "prometheus",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"refId": "A",
						"expr":  "increase(up[${__range}])",
					},
				},
			},
			// Panel 3: Prometheus with ${__rate_interval} (braced)
			map[string]interface{}{
				"id":    float64(3),
				"title": "Prom ${__rate_interval} braced",
				"type":  "timeseries",
				"datasource": map[string]interface{}{
					"type": "prometheus",
					"uid":  "prometheus",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"refId": "A",
						"expr":  "rate(up[${__rate_interval}])",
					},
				},
			},
			// Panel 4: Loki metric query with $__range (the main bug)
			map[string]interface{}{
				"id":    float64(4),
				"title": "Loki $__range",
				"type":  "timeseries",
				"datasource": map[string]interface{}{
					"type": "loki",
					"uid":  "loki",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"refId": "A",
						"expr":  `count_over_time({container=~".+"}[$__range])`,
					},
				},
			},
			// Panel 5: Loki metric query with ${__range} (braced)
			map[string]interface{}{
				"id":    float64(5),
				"title": "Loki ${__range} braced",
				"type":  "timeseries",
				"datasource": map[string]interface{}{
					"type": "loki",
					"uid":  "loki",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"refId": "A",
						"expr":  `count_over_time({container=~".+"}[${__range}])`,
					},
				},
			},
			// Panel 6: Loki metric query with ${__rate_interval} (braced)
			map[string]interface{}{
				"id":    float64(6),
				"title": "Loki ${__rate_interval} braced",
				"type":  "timeseries",
				"datasource": map[string]interface{}{
					"type": "loki",
					"uid":  "loki",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"refId": "A",
						"expr":  `rate({container=~".+"}[${__rate_interval}])`,
					},
				},
			},
		},
	}

	raw, err := json.Marshal(dashJSON)
	require.NoError(t, err)

	var dashboard map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &dashboard))

	_, err = updateDashboard(ctx, UpdateDashboardParams{
		Dashboard: dashboard,
		Overwrite: true,
	})
	require.NoError(t, err)

	return "macro-test-dashboard"
}

func deleteMacroTestDashboard(t *testing.T, uid string) {
	t.Helper()
	ctx := newTestContext()
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	_, _ = c.Dashboards.DeleteDashboardByUID(uid)
}

func TestRunPanelQuery_MacroSubstitution_E2E(t *testing.T) {
	uid := createMacroTestDashboard(t)
	defer deleteMacroTestDashboard(t, uid)

	ctx := newTestContext()

	// These tests verify the full runPanelQuery flow: fetch dashboard → find panel →
	// extract query → detect datasource type → substitute macros → execute against
	// real Prometheus/Loki backends. If macros are NOT substituted, the backend
	// returns a parse error and the test fails.

	t.Run("prometheus panel with $__range macro", func(t *testing.T) {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID: uid,
			PanelIDs:     []int{1},
			Start:        "now-1h",
			End:          "now",
		})
		require.NoError(t, err)
		require.Contains(t, result.Results, 1)
		assert.Empty(t, result.Errors)
		assert.Equal(t, "prometheus", result.Results[1].DatasourceType)
		assert.NotNil(t, result.Results[1].Results)
	})

	t.Run("prometheus panel with ${__range} braced macro", func(t *testing.T) {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID: uid,
			PanelIDs:     []int{2},
			Start:        "now-1h",
			End:          "now",
		})
		require.NoError(t, err)
		require.Contains(t, result.Results, 2)
		assert.Empty(t, result.Errors)
		assert.NotNil(t, result.Results[2].Results)
	})

	t.Run("prometheus panel with ${__rate_interval} braced macro", func(t *testing.T) {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID: uid,
			PanelIDs:     []int{3},
			Start:        "now-1h",
			End:          "now",
		})
		require.NoError(t, err)
		require.Contains(t, result.Results, 3)
		assert.Empty(t, result.Errors)
		assert.NotNil(t, result.Results[3].Results)
	})

	t.Run("loki panel with $__range macro (the original bug)", func(t *testing.T) {
		// Before this fix, this would fail with a Loki parse error because
		// executeLokiQuery did not call substituteGrafanaMacros.
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID: uid,
			PanelIDs:     []int{4},
			Start:        "now-1h",
			End:          "now",
		})
		require.NoError(t, err)
		require.Contains(t, result.Results, 4)
		assert.Empty(t, result.Errors)
		assert.Equal(t, "loki", result.Results[4].DatasourceType)
		assert.NotNil(t, result.Results[4].Results)
	})

	t.Run("loki panel with ${__range} braced macro", func(t *testing.T) {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID: uid,
			PanelIDs:     []int{5},
			Start:        "now-1h",
			End:          "now",
		})
		require.NoError(t, err)
		require.Contains(t, result.Results, 5)
		assert.Empty(t, result.Errors)
		assert.NotNil(t, result.Results[5].Results)
	})

	t.Run("loki panel with ${__rate_interval} braced macro", func(t *testing.T) {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID: uid,
			PanelIDs:     []int{6},
			Start:        "now-1h",
			End:          "now",
		})
		require.NoError(t, err)
		require.Contains(t, result.Results, 6)
		assert.Empty(t, result.Errors)
		assert.NotNil(t, result.Results[6].Results)
	})

	t.Run("multiple panels in one call", func(t *testing.T) {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID: uid,
			PanelIDs:     []int{1, 4}, // Prometheus + Loki
			Start:        "now-1h",
			End:          "now",
		})
		require.NoError(t, err)
		assert.Contains(t, result.Results, 1)
		assert.Contains(t, result.Results, 4)
		assert.Empty(t, result.Errors)
	})
}
