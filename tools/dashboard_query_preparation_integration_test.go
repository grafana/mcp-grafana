//go:build integration

package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	sqldialect "github.com/grafana/mcp-grafana/v2/tools/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardClickHousePreparationIntegration(t *testing.T) {
	ctx := newTestContext()
	for _, v2 := range []bool{false, true} {
		for _, braced := range []bool{false, true} {
			t.Run(fmt.Sprintf("v2=%t/braced=%t", v2, braced), func(t *testing.T) {
				query := "WITH toDateTime(1704069000) AS ts SELECT $__from AS start_ms, $__to AS end_ms, $__range_ms AS range_ms, $__range_s AS range_s, '$__range' AS range_text, '$__rate_interval' AS rate_interval, '$__interval' AS interval_text, $__interval_ms AS interval_ms WHERE $__timeFilter(ts) LIMIT 1"
				if braced {
					for _, macro := range []string{"__range_ms", "__range_s", "__rate_interval", "__interval_ms", "__interval", "__range", "__from", "__to"} {
						query = strings.ReplaceAll(query, "$"+macro, "${"+macro+"}")
					}
				}
				db := preparationFixture(v2, map[string]interface{}{"name": "unused", "type": "custom"}, query)
				setPreparationDatasource(db, v2, "clickhouse", sqldialect.ClickHouseDatasourceType)
				args := DashboardPanelQueriesParams{Start: "2024-01-01T00:00:00Z", End: "2024-01-01T01:00:00Z"}
				inspected, err := inspectPreparationFixture(ctx, db, v2, args)
				require.NoError(t, err)
				require.Len(t, inspected, 1)
				require.Empty(t, inspected[0].Warnings)
				result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, IsV2: v2, PanelID: 1, Start: args.Start, End: args.End})
				require.NoError(t, err)
				assert.Equal(t, inspected[0].ProcessedQuery, result.Query)
				data, ok := result.Results.(*sqldialect.SQLQueryResult)
				require.True(t, ok)
				require.Len(t, data.Rows, 1)
				row := data.Rows[0]
				for key, want := range map[string]interface{}{"start_ms": 1704067200000, "end_ms": 1704070800000, "range_ms": 3600000, "range_s": 3600, "range_text": "1h", "rate_interval": "1m", "interval_text": "3s", "interval_ms": 3000} {
					assert.Equal(t, fmt.Sprint(want), fmt.Sprint(row[key]), key)
				}
			})
		}
	}
}

// Run against the configured Grafana URL. The same test covers modern Grafana
// and Grafana 9 with cloudWatchDynamicLabels enabled or disabled, using LocalStack.
func TestDashboardCloudWatchPreparationIntegration(t *testing.T) {
	ctx := newTestContext()
	version := mcpgrafana.GrafanaVersion(ctx)
	dynamic, known := mcpgrafana.GrafanaFeatureEnabled(ctx, "cloudWatchDynamicLabels")
	legacy := strings.HasPrefix(version, "9.") && known && !dynamic
	for _, v2 := range []bool{false, true} {
		for labelIndex, label := range []interface{}{nil, nil, "", "chosen-label"} {
			t.Run(fmt.Sprintf("v2=%t/label=%d", v2, labelIndex), func(t *testing.T) {
				nullSuppressesAlias := version == "9.0.0" || version == "9.0.1" || version == "9.0.2"
				aliasUsed := legacy || labelIndex == 0 || (labelIndex == 1 && !nullSuppressesAlias)
				variable := map[string]interface{}{
					"name": "choice", "type": "custom", "current": map[string]interface{}{"value": "$__all"},
					"options": []interface{}{map[string]interface{}{"value": "resolved-alias"}},
				}
				if !aliasUsed {
					// If an inactive alias is scanned, this deliberately unavailable
					// option query will block preparation and fail the test.
					variable["type"] = "query"
					variable["query"] = "SELECT unavailable"
				}
				db := preparationFixture(v2, variable, "")
				setPreparationDatasource(db, v2, "cloudwatch", "cloudwatch")
				target := preparationPanelTarget(db, v2)
				delete(target, "rawSql")
				for k, value := range map[string]interface{}{
					"namespace": "Test/Application", "metricName": "CPUUtilization", "region": "us-east-1",
					"queryMode": "Metrics", "statistic": "Average", "period": "60", "matchExact": true,
					"dimensions": map[string]interface{}{"ServiceName": []interface{}{"test-service"}},
					"alias":      "$choice", "id": "querya", "returnData": true,
				} {
					target[k] = value
				}
				if labelIndex != 0 {
					target["label"] = label
				}
				args := DashboardPanelQueriesParams{Start: "now-1h", End: "now"}
				inspected, err := inspectPreparationFixture(ctx, db, v2, args)
				require.NoError(t, err)
				require.Len(t, inspected, 1)
				require.Empty(t, inspected[0].Warnings)
				assert.Equal(t, aliasUsed, len(inspected[0].RequiredVariables) > 0)
				result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, IsV2: v2, PanelID: 1, Start: args.Start, End: args.End})
				require.NoError(t, err)
				encoded, err := json.Marshal(result.Results)
				require.NoError(t, err)
				var responses map[string]struct {
					Frames []struct{ Schema struct{ Name string } }
				}
				require.NoError(t, json.Unmarshal(encoded, &responses))
				require.NotEmpty(t, responses["A"].Frames, "Grafana must return a frame")
				want := "CPUUtilization"
				if aliasUsed {
					want = "resolved-alias"
				} else if labelIndex == 3 {
					want = "chosen-label"
				}
				for _, frame := range responses["A"].Frames {
					assert.Equal(t, want, frame.Schema.Name)
				}
			})
		}
	}
}
