package tools

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/grafana-openapi-client-go/models"
)

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}

// extractLeftParam parses a Grafana Explore URL and returns the decoded "left" query parameter.
func extractLeftParam(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	leftValues := u.Query()["left"]
	require.Len(t, leftValues, 1, "expected exactly one 'left' param")
	return leftValues[0]
}

func TestGenerateDeeplink(t *testing.T) {
	grafanaCfg := mcpgrafana.GrafanaConfig{
		URL: "http://localhost:3000",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

	t.Run("Dashboard deeplink", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:3000/d/abc123", result)
	})

	t.Run("Panel deeplink", func(t *testing.T) {
		panelID := 5
		params := GenerateDeeplinkParams{
			ResourceType: "panel",
			DashboardUID: stringPtr("dash-123"),
			PanelID:      &panelID,
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:3000/d/dash-123?viewPanel=5", result)
	})

	t.Run("Explore deeplink", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType:  "explore",
			DatasourceUID: stringPtr("prometheus-uid"),
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Contains(t, result, "http://localhost:3000/explore?left=")
		assert.Contains(t, result, "prometheus-uid")

		// Verify the left param is valid JSON with correct structure
		leftJSON := extractLeftParam(t, result)
		var leftObj map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(leftJSON), &leftObj))
		assert.Equal(t, "prometheus-uid", leftObj["datasource"])
		queries, ok := leftObj["queries"].([]interface{})
		require.True(t, ok, "queries should be an array")
		assert.Len(t, queries, 1)
	})

	t.Run("Explore deeplink with queries", func(t *testing.T) {
		rangeTrue := true
		instantTrue := true
		params := GenerateDeeplinkParams{
			ResourceType:  "explore",
			DatasourceUID: stringPtr("prom-uid"),
			Queries: []ExploreQuery{
				{
					Expr:    `up{job="grafana"}`,
					RefID:   "A",
					Range:   &rangeTrue,
					Instant: &instantTrue,
				},
			},
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)

		leftJSON := extractLeftParam(t, result)
		var leftObj map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(leftJSON), &leftObj))

		assert.Equal(t, "prom-uid", leftObj["datasource"])
		queries := leftObj["queries"].([]interface{})
		require.Len(t, queries, 1)
		q := queries[0].(map[string]interface{})
		assert.Equal(t, `up{job="grafana"}`, q["expr"])
		assert.Equal(t, "A", q["refId"])
		assert.Equal(t, "code", q["editorMode"])
		assert.Equal(t, true, q["range"])
		assert.Equal(t, true, q["instant"])
		ds := q["datasource"].(map[string]interface{})
		assert.Equal(t, "prom-uid", ds["uid"])
	})

	t.Run("Explore deeplink with Elasticsearch extraJSON", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType:  "explore",
			DatasourceUID: stringPtr("coralogix-uid"),
			Queries: []ExploreQuery{
				{
					RefID: "A",
					ExtraJSON: map[string]interface{}{
						"query":     `app_class:"scan_status_service" AND message:"to status\: FAILED"`,
						"alias":     "",
						"timeField": "coralogix.timestamp",
						"metrics": []interface{}{
							map[string]interface{}{"type": "count", "id": "1"},
						},
						"bucketAggs": []interface{}{
							map[string]interface{}{
								"id":       "3",
								"type":     "terms",
								"field":    "customer_name.keyword",
								"settings": map[string]interface{}{"min_doc_count": "1", "size": "10", "order": "desc", "orderBy": "_term"},
							},
							map[string]interface{}{
								"id":       "2",
								"type":     "date_histogram",
								"field":    "coralogix.timestamp",
								"settings": map[string]interface{}{"interval": "1d"},
							},
						},
					},
				},
			},
			TimeRange: &TimeRange{
				From: "now-7d",
				To:   "now",
			},
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)

		leftJSON := extractLeftParam(t, result)
		var leftObj map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(leftJSON), &leftObj))

		assert.Equal(t, "coralogix-uid", leftObj["datasource"])
		queries := leftObj["queries"].([]interface{})
		require.Len(t, queries, 1)
		q := queries[0].(map[string]interface{})

		// Should have Elasticsearch-specific fields
		assert.Equal(t, `app_class:"scan_status_service" AND message:"to status\: FAILED"`, q["query"])
		assert.Equal(t, "coralogix.timestamp", q["timeField"])
		assert.NotNil(t, q["metrics"])
		assert.NotNil(t, q["bucketAggs"])

		// Should NOT have PromQL-specific fields
		assert.Nil(t, q["expr"])
		assert.Nil(t, q["editorMode"])
		assert.Nil(t, q["range"])

		// Datasource should be set
		ds := q["datasource"].(map[string]interface{})
		assert.Equal(t, "coralogix-uid", ds["uid"])
	})

	t.Run("Explore deeplink with time range embedded in left param", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType:  "explore",
			DatasourceUID: stringPtr("prom-uid"),
			Queries: []ExploreQuery{
				{Expr: "up"},
			},
			TimeRange: &TimeRange{
				From: "now-6h",
				To:   "now",
			},
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)

		// Time range should be inside the left param, NOT as top-level URL params
		assert.NotContains(t, result, "&from=")
		assert.NotContains(t, result, "&to=")

		leftJSON := extractLeftParam(t, result)
		var leftObj map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(leftJSON), &leftObj))

		rangeObj := leftObj["range"].(map[string]interface{})
		assert.Equal(t, "now-6h", rangeObj["from"])
		assert.Equal(t, "now", rangeObj["to"])
	})

	t.Run("Explore deeplink has no duplicate left params", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType:  "explore",
			DatasourceUID: stringPtr("ds-uid"),
			Queries: []ExploreQuery{
				{Expr: "rate(http_requests_total[5m])"},
			},
			TimeRange: &TimeRange{
				From: "now-1h",
				To:   "now",
			},
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)

		// Parse URL and verify only one "left" parameter exists
		u, err := url.Parse(result)
		require.NoError(t, err)
		leftValues := u.Query()["left"]
		assert.Len(t, leftValues, 1, "should have exactly one 'left' param, got %d", len(leftValues))
	})

	t.Run("With time range", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
			TimeRange: &TimeRange{
				From: "now-1h",
				To:   "now",
			},
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Contains(t, result, "http://localhost:3000/d/abc123")
		assert.Contains(t, result, "from=now-1h")
		assert.Contains(t, result, "to=now")
	})

	t.Run("With additional query params", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
			QueryParams: map[string]string{
				"var-datasource": "prometheus",
				"refresh":        "30s",
			},
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Contains(t, result, "http://localhost:3000/d/abc123")
		assert.Contains(t, result, "var-datasource=prometheus")
		assert.Contains(t, result, "refresh=30s")
	})

	t.Run("Error cases", func(t *testing.T) {
		emptyGrafanaCfg := mcpgrafana.GrafanaConfig{
			URL: "",
		}
		emptyCtx := mcpgrafana.WithGrafanaConfig(context.Background(), emptyGrafanaCfg)
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
		}
		_, err := generateDeeplink(emptyCtx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "grafana url not configured")

		params.ResourceType = "unsupported"
		_, err = generateDeeplink(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported resource type")

		// Test missing dashboardUid for dashboard
		params = GenerateDeeplinkParams{
			ResourceType: "dashboard",
		}
		_, err = generateDeeplink(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "dashboardUid is required")

		// Test missing dashboardUid for panel
		params = GenerateDeeplinkParams{
			ResourceType: "panel",
		}
		_, err = generateDeeplink(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "dashboardUid is required")

		// Test missing panelId for panel
		params = GenerateDeeplinkParams{
			ResourceType: "panel",
			DashboardUID: stringPtr("dash-123"),
		}
		_, err = generateDeeplink(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "panelId is required")

		// Test missing datasourceUid for explore
		params = GenerateDeeplinkParams{
			ResourceType: "explore",
		}
		_, err = generateDeeplink(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "datasourceUid is required")
	})
}

