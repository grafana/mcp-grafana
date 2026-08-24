//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postgresPanelDashboard builds a minimal dashboard containing a single
// PostgreSQL-backed panel. The rawSql exercises three concerns at once:
//   - ${schema} is a dashboard variable, substituted before dispatch;
//   - $__interval is a frontend macro, substituted by run_panel_query;
//   - $__timeFilter(ts) is a SQL macro, left untouched for the backend plugin.
//
// The target also carries a datasource-specific field (rawQuery) and no format,
// so the test can assert both target preservation and the format default.
func postgresPanelDashboard(dsType string) map[string]interface{} {
	return map[string]interface{}{
		"templating": map[string]interface{}{
			"list": []interface{}{
				map[string]interface{}{
					"name":    "schema",
					"type":    "constant",
					"query":   "public",
					"current": map[string]interface{}{"value": "public"},
				},
			},
		},
		"panels": []interface{}{
			map[string]interface{}{
				"id":    float64(7),
				"title": "Reconciliation counts",
				"type":  "table",
				"datasource": map[string]interface{}{
					"uid":  "postgres-uid",
					"type": dsType,
				},
				"targets": []interface{}{
					map[string]interface{}{
						"refId":    "A",
						"rawSql":   "SELECT count(*) FROM ${schema}.checks WHERE $__timeFilter(ts) /* interval=$__interval */",
						"rawQuery": true,
					},
				},
			},
		},
	}
}

// capturedQuery holds the first query object from a captured /api/ds/query payload.
type capturedQuery struct {
	payload map[string]interface{}
	query   map[string]interface{}
}

// newDSQueryTestServer stands in for Grafana's /api/ds/query endpoint. It records
// the request payload and returns the supplied frames.
func newDSQueryTestServer(t *testing.T, captured *capturedQuery, frames data.Frames) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/ds/query", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		var payload map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		captured.payload = payload
		queries, ok := payload["queries"].([]interface{})
		require.True(t, ok, "payload.queries should be an array")
		require.Len(t, queries, 1)
		captured.query, ok = queries[0].(map[string]interface{})
		require.True(t, ok, "query should be an object")

		w.Header().Set("Content-Type", "application/json")
		resp := backend.QueryDataResponse{
			Responses: backend.Responses{
				"A": backend.DataResponse{Frames: frames},
			},
		}
		b, err := json.Marshal(resp)
		require.NoError(t, err)
		_, _ = w.Write(b)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// TestRunSinglePanelQuery_PostgresDispatch drives the full panel-query path for a
// PostgreSQL panel: dispatch by datasource type, variable and frontend-macro
// substitution, SQL-macro preservation, request construction, target
// preservation, and result parsing. Both the modern and legacy datasource
// identifiers are covered.
func TestRunSinglePanelQuery_PostgresDispatch(t *testing.T) {
	tests := []struct {
		name   string
		dsType string
	}{
		{name: "modern identifier", dsType: "grafana-postgresql-datasource"},
		{name: "legacy identifier", dsType: "postgres"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frames := data.Frames{
				data.NewFrame("",
					data.NewField("region", nil, []string{"us", "eu"}),
					data.NewField("count", nil, []int64{42, 17}),
				),
			}
			var captured capturedQuery
			ts := newDSQueryTestServer(t, &captured, frames)
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

			result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
				DB:         postgresPanelDashboard(tt.dsType),
				PanelID:    7,
				QueryIndex: 0,
				Start:      "now-1h",
				End:        "now",
			})
			require.NoError(t, err)

			// Dispatch: the panel routed through the SQL path and kept its type.
			assert.Equal(t, tt.dsType, result.DatasourceType)
			assert.Equal(t, "postgres-uid", result.DatasourceUID)

			// Result parsing: frames became a tabular SQLQueryResult.
			sqlResult, ok := result.Results.(*SQLQueryResult)
			require.True(t, ok, "expected *SQLQueryResult, got %T", result.Results)
			assert.Equal(t, []string{"region", "count"}, sqlResult.Columns)
			require.Len(t, sqlResult.Rows, 2)
			assert.Equal(t, "us", sqlResult.Rows[0]["region"])
			assert.Equal(t, int64(42), sqlResult.Rows[0]["count"])
			assert.Equal(t, 2, sqlResult.RowCount)

			// Macro/variable handling: the dashboard variable and the frontend
			// $__interval macro were substituted, while the SQL macro survived
			// for the backend plugin to expand.
			assert.Contains(t, sqlResult.ProcessedQuery, "public.checks")
			assert.NotContains(t, sqlResult.ProcessedQuery, "${schema}")
			assert.Contains(t, sqlResult.ProcessedQuery, "$__timeFilter(ts)")
			assert.NotContains(t, sqlResult.ProcessedQuery, "$__interval")

			// Request construction: the query object carries the processed SQL,
			// the resolved datasource, a refId, and the string table format.
			assert.Equal(t, sqlResult.ProcessedQuery, captured.query["rawSql"])
			assert.Equal(t, "A", captured.query["refId"])
			assert.Equal(t, MSSQLFormatTable, captured.query["format"])
			ds, ok := captured.query["datasource"].(map[string]interface{})
			require.True(t, ok)
			assert.Equal(t, "postgres-uid", ds["uid"])
			assert.Equal(t, tt.dsType, ds["type"])

			// Target preservation: datasource-specific fields survive untouched.
			assert.Equal(t, true, captured.query["rawQuery"])

			// The envelope carries the resolved time range.
			assert.NotEmpty(t, captured.payload["from"])
			assert.NotEmpty(t, captured.payload["to"])
		})
	}
}

// TestRunSinglePanelQuery_PostgresEmptyResultHints verifies that an empty
// PostgreSQL result surfaces PostgreSQL-specific hints through the real path.
func TestRunSinglePanelQuery_PostgresEmptyResultHints(t *testing.T) {
	frames := data.Frames{
		data.NewFrame("",
			data.NewField("count", nil, []int64{}),
		),
	}
	var captured capturedQuery
	ts := newDSQueryTestServer(t, &captured, frames)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
		DB:         postgresPanelDashboard("grafana-postgresql-datasource"),
		PanelID:    7,
		QueryIndex: 0,
		Start:      "now-1h",
		End:        "now",
	})
	require.NoError(t, err)

	sqlResult, ok := result.Results.(*SQLQueryResult)
	require.True(t, ok)
	assert.Empty(t, sqlResult.Rows)

	require.NotEmpty(t, result.Hints)
	joined := ""
	for _, h := range result.Hints {
		joined += h + " "
	}
	assert.Contains(t, joined, "PostgreSQL")
	assert.Contains(t, joined, "search_path")
}
