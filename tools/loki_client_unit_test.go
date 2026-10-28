//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
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
