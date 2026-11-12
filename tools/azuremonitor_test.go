//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAzureMonitorLimit(t *testing.T) {
	assert.Equal(t, DefaultAzureMonitorLimit, normalizeAzureMonitorLimit(0, MaxAzureLogAnalyticsLimit))
	assert.Equal(t, DefaultAzureMonitorLimit, normalizeAzureMonitorLimit(-1, MaxAzureLogAnalyticsLimit))
	assert.Equal(t, 250, normalizeAzureMonitorLimit(250, MaxAzureLogAnalyticsLimit))
	assert.Equal(t, MaxAzureLogAnalyticsLimit, normalizeAzureMonitorLimit(5000, MaxAzureLogAnalyticsLimit))
	assert.Equal(t, MaxAzureResourceGraphLimit, normalizeAzureMonitorLimit(1000, MaxAzureResourceGraphLimit))
}

func TestKqlWithTake(t *testing.T) {
	assert.Equal(t, "T | where x == 1\n| take 11", kqlWithTake("T | where x == 1", 11))
	assert.Equal(t, "T\n| take 5", kqlWithTake("  T ;\n ", 5))
	assert.Equal(t, "T // trailing comment\n| take 5", kqlWithTake("T // trailing comment", 5))
	assert.Equal(t, "let n = 5;\nT | take n\n| take 3", kqlWithTake("let n = 5;\nT | take n", 3))
}

func TestBuildAzureLogAnalyticsQuery(t *testing.T) {
	ws := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/law"
	q := buildAzureLogAnalyticsQuery(AzureLogAnalyticsQueryParams{
		DatasourceUID: "az-uid",
		Query:         "AppRequests | where Success == false",
		Resources:     []string{ws},
	}, 50)

	assert.Equal(t, "A", q["refId"])
	assert.Equal(t, "Azure Log Analytics", q["queryType"])
	assert.Equal(t, map[string]string{"uid": "az-uid", "type": AzureMonitorDatasourceType}, q["datasource"])
	la := q["azureLogAnalytics"].(map[string]interface{})
	assert.Equal(t, "AppRequests | where Success == false\n| take 51", la["query"])
	assert.Equal(t, []string{ws}, la["resources"])
	assert.Equal(t, "table", la["resultFormat"])
	assert.Equal(t, true, la["dashboardTime"])
	assert.Equal(t, "TimeGenerated", la["timeColumn"])

	q = buildAzureLogAnalyticsQuery(AzureLogAnalyticsQueryParams{Query: "T", Resources: []string{ws}, TimeColumn: "timestamp"}, 1)
	assert.Equal(t, "timestamp", q["azureLogAnalytics"].(map[string]interface{})["timeColumn"])
}

func TestBuildAzureResourceGraphQuery(t *testing.T) {
	q := buildAzureResourceGraphQuery(AzureResourceGraphQueryParams{
		DatasourceUID: "az-uid",
		Query:         "resources | where type == 'microsoft.app/containerapps'",
		Subscriptions: []string{"sub-1"},
	}, 100)

	assert.Equal(t, "Azure Resource Graph", q["queryType"])
	assert.Equal(t, []string{"sub-1"}, q["subscriptions"])
	arg := q["azureResourceGraph"].(map[string]interface{})
	assert.Equal(t, "resources | where type == 'microsoft.app/containerapps'\n| take 101", arg["query"])
	assert.Equal(t, "table", arg["resultFormat"])

	q = buildAzureResourceGraphQuery(AzureResourceGraphQueryParams{DatasourceUID: "az-uid", Query: "resources"}, 1)
	_, ok := q["subscriptions"]
	assert.False(t, ok, "no subscriptions lets Resource Graph use every subscription the credentials can read")
}

