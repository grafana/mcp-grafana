//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dsQueryResponse = `{"results":{
	"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[{"name":"time","type":"time","typeInfo":{"frame":"time.Time"}},{"name":"value","type":"number","typeInfo":{"frame":"float64"}}]},"data":{"values":[[1700000000000],[1.5]]}}]},
	"B":{"status":400,"error":"bad_data: parse error"}}}`

// runQueryDatasource calls the source as render_grafana_app does, against a fake /api/ds/query.
func runQueryDatasource(t *testing.T, arguments, response string) (*mcp.CallToolResult, map[string]any, error) {
	t.Helper()
	var posted map[string]any
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler runs outside the test goroutine, so it must not use require.
		assert.Equal(t, "/api/ds/query", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		assert.NoError(t, json.Unmarshal(body, &posted))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(grafana.Close)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: grafana.URL})
	result, err := QueryDatasource.Handler(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: "query_datasource", Arguments: json.RawMessage(arguments),
	}})
	return result, posted, err
}

func TestQueryDatasourceResult(t *testing.T) {
	before := time.Now().UnixMilli()
	result, posted, err := runQueryDatasource(t,
		`{"datasourceUid":"prom","from":"now-6h","queries":[{"refId":"A","expr":"up","legendFormat":"{{job}}"},{"refId":"B","expr":"x("}]}`,
		dsQueryResponse)
	require.NoError(t, err)
	require.False(t, result.IsError, fmt.Sprint(result.Content[0].(*mcp.TextContent).Text))

	content, ok := result.StructuredContent.(*QueryDatasourceResult)
	require.True(t, ok)
	// tool.14: an absolute range in epoch milliseconds, and the time of the response.
	assert.Equal(t, int64(6*time.Hour/time.Millisecond), content.Data.Range.To-content.Data.Range.From)
	assert.GreaterOrEqual(t, content.Data.CapturedAt, before)
	// tool.15, tool.16: the results as they are, a partial result too.
	var results map[string]struct {
		Status int              `json:"status"`
		Frames []map[string]any `json:"frames"`
		Error  string           `json:"error"`
	}
	require.NoError(t, json.Unmarshal(content.Data.Results, &results))
	require.Len(t, results["A"].Frames, 1)
	assert.Equal(t, "bad_data: parse error", results["B"].Error)

	// tool.6, tool.12: the request has epoch milliseconds, and the defaults of each query.
	queries := posted["queries"].([]any)
	require.Len(t, queries, 2)
	first := queries[0].(map[string]any)
	assert.Equal(t, map[string]any{"uid": "prom"}, first["datasource"])
	assert.Equal(t, float64(defaultDSMaxDataPoints), first["maxDataPoints"])
	assert.Equal(t, "{{job}}", first["legendFormat"])
	rangeMs := content.Data.Range.To - content.Data.Range.From
	assert.Equal(t, float64(rangeMs/defaultDSMaxDataPoints), first["intervalMs"])
}

func TestQueryDatasourceKeepsQueryValues(t *testing.T) {
	_, posted, err := runQueryDatasource(t,
		`{"datasourceUid":"prom","queries":[{"refId":"A","datasource":{"uid":"prom","type":"prometheus"},"maxDataPoints":50,"intervalMs":7000}]}`,
		dsQueryResponse)
	require.NoError(t, err)
	query := posted["queries"].([]any)[0].(map[string]any)
	assert.Equal(t, float64(50), query["maxDataPoints"])
	assert.Equal(t, float64(7000), query["intervalMs"])
	assert.Equal(t, map[string]any{"uid": "prom", "type": "prometheus"}, query["datasource"])
}

func TestQueryDatasourceTimeFormats(t *testing.T) {
	for name, arguments := range map[string]string{
		"integer":  `{"datasourceUid":"prom","from":1700000000000,"to":1700003600000,"queries":[{"refId":"A"}]}`,
		"string":   `{"datasourceUid":"prom","from":"1700000000000","to":"1700003600000","queries":[{"refId":"A"}]}`,
		"relative": `{"datasourceUid":"prom","from":"now-1h","to":"now","queries":[{"refId":"A"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			result, _, err := runQueryDatasource(t, arguments, dsQueryResponse)
			require.NoError(t, err)
			content := result.StructuredContent.(*QueryDatasourceResult)
			assert.Equal(t, int64(time.Hour/time.Millisecond), content.Data.Range.To-content.Data.Range.From)
		})
	}
}

func TestQueryDatasourceRejectsBadArguments(t *testing.T) {
	for name, tc := range map[string]struct{ arguments, message string }{
		"no queries":      {`{"datasourceUid":"prom","queries":[]}`, "at least one query"},
		"no refId":        {`{"datasourceUid":"prom","queries":[{"expr":"up"}]}`, "no refId"},
		"duplicate refId": {`{"datasourceUid":"prom","queries":[{"refId":"A"},{"refId":"A"}]}`, "more than once"},
		"other source":    {`{"datasourceUid":"prom","queries":[{"refId":"A","datasource":{"uid":"loki"}}]}`, "another data source"},
		"name as source":  {`{"datasourceUid":"prom","queries":[{"refId":"A","datasource":"Prometheus"}]}`, "another data source"},
		"bad maxPoints":   {`{"datasourceUid":"prom","queries":[{"refId":"A","maxDataPoints":0}]}`, "maxDataPoints"},
		"reversed range":  {`{"datasourceUid":"prom","from":"now","to":"now-1h","queries":[{"refId":"A"}]}`, "before"},
		"bad time":        {`{"datasourceUid":"prom","from":"yesterday-ish","queries":[{"refId":"A"}]}`, "from"},
	} {
		t.Run(name, func(t *testing.T) {
			queried := false
			grafana := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { queried = true }))
			t.Cleanup(grafana.Close)
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: grafana.URL})
			result, err := QueryDatasource.Handler(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
				Name: "query_datasource", Arguments: json.RawMessage(tc.arguments),
			}})
			message := ""
			if err != nil {
				message = err.Error()
			} else {
				require.True(t, result.IsError)
				message = result.Content[0].(*mcp.TextContent).Text
			}
			assert.Contains(t, message, tc.message)
			assert.False(t, queried, "tool.4, tool.10: no query")
		})
	}
}

func TestQueryDatasourceNullDatasourceGetsTheDefault(t *testing.T) {
	_, posted, err := runQueryDatasource(t, `{"datasourceUid":"prom","queries":[{"refId":"A","datasource":null}]}`, `{"results":{"A":{"status":200}}}`)
	require.NoError(t, err)
	query := posted["queries"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"uid": "prom"}, query["datasource"])
}

func TestQueryDatasourceOmitsOversizedContent(t *testing.T) {
	big := `{"results":{"A":{"status":200,"frames":[{"schema":{"fields":[{"name":"` + strings.Repeat("x", maxDSResultBytes) + `"}]}}]}}}`
	result, _, err := runQueryDatasource(t, `{"datasourceUid":"prom","queries":[{"refId":"A"}]}`, big)
	require.NoError(t, err)
	assert.Nil(t, result.StructuredContent)
}

func TestQueryDatasourceSchemaAcceptsIntegerTimes(t *testing.T) {
	body, err := json.Marshal(QueryDatasource.Tool.InputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"integer"`)
	assert.Contains(t, string(body), `"datasourceUid"`)
}
