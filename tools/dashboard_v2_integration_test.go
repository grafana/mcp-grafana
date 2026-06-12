// Requires two Grafana instances from docker-compose:
//   - localhost:3000 with dashboardNewLayouts enabled (stores native v2)
//   - localhost:3002 running Grafana 11 (no dashboard.grafana.app v1beta1 API;
//     the tools must fall back to the legacy REST API)
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// minimalV2DashboardJSON returns a minimal dashboard schema v2 body (top-level
// elements/layout) suitable for update_dashboard's full-JSON mode.
func minimalV2DashboardJSON(uid, title string) map[string]interface{} {
	return map[string]interface{}{
		"uid":         uid,
		"title":       title,
		"description": "created by mcp-grafana v2 integration test",
		"tags":        []interface{}{"mcp-v2-int"},
		"timeSettings": map[string]interface{}{
			"from": "now-6h",
			"to":   "now",
		},
		"variables": []interface{}{},
		"elements": map[string]interface{}{
			"panel-1": map[string]interface{}{
				"kind": "Panel",
				"spec": map[string]interface{}{
					"id":        1,
					"title":     "Up",
					"vizConfig": map[string]interface{}{"kind": "VizConfig", "group": "timeseries", "version": "", "spec": map[string]interface{}{}},
					"data": map[string]interface{}{
						"kind": "QueryGroup",
						"spec": map[string]interface{}{
							"queries": []interface{}{
								map[string]interface{}{
									"kind": "PanelQuery",
									"spec": map[string]interface{}{
										"refId":  "A",
										"hidden": false,
										"query": map[string]interface{}{
											"kind":  "DataQuery",
											"group": "prometheus",
											"spec":  map[string]interface{}{"expr": "up"},
										},
									},
								},
							},
							"transformations": []interface{}{},
							"queryOptions":    map[string]interface{}{},
						},
					},
				},
			},
		},
		"layout": map[string]interface{}{
			"kind": "GridLayout",
			"spec": map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{
						"kind": "GridLayoutItem",
						"spec": map[string]interface{}{
							"x": 0, "y": 0, "width": 24, "height": 8,
							"element": map[string]interface{}{"kind": "ElementReference", "name": "panel-1"},
						},
					},
				},
			},
		},
	}
}

func TestDashboardV2(t *testing.T) {
	ctx := newTestContext()
	const uid = "mcp-v2-int-test"

	t.Run("create native v2 dashboard", func(t *testing.T) {
		_, err := updateDashboard(ctx, UpdateDashboardParams{
			Dashboard: minimalV2DashboardJSON(uid, "MCP V2 Integration"),
			Message:   "create v2 dashboard",
			Overwrite: true,
		})
		require.NoError(t, err)
	})

	t.Run("get returns native v2, not lossy v1", func(t *testing.T) {
		res, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid})
		require.NoError(t, err)
		assert.True(t, res.IsV2, "dashboard should be reported as v2")
		assert.Contains(t, res.APIVersion, "v2", "apiVersion should be a v2 variant")

		spec, ok := res.Dashboard.(map[string]interface{})
		require.True(t, ok)
		assert.Contains(t, spec, "elements", "native v2 spec must carry elements")
		assert.NotContains(t, spec, "panels", "must not be down-converted to v1 panels[]")
	})

	t.Run("summary parses v2 elements", func(t *testing.T) {
		summary, err := getDashboardSummary(ctx, GetDashboardSummaryParams{UID: uid})
		require.NoError(t, err)
		assert.Equal(t, "MCP V2 Integration", summary.Title)
		require.Equal(t, 1, summary.PanelCount)
		assert.Equal(t, "Up", summary.Panels[0].Title)
		assert.Equal(t, "timeseries", summary.Panels[0].Type)
	})

	t.Run("panel queries parse v2 data", func(t *testing.T) {
		queries, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{UID: uid})
		require.NoError(t, err)
		require.Len(t, queries, 1)
		assert.Equal(t, "up", queries[0].Query)
		assert.Equal(t, "prometheus", queries[0].Datasource.Type)
	})

	t.Run("patch a v2 panel title keeps it v2", func(t *testing.T) {
		_, err := updateDashboard(ctx, UpdateDashboardParams{
			UID: uid,
			Operations: []PatchOperation{
				{Op: "replace", Path: "$.elements.panel-1.spec.title", Value: "Up (patched)"},
			},
			Message: "patch v2 panel title",
		})
		require.NoError(t, err)

		res, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid})
		require.NoError(t, err)
		assert.True(t, res.IsV2, "dashboard must remain v2 after patch")

		summary, err := getDashboardSummary(ctx, GetDashboardSummaryParams{UID: uid})
		require.NoError(t, err)
		require.Equal(t, 1, summary.PanelCount)
		assert.Equal(t, "Up (patched)", summary.Panels[0].Title)
	})
}

