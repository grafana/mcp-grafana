// Requires a Grafana instance running on localhost:3000,
// with a dashboard provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetDashboardPanelQueries_NonPrometheusDatasources validates that
// get_dashboard_panel_queries returns panels for all datasource types,
// not just Prometheus/Loki.
//
// Related issue: https://github.com/grafana/mcp-grafana/issues/585
func TestGetDashboardPanelQueries_NonPrometheusDatasources(t *testing.T) {
	ctx := newTestContext()

	// Create a dashboard with panels using different datasource types
	cloudwatchPanel := map[string]interface{}{
		"id":    10,
		"title": "NLB ProcessedBytes",
		"type":  "timeseries",
		"datasource": map[string]interface{}{
			"type": "cloudwatch",
			"uid":  "cloudwatch-test",
		},
		"targets": []interface{}{
			map[string]interface{}{
				"refId":      "A",
				"namespace":  "AWS/NetworkELB",
				"metricName": "ProcessedBytes",
				"dimensions": map[string]interface{}{
					"LoadBalancer": "net/my-nlb/abc123",
				},
				"statistic": "Sum",
				"region":    "us-east-1",
				"period":    "",
				"datasource": map[string]interface{}{
					"type": "cloudwatch",
					"uid":  "cloudwatch-test",
				},
			},
		},
		"gridPos": map[string]interface{}{
			"h": 8, "w": 12, "x": 0, "y": 0,
		},
	}

	elasticsearchPanel := map[string]interface{}{
		"id":    11,
		"title": "Log Count",
		"type":  "timeseries",
		"datasource": map[string]interface{}{
			"type": "elasticsearch",
			"uid":  "elasticsearch-test",
		},
		"targets": []interface{}{
			map[string]interface{}{
				"refId": "A",
				"query": "level:error AND service:api",
				"metrics": []interface{}{
					map[string]interface{}{
						"type": "count",
						"id":   "1",
					},
				},
				"bucketAggs": []interface{}{
					map[string]interface{}{
						"type": "date_histogram",
						"id":   "2",
						"settings": map[string]interface{}{
							"interval": "auto",
						},
					},
				},
				"datasource": map[string]interface{}{
					"type": "elasticsearch",
					"uid":  "elasticsearch-test",
				},
			},
		},
		"gridPos": map[string]interface{}{
			"h": 8, "w": 12, "x": 12, "y": 0,
		},
	}

	prometheusPanel := map[string]interface{}{
		"id":    12,
		"title": "CPU Usage",
		"type":  "timeseries",
		"datasource": map[string]interface{}{
			"type": "prometheus",
			"uid":  "prometheus-test",
		},
		"targets": []interface{}{
			map[string]interface{}{
				"refId": "A",
				"expr":  "rate(container_cpu_usage_seconds_total[5m])",
				"datasource": map[string]interface{}{
					"type": "prometheus",
					"uid":  "prometheus-test",
				},
			},
		},
		"gridPos": map[string]interface{}{
			"h": 8, "w": 12, "x": 0, "y": 8,
		},
	}

	dashboardJSON := map[string]interface{}{
		"title": "Panel Queries Multi-Datasource Test",
		"tags":  []string{"integration-test"},
		"panels": []interface{}{
			cloudwatchPanel,
			elasticsearchPanel,
			prometheusPanel,
		},
	}

	// Create the dashboard
	createResult, err := updateDashboard(ctx, UpdateDashboardParams{
		Dashboard: dashboardJSON,
		Message:   "test dashboard with mixed datasource panels",
		Overwrite: true,
		UserID:    1,
	})
	require.NoError(t, err)
	require.NotNil(t, createResult.UID)

	createdUID := *createResult.UID

	// Now query the panel queries
	result, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{
		UID: createdUID,
	})
	require.NoError(t, err)

	// Collect which datasource types were returned
	datasourceTypes := make(map[string]bool)
	panelTitles := make(map[string]bool)
	for _, pq := range result {
		datasourceTypes[pq.Datasource.Type] = true
		panelTitles[pq.Title] = true
	}

	// The Prometheus panel should always be returned
	assert.True(t, panelTitles["CPU Usage"],
		"Prometheus panel 'CPU Usage' should be present in results")
	assert.True(t, datasourceTypes["prometheus"],
		"Prometheus datasource type should be present")

	// These assertions document the bug: CloudWatch and Elasticsearch panels
	// are currently omitted because the code only looks for the 'expr' field.
	// When this bug is fixed, these assertions should pass.
	assert.True(t, panelTitles["NLB ProcessedBytes"],
		"CloudWatch panel 'NLB ProcessedBytes' should be present in results (bug: currently omitted because target has no 'expr' field)")
	assert.True(t, panelTitles["Log Count"],
		"Elasticsearch panel 'Log Count' should be present in results (bug: currently omitted because target has no 'expr' field)")

	assert.True(t, datasourceTypes["cloudwatch"],
		"CloudWatch datasource type should be present in results")
	assert.True(t, datasourceTypes["elasticsearch"],
		"Elasticsearch datasource type should be present in results")

	// We created 3 panels with 1 target each, so we should get 3 results
	assert.Len(t, result, 3,
		"Should return queries for all 3 panels (prometheus + cloudwatch + elasticsearch), got %d", len(result))
}
