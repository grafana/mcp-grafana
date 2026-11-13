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

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// lokiQueryStub answers /api/ds/query with a Loki log frame holding the
// first maxLines of the given (tsNs, line) rows, and records the last payload
// it received.
func lokiQueryStub(rows ...[2]string) (*httptest.Server, *map[string]interface{}) {
	payload := &map[string]interface{}{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/loki":
			_, _ = io.WriteString(w, `{"uid":"loki","type":"loki"}`)
		case "/api/ds/query":
			*payload = map[string]interface{}{}
			_ = json.NewDecoder(r.Body).Decode(payload)
			page := rows
			if q, ok := (*payload)["queries"].([]interface{}); ok && len(q) > 0 {
				if n, ok := q[0].(map[string]interface{})["maxLines"].(float64); ok && int(n) < len(page) {
					page = page[:int(n)]
				}
			}
			_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{
				"A": backend.DataResponse{Frames: data.Frames{lokiLogFrame(page...)}},
			}})
		default:
			http.NotFound(w, r)
		}
	})), payload
}

// lokiLogFrame builds a frame shaped like the one Grafana's Loki datasource
// returns for log queries, with an "app" stream label per row.
func lokiLogFrame(rows ...[2]string) *data.Frame {
	var labels, types []json.RawMessage
	var times []time.Time
	var lines, tsNs []string
	for _, r := range rows {
		labels = append(labels, json.RawMessage(`{"app":"`+r[1][:1]+`"}`))
		types = append(types, json.RawMessage(`{"app":"I"}`))
		times = append(times, time.Unix(0, 0))
		tsNs = append(tsNs, r[0])
		lines = append(lines, r[1])
	}
	return data.NewFrame("",
		data.NewField("labels", nil, labels),
		data.NewField("Time", nil, times),
		data.NewField("Line", nil, lines),
		data.NewField("tsNs", nil, tsNs),
		data.NewField("labelTypes", nil, types),
	)
}

func TestQueryLokiLogsSortsBeforeTruncating(t *testing.T) {
	server, _ := lokiQueryStub(
		[2]string{"1000000002", "a2"},
		[2]string{"1000000001", "a1"},
		[2]string{"1000000003", "b3"},
	)
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
				StartRFC3339: "1970-01-01T00:00:00Z", EndRFC3339: "1970-01-01T00:00:02Z",
			})
			require.NoError(t, err)
			require.Len(t, result.Data, 2)
			assert.Equal(t, tc.lines, []string{result.Data[0].Line, result.Data[1].Line})
			assert.Equal(t, map[string]string{"app": result.Data[0].Line[:1]}, result.Data[0].Labels)
			require.NotNil(t, result.Metadata)
			assert.True(t, result.Metadata.ResultsTruncated)
		})
	}
}

func TestQueryLokiLogsRejectsUnparseableStreamTimestamp(t *testing.T) {
	server, _ := lokiQueryStub([2]string{"not-a-timestamp", "a1"})
	defer server.Close()

	_, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-timestamp")
}

// Loki timestamps are nanosecond-resolution but /api/ds/query takes
// milliseconds: the range is widened to whole milliseconds and lines outside
// the exact [start, end) bounds are trimmed, so paging by timestamp stays exact.
func TestQueryLokiLogsPreservesNanosecondBounds(t *testing.T) {
	server, payload := lokiQueryStub(
		[2]string{"1705312800000000000", "a-before-start"},
		[2]string{"1705312800000000001", "a-at-start"},
		[2]string{"1705312800000000003", "a-inside"},
		[2]string{"1705312800000000004", "a-at-end"},
	)
	defer server.Close()

	result, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Direction: "forward",
		StartRFC3339: "2024-01-15T10:00:00.000000001Z", EndRFC3339: "2024-01-15T10:00:00.000000004Z",
	})
	require.NoError(t, err)
	assert.Equal(t, "1705312800000", (*payload)["from"])
	assert.Equal(t, "1705312800001", (*payload)["to"])
	var lines []string
	for _, e := range result.Data {
		lines = append(lines, e.Line)
	}
	assert.Equal(t, []string{"a-at-start", "a-inside"}, lines)
}