// TestDashboardV1ToV2Replace verifies that replacing a dashboard currently
// stored as classic v1 with a full-JSON v2 body is rejected: Grafana pins the
// stored schema version at creation, so the v2 body would be silently
// down-converted to v1 — we fail closed instead of losing v2 content.
func TestDashboardV1ToV2Replace(t *testing.T) {
	ctx := newTestContext()
	const uid = "mcp-v1-to-v2-test"

	t.Run("create a classic v1 dashboard", func(t *testing.T) {
		_, err := updateDashboard(ctx, UpdateDashboardParams{
			Dashboard: map[string]interface{}{
				"uid":           uid,
				"title":         "MCP V1 to V2",
				"schemaVersion": 39,
				"tags":          []interface{}{"mcp-v1-to-v2"},
				"panels": []interface{}{
					map[string]interface{}{
						"id":      1,
						"title":   "Up",
						"type":    "timeseries",
						"gridPos": map[string]interface{}{"h": 8, "w": 24, "x": 0, "y": 0},
						"targets": []interface{}{map[string]interface{}{"refId": "A", "expr": "up"}},
					},
				},
			},
			Message:   "create classic v1",
			Overwrite: true,
		})
		require.NoError(t, err)

		res, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid})
		require.NoError(t, err)
		require.False(t, res.IsV2, "dashboard should start as classic v1")
	})

	t.Run("replacing it with a v2 body is rejected", func(t *testing.T) {
		_, err := updateDashboard(ctx, UpdateDashboardParams{
			Dashboard: minimalV2DashboardJSON(uid, "MCP V1 to V2 (attempted)"),
			Message:   "attempt v2 replace",
			Overwrite: true,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stored as classic v1")

		// And it remains classic v1, unchanged.
		res, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid})
		require.NoError(t, err)
		assert.False(t, res.IsV2, "dashboard must remain classic v1")
	})
}

// TestDashboardLegacyFallback targets the Grafana 11 instance (:3002), which
// does not serve the dashboard.grafana.app v1beta1 API, confirming the tools
// transparently fall back to the legacy REST API.
func TestDashboardLegacyFallback(t *testing.T) {
	ctx := newTestContextForURL("http://localhost:3002")

	// The capability check must report v1beta1 as unavailable here, so the
	// tools use the legacy API rather than the k8s path.
	k8s := mcpgrafana.KubernetesClientFromContext(ctx)
	require.NotNil(t, k8s)
	assert.False(t, k8s.SupportsGroupVersion(ctx, "dashboard.grafana.app", "v1beta1"),
		"Grafana 11 should not serve v1beta1; tools must use the legacy API")

	// Create a classic v1 dashboard via update_dashboard (full JSON).
	created, err := updateDashboard(ctx, UpdateDashboardParams{
		Dashboard: map[string]interface{}{
			"title": "Legacy Fallback Test",
			"tags":  []interface{}{"mcp-legacy-int"},
			"panels": []interface{}{
				map[string]interface{}{
					"id":         1,
					"title":      "Up",
					"type":       "timeseries",
					"datasource": map[string]interface{}{"type": "prometheus", "uid": "prometheus"},
					"targets":    []interface{}{map[string]interface{}{"refId": "A", "expr": "up"}},
					"gridPos":    map[string]interface{}{"h": 8, "w": 12, "x": 0, "y": 0},
				},
			},
		},
		Message:   "create via legacy API",
		Overwrite: true,
	})
	require.NoError(t, err)
	require.NotNil(t, created.UID)
	uid := *created.UID

	t.Run("get returns classic v1 via legacy", func(t *testing.T) {
		res, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid})
		require.NoError(t, err)
		assert.False(t, res.IsV2, "legacy fetch must be classic v1")
		assert.Empty(t, res.APIVersion, "legacy fetch carries no k8s apiVersion")
		spec, ok := res.Dashboard.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "Legacy Fallback Test", spec["title"])
		assert.Equal(t, uid, spec["uid"])
	})

	t.Run("summary and panel queries work via legacy", func(t *testing.T) {
		summary, err := getDashboardSummary(ctx, GetDashboardSummaryParams{UID: uid})
		require.NoError(t, err)
		assert.Equal(t, 1, summary.PanelCount)

		queries, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{UID: uid})
		require.NoError(t, err)
		require.Len(t, queries, 1)
		assert.Equal(t, "up", queries[0].Query)
	})

	t.Run("patch via legacy", func(t *testing.T) {
		_, err := updateDashboard(ctx, UpdateDashboardParams{
			UID:        uid,
			Operations: []PatchOperation{{Op: "replace", Path: "$.title", Value: "Legacy Patched"}},
			Message:    "patch via legacy",
		})
		require.NoError(t, err)

		res, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid})
		require.NoError(t, err)
		spec, ok := res.Dashboard.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "Legacy Patched", spec["title"])
	})
}
