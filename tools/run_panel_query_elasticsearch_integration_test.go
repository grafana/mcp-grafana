//go:build integration

package tools

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const esIntegrationDashboardUID = "es-panel-test"

// createElasticsearchTestDashboard creates a dashboard with panels on the provisioned
// Elasticsearch datasource, which holds the docs from testdata/elasticsearch-seed.sh, and
// removes it when the test ends.
func createElasticsearchTestDashboard(t *testing.T) {
	t.Helper()
	ctx := newTestContext()
	panel := func(id int, title, query string, metrics, bucketAggs []interface{}) map[string]interface{} {
		return map[string]interface{}{
			"id":         id,
			"title":      title,
			"type":       "timeseries",
			"datasource": map[string]interface{}{"uid": "elasticsearch", "type": elasticsearchDatasourceType},
			"targets": []interface{}{map[string]interface{}{
				"refId": "A", "query": query, "timeField": "@timestamp", "metrics": metrics, "bucketAggs": bucketAggs,
			}},
		}
	}
	count := []interface{}{map[string]interface{}{"id": "1", "type": "count"}}
	histogram := []interface{}{map[string]interface{}{
		"id": "2", "type": "date_histogram", "field": "@timestamp", "settings": map[string]interface{}{"interval": "auto"},
	}}
	rawData := []interface{}{map[string]interface{}{"id": "1", "type": "raw_data", "settings": map[string]interface{}{"size": "10"}}}

	dashJSON := map[string]interface{}{
		"uid":   esIntegrationDashboardUID,
		"title": "Elasticsearch Panel Query Integration Test",
		"panels": []interface{}{
			panel(1, "Requests per service", "service:$service", count, histogram),
			panel(2, "Logs by level", "level:${level}", rawData, []interface{}{}),
		},
		"templating": map[string]interface{}{"list": []interface{}{
			map[string]interface{}{"name": "service", "type": "custom", "multi": true,
				"current": map[string]interface{}{"value": []interface{}{"api-gateway", "auth-service"}}},
			map[string]interface{}{"name": "level", "type": "custom",
				"current": map[string]interface{}{"value": "error"}},
		}},
	}
	raw, err := json.Marshal(dashJSON)
	require.NoError(t, err)
	var dashboard map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &dashboard))

	_, err = updateDashboard(ctx, UpdateDashboardParams{Dashboard: dashboard, Overwrite: true})
	require.NoError(t, err)
	t.Cleanup(func() {
		c := mcpgrafana.GrafanaClientFromContext(ctx)
		_, _ = c.Dashboards.DeleteDashboardByUID(esIntegrationDashboardUID)
	})
}

// esFrame returns refId A's only frame.
func esFrame(t *testing.T, panel *PanelQueryResult) *backend.DataResponse {
	t.Helper()
	responses, ok := panel.Results.(backend.Responses)
	require.True(t, ok, "expected backend.Responses, got %T", panel.Results)
	r := responses["A"]
	require.Len(t, r.Frames, 1)
	return &r
}

// esCountSum adds up the counts of a date histogram panel.
func esCountSum(t *testing.T, panel *PanelQueryResult) (sum float64, buckets int) {
	t.Helper()
	frame := esFrame(t, panel).Frames[0]
	require.Len(t, frame.Fields, 2, "time and count")
	for i := range frame.Rows() {
		v, ok := frame.Fields[1].ConcreteAt(i)
		require.True(t, ok)
		sum += v.(float64)
	}
	return sum, frame.Rows()
}

func runESPanel(t *testing.T, params RunPanelQueryParams) *PanelQueryResult {
	t.Helper()
	params.DashboardUID = esIntegrationDashboardUID
	result, err := runPanelQuery(newTestContext(), params)
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	return result.Results[params.PanelIDs[0]]
}

func TestRunPanelQuery_Elasticsearch_E2E(t *testing.T) {
	ctx := newTestContext()
	createElasticsearchTestDashboard(t)

	// CI waits for Grafana only; the seed container may still be indexing.
	require.Eventually(t, func() bool {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: esIntegrationDashboardUID, PanelIDs: []int{2}, Start: "now-30d"})
		if err != nil || len(result.Errors) > 0 || result.Results[2] == nil {
			return false
		}
		frames := result.Results[2].Results.(backend.Responses)["A"].Frames
		return len(frames) == 1 && frames[0].Rows() == 2
	}, 2*time.Minute, 2*time.Second, "elasticsearch did not return the seeded documents")

	t.Run("auto date histogram over a long range", func(t *testing.T) {
		// One-second buckets over 30 days exceed Elasticsearch's limit of 65536 buckets.
		panel := runESPanel(t, RunPanelQueryParams{PanelIDs: []int{1}, Start: "now-30d"})
		assert.Equal(t, elasticsearchDatasourceType, panel.DatasourceType)
		assert.Equal(t, `service:("api\-gateway" OR "auth\-service")`, panel.Query)
		sum, buckets := esCountSum(t, panel)
		assert.EqualValues(t, 5, sum, "4 api-gateway and 1 auth-service documents")
		// $__interval over 30 days is 7.2h, rounded to 6h: 120 intervals, so 121 buckets.
		assert.InDelta(t, 121, buckets, 1, "6h buckets, as for $__interval")
	})

	t.Run("variable override", func(t *testing.T) {
		panel := runESPanel(t, RunPanelQueryParams{PanelIDs: []int{1}, Start: "now-30d", Variables: map[string]string{"service": "user-service"}})
		assert.Equal(t, `service:("user\-service")`, panel.Query)
		sum, _ := esCountSum(t, panel)
		assert.EqualValues(t, 1, sum)
	})

	t.Run("raw data panel", func(t *testing.T) {
		panel := runESPanel(t, RunPanelQueryParams{PanelIDs: []int{2}, Start: "now-30d"})
		assert.Equal(t, "level:error", panel.Query)
		frame := esFrame(t, panel).Frames[0]
		assert.Equal(t, 2, frame.Rows(), fmt.Sprintf("two error documents, got %d", frame.Rows()))
	})
}