// Loki applies maxLines to the widened range, so lines in the extra
// millisecond past end can fill the page. Paging backward from a boundary
// timestamp must still return a full page and flag truncation.
func TestQueryLokiLogsRefillsPageTrimmedAtBoundary(t *testing.T) {
	server, payload := lokiQueryStub(
		[2]string{"1705312800000000009", "a-past-end-2"},
		[2]string{"1705312800000000005", "a-past-end-1"},
		[2]string{"1705312800000000004", "a-at-end"},
		[2]string{"1705312800000000003", "a-3"},
		[2]string{"1705312800000000002", "a-2"},
		[2]string{"1705312800000000001", "a-1"},
	)
	defer server.Close()

	result, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 2, Direction: "backward",
		StartRFC3339: "2024-01-15T10:00:00Z", EndRFC3339: "2024-01-15T10:00:00.000000004Z",
	})
	require.NoError(t, err)
	var lines []string
	for _, e := range result.Data {
		lines = append(lines, e.Line)
	}
	assert.Equal(t, []string{"a-3", "a-2"}, lines)
	assert.True(t, result.Metadata.ResultsTruncated)
	assert.EqualValues(t, 6, (*payload)["queries"].([]interface{})[0].(map[string]interface{})["maxLines"])
}

// Each refill asks for the page size plus the lines trimmed at the leading
// edge, rather than adding to the previous request, so repeated refills over a
// dense boundary don't overshoot.
func TestQueryLokiLogsRefillDoesNotCompound(t *testing.T) {
	server, payload := lokiQueryStub(
		[2]string{"1705312800000000009", "s5"},
		[2]string{"1705312800000000008", "s4"},
		[2]string{"1705312800000000007", "s3"},
		[2]string{"1705312800000000006", "s2"},
		[2]string{"1705312800000000005", "s1"},
		[2]string{"1705312800000000003", "a-3"},
		[2]string{"1705312800000000002", "a-2"},
		[2]string{"1705312800000000001", "a-1"},
	)
	defer server.Close()

	result, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 2, Direction: "backward",
		StartRFC3339: "2024-01-15T10:00:00Z", EndRFC3339: "2024-01-15T10:00:00.000000004Z",
	})
	require.NoError(t, err)
	require.Len(t, result.Data, 2)
	// Requests: 3, then 3+3, then 3+5 (not 6+5).
	assert.EqualValues(t, 8, (*payload)["queries"].([]interface{})[0].(map[string]interface{})["maxLines"])
}

// Lines trimmed at the trailing edge mean Loki ran out of in-range lines, so
// the query is not repeated.
func TestQueryLokiLogsSkipsRefillForTrailingTrim(t *testing.T) {
	server, payload := lokiQueryStub(
		[2]string{"1705312800000000003", "a-3"},
		[2]string{"1705312800000000002", "a-2"},
		[2]string{"1705312800000000000", "a-before-start"},
	)
	defer server.Close()

	result, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 2, Direction: "backward",
		StartRFC3339: "2024-01-15T10:00:00.000000001Z", EndRFC3339: "2024-01-15T10:00:01Z",
	})
	require.NoError(t, err)
	assert.Len(t, result.Data, 2)
	assert.False(t, result.Metadata.ResultsTruncated)
	assert.EqualValues(t, 3, (*payload)["queries"].([]interface{})[0].(map[string]interface{})["maxLines"])
}

// Instant queries evaluate at end, falling back to start, like the proxy path.
func TestQueryLokiLogsInstantUsesStartWithoutEnd(t *testing.T) {
	server, payload := lokiQueryStub()
	defer server.Close()

	_, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `count_over_time({app="a"}[5m])`, QueryType: "instant",
		StartRFC3339: "2024-01-15T10:00:00Z",
	})
	require.NoError(t, err)
	assert.Equal(t, "1705312800000", (*payload)["from"])
	assert.Equal(t, "1705312800000", (*payload)["to"])
}

