//go:build unit

package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLokiClient_FetchData_PassesMatcherAsQueryParam(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"status":"success","data":["service"]}`))
	}))
	defer server.Close()

	c := &Client{httpClient: server.Client(), baseURL: server.URL}

	result, err := c.fetchData(context.Background(), "/loki/api/v1/label/service/values", `{namespace="prod"}`, "", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"service"}, result)
	assert.Equal(t, `{namespace="prod"}`, gotQuery)
}

func TestLokiClient_FetchData_OmitsQueryParamWhenMatcherEmpty(t *testing.T) {
	var sawQuery bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawQuery = r.URL.Query()["query"]
		_, _ = w.Write([]byte(`{"status":"success","data":["app","pod"]}`))
	}))
	defer server.Close()

	c := &Client{httpClient: server.Client(), baseURL: server.URL}

	result, err := c.fetchData(context.Background(), "/loki/api/v1/labels", "", "", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"app", "pod"}, result)
	assert.False(t, sawQuery, "query param should be omitted when matcher is empty")
}

// lokiQueryStub serves body as the Loki query response through the Grafana
// datasource proxy, and records the query parameters it received.
func lokiQueryStub(body string) (*httptest.Server, *url.Values) {
	query := &url.Values{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/loki":
			_, _ = io.WriteString(w, `{"uid":"loki","type":"loki"}`)
		case "/api/datasources/proxy/uid/loki/loki/api/v1/query_range",
			"/api/datasources/proxy/uid/loki/loki/api/v1/query":
			*query = r.URL.Query()
			_, _ = io.WriteString(w, body)
		default:
			http.NotFound(w, r)
		}
	})), query
}

func TestQueryLokiLogsSortsBeforeTruncating(t *testing.T) {
	server, _ := lokiQueryStub(`{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"app":"a"},"values":[["1000000002","a2"],["1000000001","a1"]]},
		{"stream":{"app":"b"},"values":[["1000000003","b3"]]}
	]}}`)
	defer server.Close()
	ctx := enforceTestCtx(server, false)

	for _, tc := range []struct {
		direction string
		lines     []string
	}{
		{direction: "backward", lines: []string{"b3", "a2"}},
		{direction: "forward", lines: []string{"a1", "a2"}},
	} {
		t.Run(tc.direction, func(t *testing.T) {
			result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
				DatasourceUID: "loki", LogQL: `{app=~".+"}`, Limit: 2, Direction: tc.direction,
			})
			require.NoError(t, err)
			require.Len(t, result.Data, 2)
			assert.Equal(t, tc.lines, []string{result.Data[0].Line, result.Data[1].Line})
			require.NotNil(t, result.Metadata)
			assert.True(t, result.Metadata.ResultsTruncated)
		})
	}
}

func TestQueryLokiLogsRejectsUnparseableStreamTimestamp(t *testing.T) {
	server, _ := lokiQueryStub(`{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"app":"a"},"values":[["not-a-timestamp","a1"]]}
	]}}`)
	defer server.Close()

	_, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-timestamp")
}

// Loki timestamps are nanosecond-resolution, so a bound one nanosecond off a
// second boundary must survive both parsing and formatting unchanged.
func TestQueryLokiLogsPreservesNanosecondBounds(t *testing.T) {
	server, query := lokiQueryStub(`{"status":"success","data":{"resultType":"streams","result":[]}}`)
	defer server.Close()

	_, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`,
		StartRFC3339: "2024-01-15T10:00:00.000000001Z", EndRFC3339: "2024-01-15T10:00:00.000000004Z",
	})
	require.NoError(t, err)
	assert.Equal(t, "1705312800000000001", query.Get("start"))
	assert.Equal(t, "1705312800000000004", query.Get("end"))

	_, err = queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `count_over_time({app="a"}[5m])`, QueryType: "instant",
		EndRFC3339: "2024-01-15T10:00:00.000000004Z",
	})
	require.NoError(t, err)
	assert.Equal(t, "1705312800000000004", query.Get("time"))
}

func TestQueryLokiLogsCapsLineBytes(t *testing.T) {
	server, _ := lokiQueryStub(`{"status":"success","data":{"resultType":"streams","encodingFlags":["categorize-labels"],"result":[
		{"stream":{"app":"a"},"values":[
			["1000000002","abcdéfgh",{"parsed":{"message":"0123456789"},"structuredMetadata":{"traceID":"t1"}}],
			["1000000001","ok",{}]
		]}
	]}}`)
	defer server.Close()

	for _, tc := range []struct {
		name          string
		serverDefault int
		requested     int
		format        string
		line          string
		message       string
		cut           int
	}{
		{name: "no cap", line: "abcdéfgh", message: "0123456789"},
		{name: "cut lands inside a rune", requested: 5, line: "abcd", message: "01234", cut: 5 + 5},
		{name: "server default", serverDefault: 6, line: "abcdé", message: "012345", cut: 3 + 4},
		{name: "call overrides server default", serverDefault: 2, requested: 8, line: "abcdéfg", message: "01234567", cut: 1 + 2},
		{name: "compact format", requested: 5, format: "compact", line: "abcd", message: "01234", cut: 5 + 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := mcpgrafana.WithGrafanaConfig(enforceTestCtx(server, false), mcpgrafana.GrafanaConfig{
				URL: server.URL, MaxLokiLineBytes: tc.serverDefault,
			})

			result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
				DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 10, MaxLineBytes: tc.requested, Format: tc.format,
			})
			require.NoError(t, err)

			if tc.format == "compact" {
				require.Len(t, result.Streams, 1)
				lines := result.Streams[0].Lines
				require.Len(t, lines, 2)
				assert.Equal(t, tc.line, lines[0].Line)
				assert.Equal(t, tc.message, lines[0].Parsed["message"])
				assert.Equal(t, tc.cut, lines[0].TruncatedBytes)
				assert.Zero(t, lines[1].TruncatedBytes)
				return
			}
			require.Len(t, result.Data, 2)
			assert.Equal(t, tc.line, result.Data[0].Line)
			assert.Equal(t, tc.message, result.Data[0].Parsed["message"])
			assert.Equal(t, "t1", result.Data[0].StructuredMetadata["traceID"])
			assert.Equal(t, tc.cut, result.Data[0].TruncatedBytes)
			assert.Equal(t, "ok", result.Data[1].Line)
			assert.Zero(t, result.Data[1].TruncatedBytes)
		})
	}
}