func TestAzureMonitorValidation(t *testing.T) {
	ctx := context.Background()

	_, err := queryAzureLogAnalytics(ctx, AzureLogAnalyticsQueryParams{DatasourceUID: "u", Resources: []string{"/r"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query is required")

	_, err = queryAzureLogAnalytics(ctx, AzureLogAnalyticsQueryParams{DatasourceUID: "u", Query: "T"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resources is required")

	_, err = queryAzureLogAnalytics(ctx, AzureLogAnalyticsQueryParams{DatasourceUID: "u", Query: "T", Resources: []string{"/r"}, Start: "now", End: "now-1h"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "end time must be after start time")

	_, err = queryAzureResourceGraph(ctx, AzureResourceGraphQueryParams{DatasourceUID: "u", Query: "  "})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query is required")
}

func TestValidateAzureMonitorKQL(t *testing.T) {
	require.NoError(t, validateAzureMonitorKQL("AppRequests | summarize count() by bin(TimeGenerated, 5m)"))
	require.NoError(t, validateAzureMonitorKQL("AppTraces | where Message has 'renderer'"))

	err := validateAzureMonitorKQL("AppRequests | summarize count() by bin(TimeGenerated, 5m) | render timechart")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "render operator is not supported")
	assert.Error(t, validateAzureMonitorKQL("T\n|RENDER piechart"))

	_, err = queryAzureResourceGraph(context.Background(), AzureResourceGraphQueryParams{DatasourceUID: "u", Query: "resources | summarize count() by type | render barchart"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "render")
}

func newAzureMonitorTestFrame(rows int) *data.Frame {
	names := make([]string, rows)
	counts := make([]int64, rows)
	for i := range rows {
		names[i] = fmt.Sprintf("app-%d", i+1)
		counts[i] = int64(i + 1)
	}
	return data.NewFrame("PrimaryResult",
		data.NewField("name", nil, names),
		data.NewField("count_", nil, counts),
	)
}

func newAzureMonitorMockGrafana(t *testing.T, dsType string, handler http.HandlerFunc) context.Context {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/datasources/uid/") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   1,
				"uid":  strings.TrimPrefix(r.URL.Path, "/api/datasources/uid/"),
				"name": "Azure Monitor",
				"type": dsType,
			})
			return
		}
		if r.URL.Path == "/api/ds/query" && handler != nil {
			handler(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	cfg := mcpgrafana.GrafanaConfig{URL: ts.URL, APIKey: "test-api-key"}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
	return mcpgrafana.WithGrafanaClient(ctx, mcpgrafana.NewGrafanaClient(ctx, ts.URL, "test-api-key", nil))
}

func newAzureMonitorQueryMock(t *testing.T, rows int, gotBody *map[string]interface{}) context.Context {
	t.Helper()
	return newAzureMonitorMockGrafana(t, AzureMonitorDatasourceType, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(gotBody))
		frames := data.Frames{}
		if rows > 0 {
			frames = append(frames, newAzureMonitorTestFrame(rows))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&backend.QueryDataResponse{Responses: backend.Responses{
			"A": backend.DataResponse{Frames: frames},
		}})
	})
}

func TestAzureMonitor_RejectsWrongDatasourceType(t *testing.T) {
	ctx := newAzureMonitorMockGrafana(t, "loki", nil)

	_, err := queryAzureLogAnalytics(ctx, AzureLogAnalyticsQueryParams{DatasourceUID: "u", Query: "T", Resources: []string{"/r"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is of type loki, not "+AzureMonitorDatasourceType)

	_, err = queryAzureResourceGraph(ctx, AzureResourceGraphQueryParams{DatasourceUID: "u", Query: "resources"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not "+AzureMonitorDatasourceType)
}

func TestQueryAzureLogAnalytics_EndToEnd(t *testing.T) {
	var gotBody map[string]interface{}
	ctx := newAzureMonitorQueryMock(t, 2, &gotBody)

	result, err := queryAzureLogAnalytics(ctx, AzureLogAnalyticsQueryParams{
		DatasourceUID: "az-uid",
		Query:         "AppRequests | summarize count_ = count() by name",
		Resources:     []string{"/subscriptions/s/workspaces/law"},
		Start:         "2026-02-02T19:00:00Z",
		End:           "2026-02-02T20:00:00Z",
		Limit:         2,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"name", "count_"}, result.Columns)
	assert.Equal(t, 2, result.RowCount)
	assert.False(t, result.Truncated, "exactly limit rows is not truncation")
	assert.Nil(t, result.Hints)
	assert.Equal(t, "app-1", result.Rows[0]["name"])

	assert.Equal(t, "1770058800000", gotBody["from"])
	assert.Equal(t, "1770062400000", gotBody["to"])
	q := gotBody["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "Azure Log Analytics", q["queryType"])
	la := q["azureLogAnalytics"].(map[string]interface{})
	assert.Equal(t, "AppRequests | summarize count_ = count() by name\n| take 3", la["query"])
	assert.Equal(t, []interface{}{"/subscriptions/s/workspaces/law"}, la["resources"])
	assert.Equal(t, true, la["dashboardTime"])
}

func TestQueryAzureLogAnalytics_Truncated(t *testing.T) {
	var gotBody map[string]interface{}
	ctx := newAzureMonitorQueryMock(t, 3, &gotBody)

	result, err := queryAzureLogAnalytics(ctx, AzureLogAnalyticsQueryParams{DatasourceUID: "az-uid", Query: "T", Resources: []string{"/r"}, Limit: 2})
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.Equal(t, 2, result.RowCount)
	require.Len(t, result.Rows, 2)
}

func TestQueryAzureLogAnalytics_EmptyResultHasHints(t *testing.T) {
	var gotBody map[string]interface{}
	ctx := newAzureMonitorQueryMock(t, 0, &gotBody)

	result, err := queryAzureLogAnalytics(ctx, AzureLogAnalyticsQueryParams{
		DatasourceUID: "az-uid",
		Query:         "AppRequests | where TimeGenerated > ago(1d)",
		Resources:     []string{"/r"},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, result.RowCount)
	require.NotNil(t, result.Hints)
	assert.Contains(t, result.Hints.Summary, "Azure Log Analytics")
	assert.Contains(t, strings.Join(result.Hints.PossibleCauses, "\n"), "ago()")
}

func TestQueryAzureResourceGraph_EndToEnd(t *testing.T) {
	var gotBody map[string]interface{}
	ctx := newAzureMonitorQueryMock(t, 3, &gotBody)

	result, err := queryAzureResourceGraph(ctx, AzureResourceGraphQueryParams{
		DatasourceUID: "az-uid",
		Query:         "resources | summarize count_ = count() by name",
		Subscriptions: []string{"sub-1"},
		Limit:         2,
	})
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.Equal(t, 2, result.RowCount)
	assert.Nil(t, result.Hints)

	q := gotBody["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "Azure Resource Graph", q["queryType"])
	assert.Equal(t, []interface{}{"sub-1"}, q["subscriptions"])
	assert.Equal(t, "resources | summarize count_ = count() by name\n| take 3", q["azureResourceGraph"].(map[string]interface{})["query"])
}

func TestAzureMonitor_UpstreamErrorSurfaced(t *testing.T) {
	ctx := newAzureMonitorMockGrafana(t, AzureMonitorDatasourceType, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{"A":{"error":"SemanticError: 'NoSuchTable' could not be resolved","status":400}}}`))
	})

	_, err := queryAzureLogAnalytics(ctx, AzureLogAnalyticsQueryParams{DatasourceUID: "az-uid", Query: "NoSuchTable", Resources: []string{"/r"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SemanticError")
}