// Without stepSeconds, range queries send Loki's default step of range/250
// rather than letting Grafana derive one from its 1s default interval.
func TestQueryLokiLogsSendsLokiDefaultStep(t *testing.T) {
	server, payload := lokiQueryStub()
	defer server.Close()

	_, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `rate({app="a"}[5m])`,
		StartRFC3339: "2024-01-15T10:00:00Z", EndRFC3339: "2024-01-15T11:00:00Z",
	})
	require.NoError(t, err)
	assert.Equal(t, "14s", (*payload)["queries"].([]interface{})[0].(map[string]interface{})["step"])
}

// Grafana's Loki datasource sends supportingQueryType to Loki as
// X-Query-Tags: Source=<value>, keeping the attribution the proxy path had.
func TestQueryLokiLogsTagsQuerySource(t *testing.T) {
	server, payload := lokiQueryStub()
	defer server.Close()

	_, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`,
	})
	require.NoError(t, err)
	assert.Equal(t, "mcp-grafana", (*payload)["queries"].([]interface{})[0].(map[string]interface{})["supportingQueryType"])
}

// Grafana before 11.0 rejects unknown supportingQueryType values with a 500.
func TestQueryLokiLogsOmitsQuerySourceBeforeGrafana11(t *testing.T) {
	server, payload := lokiQueryStub()
	defer server.Close()

	ctx := enforceTestCtx(server, false)
	mcpgrafana.GrafanaClientFromContext(ctx).Version = "10.4.7"
	_, err := queryLokiLogs(ctx, QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`,
	})
	require.NoError(t, err)
	assert.NotContains(t, (*payload)["queries"].([]interface{})[0], "supportingQueryType")
}