func TestFindDatasourceVariableName(t *testing.T) {
	t.Run("finds datasource variable", func(t *testing.T) {
		dashboard := &models.DashboardFullWithMeta{
			Dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"type": "datasource",
							"name": "datasource",
						},
						map[string]interface{}{
							"type": "query",
							"name": "namespace",
						},
					},
				},
			},
		}

		name, err := findDatasourceVariableName(dashboard)
		require.NoError(t, err)
		assert.Equal(t, "datasource", name)
	})

	t.Run("finds custom-named datasource variable", func(t *testing.T) {
		dashboard := &models.DashboardFullWithMeta{
			Dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"type": "interval",
							"name": "interval",
						},
						map[string]interface{}{
							"type": "datasource",
							"name": "prometheus_source",
						},
					},
				},
			},
		}

		name, err := findDatasourceVariableName(dashboard)
		require.NoError(t, err)
		assert.Equal(t, "prometheus_source", name)
	})

	t.Run("returns empty when no datasource variable", func(t *testing.T) {
		dashboard := &models.DashboardFullWithMeta{
			Dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"type": "query",
							"name": "namespace",
						},
					},
				},
			},
		}

		name, err := findDatasourceVariableName(dashboard)
		require.NoError(t, err)
		assert.Empty(t, name)
	})

	t.Run("returns empty for nil dashboard", func(t *testing.T) {
		name, err := findDatasourceVariableName(nil)
		require.NoError(t, err)
		assert.Empty(t, name)
	})

	t.Run("returns empty for empty templating", func(t *testing.T) {
		dashboard := &models.DashboardFullWithMeta{
			Dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{},
				},
			},
		}

		name, err := findDatasourceVariableName(dashboard)
		require.NoError(t, err)
		assert.Empty(t, name)
	})

	t.Run("dashboard with no datasourceUID skips resolution", func(t *testing.T) {
		// When datasourceUID is not provided for dashboard type,
		// it should generate a plain link without resolution
		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL: "http://localhost:3000",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:3000/d/abc123", result)
		// No var-datasource should be in the URL
		assert.NotContains(t, result, "var-datasource")
	})

	t.Run("explicit var-datasource in queryParams is not overridden", func(t *testing.T) {
		// resolveDatasourceVariable should not override an explicitly set var-datasource
		queryParams := map[string]string{
			"var-datasource": "my-explicit-value",
		}

		// Even if we call resolve, the existing value should be preserved
		result, err := resolveDatasourceVariable(
			context.Background(), // no grafana client in context
			"dash-uid",
			"ds-uid",
			queryParams,
		)
		require.NoError(t, err)
		assert.Equal(t, "my-explicit-value", result["var-datasource"])
	})

	t.Run("nil grafana client gracefully skips resolution", func(t *testing.T) {
		// When there's no Grafana client in context, resolution should be skipped
		result, err := resolveDatasourceVariable(
			context.Background(),
			"dash-uid",
			"ds-uid",
			nil,
		)
		require.NoError(t, err)
		assert.Nil(t, result)
	})
}
