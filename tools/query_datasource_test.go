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
		"blank title":     {`{"datasourceUid":"prom","title":" \t","queries":[{"refId":"A"}]}`, "white space"},
		"long title":      {`{"datasourceUid":"prom","title":"` + strings.Repeat("é", 101) + `","queries":[{"refId":"A"}]}`, "at most 100"},
		"bom title":       {`{"datasourceUid":"prom","title":"\ufeff\u3000","queries":[{"refId":"A"}]}`, "white space"},
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

// dsStringBody is a response whose size is n bytes: one frame with one string.
func dsStringBody(n int) string {
	head := `{"results":{"A":{"status":200,"frames":[{"schema":{"fields":[{"name":"v","type":"string"}]},"data":{"values":[["`
	tail := `"]]}}]}}}`
	return head + strings.Repeat("x", n-len(head)-len(tail)) + tail
}

func TestQueryDatasourceTooLarge(t *testing.T) {
	for name, size := range map[string]int{
		"body past the budget":            maxDSResultBytes + 1,
		"body in, result past the budget": maxDSResultBytes - 10,
	} {
		t.Run(name, func(t *testing.T) {
			result, _, err := runQueryDatasource(t, `{"datasourceUid":"prom","queries":[{"refId":"A"}]}`, dsStringBody(size))
			require.NoError(t, err)
			// tool.24, tool.25: the too-large tool error, one text item, and no structured content.
			require.True(t, result.IsError)
			assert.Nil(t, result.StructuredContent)
			require.Len(t, result.Content, 1)
			assert.Equal(t,
				"too-large: The result is larger than 1048576 bytes. Use a shorter range, a larger intervalMs or fewer series.",
				result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

func TestQueryDatasourceResultSizeIsTheMarshaledSize(t *testing.T) {
	result, _, err := runQueryDatasource(t, `{"datasourceUid":"prom","title":"t","unit":"s","queries":[{"refId":"A"}]}`, dsStringBody(4096))
	require.NoError(t, err)
	require.False(t, result.IsError)
	content := result.StructuredContent.(*QueryDatasourceResult)
	body, err := json.Marshal(content)
	require.NoError(t, err)
	size, err := dsResultSize(*content)
	require.NoError(t, err)
	assert.Equal(t, len(body), size)
	assert.NotNil(t, content.Data.Results, "the size does not change the result")
}

func TestQueryDatasourceSchemaOfTitle(t *testing.T) {
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(QueryDatasource.Tool.InputSchema.(json.RawMessage), &schema))
	title := schema.Properties["title"]
	assert.Equal(t, "string", title["type"])
	assert.Equal(t, `\S`, title["pattern"])
	assert.Equal(t, float64(maxDSTitleChars), title["maxLength"])
	assert.NotEmpty(t, title["description"])
}

func TestQueryDatasourceSchemaAcceptsIntegerTimes(t *testing.T) {
	body, err := json.Marshal(QueryDatasource.Tool.InputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"integer"`)
	assert.Contains(t, string(body), `"datasourceUid"`)
}

func TestQueryDatasourceFailedQueryNotes(t *testing.T) {
	// tool.29: one note for each entry with an error, also when every entry has one.
	for name, tc := range map[string]struct {
		response string
		want     []string
	}{
		"partial": {dsQueryResponse, []string{"Query B failed: bad_data: parse error"}},
		"all": {
			`{"results":{"B":{"status":400,"error":"two"},"A":{"status":500,"error":"one"}}}`,
			[]string{"Query A failed: one", "Query B failed: two"},
		},
		"none": {`{"results":{"A":{"status":200,"frames":[]}}}`, []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			result, _, err := runQueryDatasource(t,
				`{"datasourceUid":"prom","queries":[{"refId":"A"},{"refId":"B"}]}`, tc.response)
			require.NoError(t, err)
			content := result.StructuredContent.(*QueryDatasourceResult)
			assert.Equal(t, tc.want, content.AppNotes())
			// The notes are for render_grafana_app: they are not in the structured content.
			body, err := json.Marshal(content)
			require.NoError(t, err)
			assert.NotContains(t, string(body), "notes")
		})
	}
}

func TestQueryDatasourceTypeAndUnit(t *testing.T) {
	// tool.7, tool.21 to tool.23: the type of the model, the unit on the panel and on each alternative.
	result, _, err := runQueryDatasource(t,
		`{"datasourceUid":"prom","type":"streamgraph","unit":"reqps","queries":[{"refId":"A"}]}`,
		`{"results":{"A":{"status":200,"frames":[{"schema":{"fields":[{"name":"time","type":"time"},{"name":"v","type":"number"}]},"data":{"values":[[1700000000000,1700000060000],[1,2]]}}]}}}`)
	require.NoError(t, err)
	require.False(t, result.IsError)
	content := result.StructuredContent.(*QueryDatasourceResult)
	assert.Equal(t, "streamgraph", content.Chart)
	assert.Equal(t, "grafana-echartsstream-panel", content.Panel.Type)
	assert.Equal(t, map[string]any{"streamChartType": "river"}, content.Panel.Options)
	require.NotNil(t, content.Panel.FieldConfig)
	assert.Equal(t, "reqps", content.Panel.FieldConfig.Defaults.Unit)
	require.NotEmpty(t, content.Alternatives)
	for _, alternative := range content.Alternatives {
		assert.NotEqual(t, "streamgraph", alternative.Chart)
		require.NotNil(t, alternative.Panel.FieldConfig)
		assert.Equal(t, "reqps", alternative.Panel.FieldConfig.Defaults.Unit)
	}
	body, err := json.Marshal(content)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"fieldConfig":{"defaults":{"unit":"reqps"}}`)
}

func TestQueryDatasourceNoUnitHasNoFieldConfig(t *testing.T) {
	result, _, err := runQueryDatasource(t, `{"datasourceUid":"prom","queries":[{"refId":"A"}]}`, dsQueryResponse)
	require.NoError(t, err)
	body, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "fieldConfig")
}

func TestQueryDatasourceNoRowsDoesNotCheckType(t *testing.T) {
	// tool.19: with no rows the type is not checked, and the chart is the chart of the type.
	result, _, err := runQueryDatasource(t,
		`{"datasourceUid":"prom","type":"sankey","queries":[{"refId":"A"}]}`,
		`{"results":{"A":{"status":200,"frames":[]}}}`)
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "sankey", result.StructuredContent.(*QueryDatasourceResult).Chart)
}

func TestQueryDatasourceRejectsUnknownType(t *testing.T) {
	result, posted, err := runQueryDatasource(t,
		`{"datasourceUid":"prom","type":"gauge","queries":[{"refId":"A"}]}`, dsQueryResponse)
	if err == nil {
		require.True(t, result.IsError)
	}
	assert.Nil(t, posted, "tool.4: no query")
	body, err := json.Marshal(QueryDatasource.Tool.InputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"sankey"`)
	assert.Contains(t, string(body), `Each chart needs: timeseries (a time field and number fields)`)
}

func TestQueryDatasourceTypeMismatchWhenNoChartFits(t *testing.T) {
	// tool.20: if no chart fits, <charts> is the chart of the pick.
	result, _, err := runQueryDatasource(t,
		`{"datasourceUid":"prom","type":"sankey","queries":[{"refId":"A"}]}`,
		`{"results":{"A":{"status":200,"frames":[{"schema":{"fields":[{"name":"ok","type":"boolean"}]},"data":{"values":[[true,false]]}}]}}}`)
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Equal(t,
		"type-mismatch: sankey needs source, target and value fields. Charts that fit: barchart.",
		result.Content[0].(*mcp.TextContent).Text)
}

func runQueryDatasourceStatus(t *testing.T, status int, response string) (*mcp.CallToolResult, error) {
	t.Helper()
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(grafana.Close)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: grafana.URL})
	return QueryDatasource.Handler(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: "query_datasource", Arguments: json.RawMessage(`{"datasourceUid":"prom","queries":[{"refId":"A"},{"refId":"B"}]}`),
	}})
}

// Grafana answers a failed query with 400 and the results of each query: a partial result (tool.16, tool.29).
func TestQueryDatasourcePartialFailure(t *testing.T) {
	result, err := runQueryDatasourceStatus(t, http.StatusBadRequest, `{"results":{`+
		`"A":{"status":200,"frames":[{"schema":{"fields":[{"name":"Time","type":"time"},{"name":"Value","type":"number"}]},"data":{"values":[[1,2],[3,4]]}}]},`+
		`"B":{"status":400,"error":"parse error","frames":[{"schema":{},"data":{"values":[]}}]}}}`)
	require.NoError(t, err)
	require.False(t, result.IsError)
	content := result.StructuredContent.(*QueryDatasourceResult)
	assert.Equal(t, "timeseries", content.Chart)
	assert.Equal(t, []string{"Query B failed: parse error"}, content.AppNotes())
}

func TestQueryDatasourceStatusWithoutResults(t *testing.T) {
	for name, tc := range map[string]struct {
		status   int
		response string
	}{
		"not found":     {http.StatusNotFound, `{"message":"Data source not found"}`},
		"no entries":    {http.StatusBadRequest, `{"results":{}}`},
		"not JSON":      {http.StatusInternalServerError, `<html>bad gateway</html>`},
		"empty body":    {http.StatusBadGateway, ``},
		"no results ok": {http.StatusOK, `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := runQueryDatasourceStatus(t, tc.status, tc.response)
			require.NoError(t, err)
			require.True(t, result.IsError)
			assert.Nil(t, result.StructuredContent)
			message := result.Content[0].(*mcp.TextContent).Text
			if tc.status != http.StatusOK {
				assert.Contains(t, message, fmt.Sprintf("query returned status %d: %s", tc.status, tc.response))
			} else {
				assert.Contains(t, message, "returned no results")
			}
		})
	}
}