func TestLokiFramesToResult_StatusError(t *testing.T) {
	_, err := lokiFramesToResult(&backend.QueryDataResponse{Responses: backend.Responses{
		"A": backend.DataResponse{Status: backend.StatusBadGateway},
	}}, "range")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

func TestQueryLokiLogsCategorizesLabels(t *testing.T) {
	frame := data.NewFrame("",
		data.NewField("labels", nil, []json.RawMessage{json.RawMessage(`{"app":"a","trace_id":"t1","level":"error"}`)}),
		data.NewField("Time", nil, []time.Time{time.Unix(1, 0)}),
		data.NewField("Line", nil, []string{"boom"}),
		data.NewField("tsNs", nil, []string{"1000000000"}),
		data.NewField("labelTypes", nil, []json.RawMessage{json.RawMessage(`{"app":"I","trace_id":"S","level":"P"}`)}),
	)
	frame.SetMeta(&data.FrameMeta{Stats: []data.QueryStat{{FieldConfig: data.FieldConfig{DisplayName: "Summary: total lines processed"}, Value: 42}}})

	result, err := lokiFramesToResult(&backend.QueryDataResponse{Responses: backend.Responses{
		"A": backend.DataResponse{Frames: data.Frames{frame}},
	}}, "range")
	require.NoError(t, err)
	assert.Equal(t, "streams", result.ResultType)
	require.NotNil(t, result.TotalLinesScanned)
	assert.Equal(t, 42, *result.TotalLinesScanned)
	assert.Equal(t, []LogEntry{{
		Timestamp:          "1000000000",
		Line:               "boom",
		Labels:             map[string]string{"app": "a"},
		StructuredMetadata: map[string]string{"trace_id": "t1"},
		Parsed:             map[string]string{"level": "error"},
	}}, result.Entries)
}

// With Grafana's lokiLogsDataplane flag, log frames use body and timestamp
// and have no tsNs. The nanosecond timestamp must survive the /api/ds/query
// JSON round trip.
func TestQueryLokiLogsReadsDataplaneFrames(t *testing.T) {
	frame := data.NewFrame("",
		data.NewField("labels", nil, []json.RawMessage{json.RawMessage(`{"app":"a","level":"error"}`)}),
		data.NewField("timestamp", nil, []time.Time{time.Unix(0, 1705312800000000123)}),
		data.NewField("body", nil, []string{"boom"}),
		data.NewField("id", nil, []string{"1705312800000000123_abcd"}),
		data.NewField("labelTypes", nil, []json.RawMessage{json.RawMessage(`{"app":"I","level":"P"}`)}),
	)
	frame.SetMeta(&data.FrameMeta{Type: data.FrameTypeLogLines})
	raw, err := json.Marshal(backend.QueryDataResponse{Responses: backend.Responses{
		"A": backend.DataResponse{Frames: data.Frames{frame}},
	}})
	require.NoError(t, err)
	var resp backend.QueryDataResponse
	require.NoError(t, json.Unmarshal(raw, &resp))

	result, err := lokiFramesToResult(&resp, "range")
	require.NoError(t, err)
	assert.Equal(t, "streams", result.ResultType)
	assert.Equal(t, []LogEntry{{
		Timestamp: "1705312800000000123",
		Line:      "boom",
		Labels:    map[string]string{"app": "a"},
		Parsed:    map[string]string{"level": "error"},
	}}, result.Entries)
}

func TestLokiFramesToResult_Metrics(t *testing.T) {
	frame := data.NewFrame("",
		data.NewField("Time", nil, []time.Time{time.UnixMilli(1000), time.UnixMilli(2500)}),
		data.NewField("Value", data.Labels{"app": "a"}, []float64{3, 4}),
	)
	resp := &backend.QueryDataResponse{Responses: backend.Responses{"A": backend.DataResponse{Frames: data.Frames{frame}}}}

	matrix, err := lokiFramesToResult(resp, "range")
	require.NoError(t, err)
	assert.Equal(t, "matrix", matrix.ResultType)
	assert.Equal(t, []LogEntry{{
		Labels: map[string]string{"app": "a"},
		Values: []MetricValue{{Timestamp: "1.000", Value: 3}, {Timestamp: "2.500", Value: 4}},
	}}, matrix.Entries)

	vector, err := lokiFramesToResult(resp, "instant")
	require.NoError(t, err)
	assert.Equal(t, "vector", vector.ResultType)
	four := 4.0
	assert.Equal(t, []LogEntry{{Timestamp: "2.500", Value: &four, Labels: map[string]string{"app": "a"}}}, vector.Entries)

	empty, err := lokiFramesToResult(&backend.QueryDataResponse{Responses: backend.Responses{}}, "range")
	require.NoError(t, err)
	assert.Equal(t, "matrix", empty.ResultType)
	assert.Empty(t, empty.Entries)
}

// lokiToolResult runs the query_loki_logs tool against a Grafana stub
// that answers /api/ds/query with frames.
func lokiToolResult(t *testing.T, args string, frames ...*data.Frame) *mcp.CallToolResult {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/loki":
			_, _ = io.WriteString(w, `{"uid":"loki","type":"loki"}`)
		case "/api/ds/query":
			_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{
				"A": backend.DataResponse{Frames: frames},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	result, err := QueryLokiLogs.Handler(enforceTestCtx(server, false), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "query_loki_logs", Arguments: json.RawMessage(args)},
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	return result
}

// callQueryLokiLogs returns the text of a successful query_loki_logs call.
func callQueryLokiLogs(t *testing.T, args string, frames ...*data.Frame) string {
	t.Helper()
	result := lokiToolResult(t, args, frames...)
	text := result.Content[0].(*mcp.TextContent).Text
	require.False(t, result.IsError, text)
	assert.Nil(t, result.StructuredContent)
	return text
}

// testLokiLogFrame has two lines of stream app=a and one of app=b, newest first.
func testLokiLogFrame() *data.Frame {
	return data.NewFrame("",
		data.NewField("labels", nil, []json.RawMessage{
			json.RawMessage(`{"app":"a","trace_id":"t1","level":"error"}`),
			json.RawMessage(`{"app":"b"}`),
			json.RawMessage(`{"app":"a","trace_id":"t1","level":"error"}`),
		}),
		data.NewField("Time", nil, []time.Time{time.Unix(0, 0), time.Unix(0, 0), time.Unix(0, 0)}),
		data.NewField("Line", nil, []string{"boom", "ok", "bang"}),
		data.NewField("tsNs", nil, []string{"3000000000", "2000000000", "1000000000"}),
		data.NewField("labelTypes", nil, []json.RawMessage{
			json.RawMessage(`{"app":"I","trace_id":"S","level":"P"}`),
			json.RawMessage(`{"app":"I"}`),
			json.RawMessage(`{"app":"I","trace_id":"S","level":"P"}`),
		}),
	)
}

func testLokiMetricFrame() *data.Frame {
	return data.NewFrame("",
		data.NewField("Time", nil, []time.Time{time.UnixMilli(1000), time.UnixMilli(2500)}),
		data.NewField("Value", data.Labels{"app": "a"}, []float64{3, 4.5}),
	)
}

const (
	testLokiTimeArgs = `"startRfc3339":"1970-01-01T00:00:00Z","endRfc3339":"1970-01-01T00:00:05Z"`
	testLokiLogArgs  = `{"datasourceUid":"loki","logql":"{app=~\".+\"}",` + testLokiTimeArgs
	testLokiRateArgs = `{"datasourceUid":"loki","logql":"sum by (app) (count_over_time({app=\"a\"}[1m]))",` + testLokiTimeArgs
)

// TestQueryLokiLogsDefaultFormatUnchanged pins the exact text of the default output.
func TestQueryLokiLogsDefaultFormatUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, args, text string
		frame            *data.Frame
	}{
		{
			name:  "log",
			args:  testLokiLogArgs + `}`,
			frame: testLokiLogFrame(),
			text: `{"data":[` +
				`{"timestamp":"3000000000","line":"boom","labels":{"app":"a"},"structuredMetadata":{"trace_id":"t1"},"parsed":{"level":"error"}},` +
				`{"timestamp":"2000000000","line":"ok","labels":{"app":"b"}},` +
				`{"timestamp":"1000000000","line":"bang","labels":{"app":"a"},"structuredMetadata":{"trace_id":"t1"},"parsed":{"level":"error"}}],` +
				`"metadata":{"linesReturned":3,"maxLinesAllowed":10,"resultsTruncated":false,"totalLinesScanned":null,` +
				`"startTime":"1970-01-01T00:00:00Z","endTime":"1970-01-01T00:00:05Z"}}`,
		},
		{
			name:  "metric",
			args:  testLokiRateArgs + `}`,
			frame: testLokiMetricFrame(),
			text: `{"data":[{"values":[{"timestamp":"1.000","value":3},{"timestamp":"2.500","value":4.5}],"labels":{"app":"a"}}],` +
				`"metadata":{"linesReturned":1,"maxLinesAllowed":10,"resultsTruncated":false,"totalLinesScanned":null,` +
				`"startTime":"1970-01-01T00:00:00Z","endTime":"1970-01-01T00:00:05Z"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.text, callQueryLokiLogs(t, tc.args, tc.frame))
		})
	}
}

func TestQueryLokiLogsFormatLokiStreams(t *testing.T) {
	text := callQueryLokiLogs(t, testLokiLogArgs+`,"format":"loki"}`, testLokiLogFrame())
	assert.JSONEq(t, `{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"app":"a","trace_id":"t1","level":"error"},"values":[["3000000000","boom"],["1000000000","bang"]]},
		{"stream":{"app":"b"},"values":[["2000000000","ok"]]}
	]},"metadata":{"linesReturned":3,"maxLinesAllowed":10,"resultsTruncated":false,"totalLinesScanned":null,
		"startTime":"1970-01-01T00:00:00Z","endTime":"1970-01-01T00:00:05Z"}}`, text)
}

func TestQueryLokiLogsFormatLokiTruncated(t *testing.T) {
	text := callQueryLokiLogs(t, testLokiLogArgs+`,"format":"loki","limit":1}`, testLokiLogFrame())
	assert.JSONEq(t, `{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"app":"a","trace_id":"t1","level":"error"},"values":[["3000000000","boom"]]}
	]},"metadata":{"linesReturned":1,"maxLinesAllowed":1,"resultsTruncated":true,"totalLinesScanned":null,
		"startTime":"1970-01-01T00:00:00Z","endTime":"1970-01-01T00:00:05Z"}}`, text)
}

func TestQueryLokiLogsFormatLokiMetrics(t *testing.T) {
	metadata := `"metadata":{"linesReturned":1,"maxLinesAllowed":10,"resultsTruncated":false,"totalLinesScanned":null,
		"startTime":"1970-01-01T00:00:00Z","endTime":"1970-01-01T00:00:05Z"}`
	for _, tc := range []struct {
		queryType, data string
	}{
		{
			queryType: "range",
			data:      `{"resultType":"matrix","result":[{"metric":{"app":"a"},"values":[[1,"3"],[2.5,"4.5"]]}]}`,
		},
		{
			queryType: "instant",
			data:      `{"resultType":"vector","result":[{"metric":{"app":"a"},"value":[2.5,"4.5"]}]}`,
		},
	} {
		t.Run(tc.queryType, func(t *testing.T) {
			args := testLokiRateArgs + `,"format":"loki","queryType":"` + tc.queryType + `"}`
			text := callQueryLokiLogs(t, args, testLokiMetricFrame())
			assert.JSONEq(t, `{"status":"success","data":`+tc.data+`,`+metadata+`}`, text)
		})
	}
}

func TestQueryLokiLogsFormatLokiUnlabeledSeries(t *testing.T) {
	frame := data.NewFrame("",
		data.NewField("Time", nil, []time.Time{time.UnixMilli(1000)}),
		data.NewField("Value", nil, []float64{0}),
	)
	args := `{"datasourceUid":"loki","logql":"vector(0)","format":"loki","queryType":"instant",` + testLokiTimeArgs + `}`
	var got struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(callQueryLokiLogs(t, args, frame)), &got))
	assert.JSONEq(t, `{"resultType":"vector","result":[{"metric":{},"value":[1,"0"]}]}`, string(got.Data))
}

func TestQueryLokiLogsFormatLokiEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, logql, queryType, resultType string
	}{
		{name: "log range", logql: `{app="a"} |= "x"`, queryType: "range", resultType: "streams"},
		{name: "log instant", logql: `{app="a"}`, queryType: "instant", resultType: "streams"},
		{name: "metric range", logql: `count_over_time({app="a"}[1m])`, queryType: "range", resultType: "matrix"},
		{name: "metric instant", logql: `count_over_time({app="a"}[1m])`, queryType: "instant", resultType: "vector"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := fmt.Sprintf(`{"datasourceUid":"loki","logql":%q,"queryType":%q,"format":"loki",%s}`,
				tc.logql, tc.queryType, testLokiTimeArgs)
			var got struct {
				Status string            `json:"status"`
				Data   json.RawMessage   `json:"data"`
				Hints  *EmptyResultHints `json:"hints"`
			}
			require.NoError(t, json.Unmarshal([]byte(callQueryLokiLogs(t, args)), &got))
			assert.Equal(t, "success", got.Status)
			assert.JSONEq(t, `{"resultType":"`+tc.resultType+`","result":[]}`, string(got.Data))
			assert.NotNil(t, got.Hints)
		})
	}
}

func TestQueryLokiLogsInvalidFormat(t *testing.T) {
	result := lokiToolResult(t, testLokiLogArgs+`,"format":"json"}`)
	assert.True(t, result.IsError)
	assert.Equal(t, `invalid format "json": must be 'full', 'compact' or 'loki'`,
		result.Content[0].(*mcp.TextContent).Text)
}

func TestIsLogQLLogQuery(t *testing.T) {
	for logql, want := range map[string]bool{
		`{app="a"}`:                              true,
		` ({app="a"} |= "x")`:                    true,
		"# note\n{app=\"a\"}":                    true,
		`/* note */ {app="a"}`:                   true,
		`count_over_time({app="a"}[1m])`:         false,
		`(sum(rate({app="a"}[1m])))`:             false,
		`sum by (app) (rate({app="a"}[1m])) > 1`: false,
		`vector(0)`:                              false,
		"# {app=\"a\"}\nvector(0)":               false,
		`/* {app="a"}`:                           false,
	} {
		assert.Equal(t, want, isLogQLLogQuery(logql), logql)
	}
}

func TestLokiClient_MakeRequest_SetsQueryTags(t *testing.T) {
	var gotTags string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTags = r.Header.Get("X-Query-Tags")
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	c := &Client{httpClient: server.Client(), baseURL: server.URL}
	_, err := c.fetchData(context.Background(), "/loki/api/v1/labels", "", "", "")
	require.NoError(t, err)
	assert.Equal(t, "Source=mcp-grafana", gotTags)
}

// grafanaLokiStub mimics how Grafana routes a Loki datasource: the proxy
// path forwards the request path to Loki unchanged, while the /resources
// handler prepends /loki/api/v1/ itself (pkg/tsdb/loki callResource). Loki
// answers a path it does not serve with its UI's index.html, which Grafana's
// resource handler relays as Content-Type: application/json.
func grafanaLokiStub(proxyStatus int, body string) (*httptest.Server, *[]string) {
	var paths []string
	const proxyBase = "/api/datasources/proxy/uid/loki"
	const resourcesBase = "/api/datasources/uid/loki/resources/"
	serveLoki := func(w http.ResponseWriter, lokiPath string) {
		w.Header().Set("Content-Type", "application/json")
		switch lokiPath {
		case "/loki/api/v1/query_range", "/loki/api/v1/query", "/loki/api/v1/index/stats":
			_, _ = io.WriteString(w, body)
		default:
			_, _ = io.WriteString(w, "<!DOCTYPE html>\n<html lang=\"en\"><head><title>Loki UI</title></head></html>")
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch {
		case r.URL.Path == "/api/datasources/uid/loki":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"uid":"loki","type":"loki"}`)
		case strings.HasPrefix(r.URL.Path, proxyBase+"/"):
			if proxyStatus != http.StatusOK {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(proxyStatus)
				_, _ = io.WriteString(w, `{"message":"query timed out"}`)
				return
			}
			serveLoki(w, strings.TrimPrefix(r.URL.Path, proxyBase))
		case strings.HasPrefix(r.URL.Path, resourcesBase):
			if r.URL.Query().Get("query") == "" {
				http.Error(w, "query parameter not forwarded", http.StatusBadRequest)
				return
			}
			serveLoki(w, "/loki/api/v1/"+strings.TrimPrefix(r.URL.Path, resourcesBase))
		default:
			http.NotFound(w, r)
		}
	})), &paths
}

// A 500 on the proxy path falls back to /resources. For Loki that route must
// drop the /loki/api/v1 prefix, which Grafana adds back; otherwise Loki gets
// /loki/api/v1/loki/api/v1/index/stats and answers with its UI page. Stats
// (like labels and patterns) still go through the proxy client; log queries
// use /api/ds/query and never reach this path.
func TestQueryLokiStatsFallsBackToLokiResourcesRoute(t *testing.T) {
	resetFallbackCache()
	t.Cleanup(resetFallbackCache)

	server, paths := grafanaLokiStub(http.StatusInternalServerError, `{"streams":2,"chunks":3,"bytes":4096,"entries":5}`)
	defer server.Close()

	stats, err := queryLokiStats(enforceTestCtx(server, false), QueryLokiStatsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`,
	})
	require.NoError(t, err)
	assert.Equal(t, &Stats{Streams: 2, Chunks: 3, Bytes: 4096, Entries: 5}, stats)
	assert.Contains(t, *paths, "/api/datasources/uid/loki/resources/index/stats")
	assert.NotContains(t, *paths, "/api/datasources/uid/loki/resources/loki/api/v1/index/stats")
}

func TestRunPanelQueryLokiKeepsMacroLikeVariableValue(t *testing.T) {
	server, payload := lokiQueryStub()
	defer server.Close()
	db := map[string]interface{}{
		"templating": map[string]interface{}{"list": []interface{}{
			map[string]interface{}{"name": "app", "type": "custom", "current": map[string]interface{}{"value": "$__auto"}},
		}},
		"panels": []interface{}{map[string]interface{}{
			"id": float64(1), "datasource": map[string]interface{}{"uid": "loki", "type": "loki"},
			"targets": []interface{}{map[string]interface{}{"refId": "A", "expr": `sum(count_over_time({app="$app"}[$__auto]))`}},
		}},
	}

	_, err := runSinglePanelQuery(enforceTestCtx(server, false), singlePanelQueryParams{
		DB: db, PanelID: 1, Start: "2024-01-15T10:00:00Z", End: "2024-01-15T11:00:00Z",
	})
	require.NoError(t, err)
	q := (*payload)["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, `sum(count_over_time({app="$__auto"}[30s]))`, q["expr"])
}
