//go:build unit

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tempoTestServer creates a mock server that handles both the Grafana datasource
// API (for getDatasourceByUID) and the Tempo proxy and /api/ds/query endpoints.
func tempoTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, func()) {
	t.Helper()

	mux := http.NewServeMux()

	// Mock the Grafana datasource API — getDatasourceByUID calls this
	mux.HandleFunc("/api/datasources/uid/test-tempo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uid":  "test-tempo",
			"name": "Test Tempo",
			"type": "tempo",
			"id":   1,
		})
	})

	// Mock the Grafana datasource list API
	mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"uid": "test-tempo", "name": "Test Tempo", "type": "tempo", "id": 1},
		})
	})

	// Route all Tempo API calls through the proxy path
	mux.HandleFunc("/api/datasources/proxy/uid/test-tempo/", handler)
	mux.HandleFunc("/api/ds/query", handler)

	// Frontend settings
	mux.HandleFunc("/api/frontend/settings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})

	ts := httptest.NewServer(mux)

	return ts, ts.Close
}

// tempoDSQueryServer answers /api/ds/query with frames, recording the query
// it received.
func tempoDSQueryServer(t *testing.T, frames data.Frames) (*httptest.Server, func(), *map[string]any) {
	t.Helper()
	payload := &map[string]any{}
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ds/query" {
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(payload)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{
			"A": backend.DataResponse{Frames: frames},
		}})
	})
	return ts, cleanup, payload
}

// dsQueryOf returns the single query of a recorded /api/ds/query payload.
func dsQueryOf(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	queries, _ := payload["queries"].([]any)
	require.Len(t, queries, 1)
	return queries[0].(map[string]any)
}

// tempoTracesFrame builds the frames of Grafana's "traces" table type for
// one trace whose only span set is spans. A nil spans frame gives no traces.
func tempoTracesFrame(t *testing.T, spans *data.Frame) data.Frames {
	t.Helper()
	if spans == nil {
		return data.Frames{data.NewFrame("Traces", data.NewField("traceID", nil, []string{}))}
	}
	nested, err := json.Marshal([]*data.Frame{spans})
	require.NoError(t, err)
	duration := 125.0
	return data.Frames{data.NewFrame("Traces",
		data.NewField("traceID", nil, []string{"abc123"}),
		data.NewField("startTime", nil, []time.Time{time.UnixMilli(1735689600001)}),
		data.NewField("traceService", nil, []string{"frontend"}),
		data.NewField("traceName", nil, []string{"GET /"}),
		data.NewField("traceDuration", nil, []*float64{&duration}),
		data.NewField("nested", nil, []json.RawMessage{nested}),
	)}
}

func tempoTestContext(t *testing.T, serverURL string) func(*mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	t.Helper()
	return tempoTestContextWithVersion(t, serverURL, "13.2.0")
}

// tempoTestContextWithVersion is tempoTestContext for a Grafana reporting the
// given version, which decides whether queries use /api/ds/query.
func tempoTestContextWithVersion(t *testing.T, serverURL, version string) func(*mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	t.Helper()

	u, _ := url.Parse(serverURL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"

	c := client.NewHTTPClientWithConfig(nil, cfg)
	ctx := mcpgrafana.WithGrafanaClient(
		mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: serverURL}),
		&mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c, Version: version},
	)

	tools := map[string]mcpgrafana.Tool{
		"search_tempo_traces":         SearchTempoTracesTool,
		"query_tempo_metrics":         QueryTempoMetricsTool,
		"get_tempo_trace":             GetTempoTraceTool,
		"diff_tempo_traces":           DiffTempoTracesTool,
		"list_tempo_attribute_names":  ListTempoAttributeNamesTool,
		"list_tempo_attribute_values": ListTempoAttributeValuesTool,
		"get_tempo_traceql_docs":      GetTempoTraceQLDocsTool,
	}

	return func(req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tool, ok := tools[req.Params.Name]
		if !ok {
			return nil, fmt.Errorf("unknown tool: %s", req.Params.Name)
		}
		return tool.Handler(ctx, req)
	}
}

func makeTempoRequest(toolName string, args map[string]any) *mcp.CallToolRequest {
	argsJSON, _ := json.Marshal(args)
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      toolName,
			Arguments: argsJSON,
		},
	}
}

func TestTempoSearch(t *testing.T) {
	spans := data.NewFrame("Spans",
		data.NewField("traceIdHidden", nil, []string{"abc123"}),
		data.NewField("spanID", nil, []string{"s1"}),
		data.NewField("time", nil, []time.Time{time.UnixMilli(1735689600002)}),
		data.NewField("name", nil, []string{""}),
		data.NewField("cache.hit", nil, []bool{false}),
		data.NewField("http.status_code", nil, []float64{500}),
		data.NewField("note", nil, []string{""}),
		data.NewField("duration", nil, []float64{2_500_000}),
	)
	ts, cleanup, payload := tempoDSQueryServer(t, tempoTracesFrame(t, spans))
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ span.http.status_code >= 500 }`,
		"start":         "2025-01-01T00:00:00Z",
		"end":           "2025-01-01T01:00:00Z",
		"spss":          2,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "%v", result.Content)
	assert.JSONEq(t, `{"traces":[{"traceID":"abc123","rootServiceName":"frontend","rootTraceName":"GET /","startTimeMs":1735689600001,"durationMs":125,`+
		`"spanSets":[{"spans":[{"spanID":"s1","startTimeMs":1735689600002,"durationMs":2.5,`+
		`"attributes":{"cache.hit":false,"http.status_code":500,"note":""}}]}]}]}`, result.Content[0].(*mcp.TextContent).Text,
		"zero-valued attributes keep their types")

	assert.Equal(t, "1735689600000", (*payload)["from"])
	assert.Equal(t, "1735693200000", (*payload)["to"])
	q := dsQueryOf(t, *payload)
	assert.Equal(t, "traceql", q["queryType"])
	assert.Equal(t, "traces", q["tableType"])
	assert.Equal(t, `{ span.http.status_code >= 500 }`, q["query"])
	assert.Equal(t, float64(tempoDefaultSearchLimit), q["limit"])
	assert.Equal(t, float64(2), q["spss"])
	assert.Equal(t, map[string]any{"uid": "test-tempo", "type": "tempo"}, q["datasource"])
}

// Grafana before 12.3 cannot run TraceQL searches through /api/ds/query.
func TestTempoSearch_OldGrafanaUsesProxy(t *testing.T) {
	var capturedPath string
	var capturedQuery url.Values
	var capturedAccept string

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query()
		capturedAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"traces":[{"traceID":"abc123","rootServiceName":"frontend","spanSets":[{"spans":[{"spanID":"s1","attributes":[{"key":"http.status_code","value":{"intValue":"500"}}]}]}]}]}`))
	})
	defer cleanup()

	call := tempoTestContextWithVersion(t, ts.URL, "12.2.0")
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ span.http.status_code >= 500 }`,
		"start":         "2025-01-01T00:00:00Z",
		"end":           "2025-01-01T01:00:00Z",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"attributes":{"http.status_code":500}`, "proxy output is flattened the same way")

	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/search", capturedPath)
	assert.Equal(t, `{ span.http.status_code >= 500 }`, capturedQuery.Get("q"))
	assert.Equal(t, "1735689600", capturedQuery.Get("start"))
	assert.Equal(t, "1735693200", capturedQuery.Get("end"))
	assert.Equal(t, "application/vnd.grafana.llm", capturedAccept)
}

// On the proxy, Tempo's own response is tidied.
func TestTempoSearch_LimitAndDedupe(t *testing.T) {
	var capturedQuery url.Values
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"traces":[{"traceID":"abc123",` +
			`"spanSet":{"spans":[{"spanID":"s1"}],"matched":1},` +
			`"spanSets":[{"spans":[{"spanID":"s1"}],"matched":1}]}],` +
			`"metrics":{"inspectedBytes":100,"completedJobs":1,"totalJobs":1470}}`))
	})
	defer cleanup()

	call := tempoTestContextWithVersion(t, ts.URL, "12.2.0")
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ }`,
		"limit":         1,
		"spss":          5,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "1", capturedQuery.Get("limit"))
	assert.Equal(t, "5", capturedQuery.Get("spss"))

	text := result.Content[0].(*mcp.TextContent).Text
	assert.Equal(t, 1, strings.Count(text, `"spanID":"s1"`), "spans should not be duplicated: %s", text)
	assert.NotContains(t, text, `"spanSet":`)
	assert.Contains(t, text, `"inspectedBytes":100`)
	assert.Contains(t, text, "Partial results: the search stopped after 1 of 1470 jobs because it reached the limit of 1 traces")
}

func TestTidyTempoSearchResults(t *testing.T) {
	t.Run("complete searches get no note", func(t *testing.T) {
		out := tidyTempoSearchResults(`{"traces":[],"metrics":{"completedJobs":3,"totalJobs":3}}`, 20)
		assert.NotContains(t, out, "note")
	})
	t.Run("incomplete jobs below the limit", func(t *testing.T) {
		out := tidyTempoSearchResults(`{"traces":[{"traceID":"a"}],"metrics":{"completedJobs":2,"totalJobs":3}}`, 20)
		assert.Contains(t, out, "only 2 of 3 search jobs completed")
	})
	t.Run("unparseable body passes through", func(t *testing.T) {
		assert.Equal(t, "not json", tidyTempoSearchResults("not json", 20))
	})
}

func TestTempoSearch_DefaultTimeBounds(t *testing.T) {
	ts, cleanup, payload := tempoDSQueryServer(t, tempoTracesFrame(t, nil))
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ }`,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	from, err := strconv.ParseInt((*payload)["from"].(string), 10, 64)
	require.NoError(t, err)
	to, err := strconv.ParseInt((*payload)["to"].(string), 10, 64)
	require.NoError(t, err)
	assert.Equal(t, time.Hour.Milliseconds(), to-from, "an omitted range should default to the past hour")
}

func TestTempoSearch_PartialTimeBounds(t *testing.T) {
	ts, cleanup, payload := tempoDSQueryServer(t, tempoTracesFrame(t, nil))
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ }`,
		"start":         "2025-01-01T00:00:00Z",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "1735689600000", (*payload)["from"])
	assert.NotEmpty(t, (*payload)["to"], "end should be defaulted when only start is provided")
}

func TestTempoMetricsInstant(t *testing.T) {
	frame := data.NewFrame("GET",
		data.NewField("time", nil, []time.Time{time.UnixMilli(1735693200000)}),
		data.NewField("value", data.Labels{"span.http.method": `"GET"`}, []float64{42}),
	)
	ts, cleanup, payload := tempoDSQueryServer(t, data.Frames{frame})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("query_tempo_metrics", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ } | count_over_time() by (span.http.method)`,
		"type":          "instant",
		"start":         "2025-01-01T00:00:00Z",
		"end":           "2025-01-01T01:00:00Z",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	assert.JSONEq(t, `{"series":[{"labels":{"span.http.method":"GET"},"value":42}]}`, result.Content[0].(*mcp.TextContent).Text)
	assert.Equal(t, "metrics-instant", result.Meta["type"])

	assert.Equal(t, "1735689600000", (*payload)["from"])
	assert.Equal(t, "1735693200000", (*payload)["to"])
	q := dsQueryOf(t, *payload)
	assert.Equal(t, "traceql", q["queryType"])
	assert.Equal(t, "instant", q["metricsQueryType"])
}

// Grafana 11.5 runs every metrics query through /api/ds/query as a range
// query, so instant queries stay on the proxy until 11.6.
func TestTempoMetrics_InstantGate(t *testing.T) {
	for version, wantPath := range map[string]string{
		"11.5.3": "/api/datasources/proxy/uid/test-tempo/api/metrics/query",
		"11.6.0": "/api/ds/query",
	} {
		t.Run(version, func(t *testing.T) {
			var capturedPath string
			ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			})
			defer cleanup()
			call := tempoTestContextWithVersion(t, ts.URL, version)
			_, err := call(makeTempoRequest("query_tempo_metrics", map[string]any{
				"datasourceUid": "test-tempo", "query": `{ } | rate()`, "type": "instant",
			}))
			require.NoError(t, err)
			assert.Equal(t, wantPath, capturedPath)
		})
	}
}

// Grafana picks search or metrics from the query text, so each tool rejects
// the other kind of result.
func TestTempoWrongQueryKind(t *testing.T) {
	ts, cleanup, _ := tempoDSQueryServer(t, tempoTracesFrame(t, nil))
	defer cleanup()
	result, err := tempoTestContext(t, ts.URL)(makeTempoRequest("query_tempo_metrics", map[string]any{
		"datasourceUid": "test-tempo", "query": `{ status = error }`,
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "not a TraceQL metrics query")

	series := data.NewFrame("", data.NewField("time", nil, []time.Time{time.UnixMilli(1)}), data.NewField("value", nil, []float64{1}))
	ts2, cleanup2, _ := tempoDSQueryServer(t, data.Frames{series})
	defer cleanup2()
	result, err = tempoTestContext(t, ts2.URL)(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo", "query": `{ } | rate()`,
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "metrics queries")
}

// Grafana before 11.5 cannot run TraceQL metrics through /api/ds/query.
func TestTempoMetrics_OldGrafanaUsesProxy(t *testing.T) {
	var capturedPath string
	var capturedQuery url.Values

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"series":[{"labels":{},"samples":[{"value":42}]}]}`))
	})
	defer cleanup()

	call := tempoTestContextWithVersion(t, ts.URL, "11.4.2")
	result, err := call(makeTempoRequest("query_tempo_metrics", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ } | count_over_time()`,
		"type":          "instant",
		"start":         "2025-01-01T00:00:00Z",
		"end":           "2025-01-01T01:00:00Z",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/metrics/query", capturedPath)
	assert.Equal(t, "1735689600000000000", capturedQuery.Get("start"))
	assert.Equal(t, "1735693200000000000", capturedQuery.Get("end"))
}

func TestTempoMetrics_DefaultTimeBounds(t *testing.T) {
	ts, cleanup, payload := tempoDSQueryServer(t, nil)
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("query_tempo_metrics", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ } | rate()`,
		"type":          "range",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.JSONEq(t, `{"series":[]}`, result.Content[0].(*mcp.TextContent).Text)
	assert.NotEmpty(t, (*payload)["from"], "start should be set when omitted by caller")
	assert.NotEmpty(t, (*payload)["to"], "end should be set when omitted by caller")
}

func TestTempoMetricsRange(t *testing.T) {
	series := data.NewFrame("GET",
		data.NewField("time", nil, []time.Time{time.UnixMilli(1000), time.UnixMilli(2000)}),
		data.NewField("GET", data.Labels{"span.http.method": `"GET"`, "status.code": "2"}, []float64{3, 4}),
	)
	series.SetMeta(&data.FrameMeta{Type: data.FrameTypeTimeSeriesMulti})
	exemplars := data.NewFrame("exemplar",
		data.NewField("Time", nil, []time.Time{time.UnixMilli(1500)}),
		data.NewField("Value", nil, []float64{0.25}),
		data.NewField("traceId", nil, []string{"abc123"}),
		data.NewField("span.http.method", nil, []string{"GET"}),
	)
	exemplars.RefID = "GET"
	exemplars.SetMeta(&data.FrameMeta{DataTopic: data.DataTopicAnnotations})
	ts, cleanup, payload := tempoDSQueryServer(t, data.Frames{series, exemplars})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("query_tempo_metrics", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ } | rate() by (span.http.method)`,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	assert.JSONEq(t, `{"series":[{"labels":{"span.http.method":"GET","status.code":"2"},`+
		`"samples":[{"timestampMs":1000,"value":3},{"timestampMs":2000,"value":4}],`+
		`"exemplars":[{"traceId":"abc123","timestampMs":1500,"value":0.25}]}]}`, result.Content[0].(*mcp.TextContent).Text)
	assert.Equal(t, "metrics-range", result.Meta["type"])
	assert.Equal(t, "range", dsQueryOf(t, *payload)["metricsQueryType"])
}

func TestTempoMetricsDefaultsToRange(t *testing.T) {
	ts, cleanup, payload := tempoDSQueryServer(t, nil)
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("query_tempo_metrics", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ } | rate()`,
	}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "range", dsQueryOf(t, *payload)["metricsQueryType"])
}

func TestTempoMetricsInvalidType(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("query_tempo_metrics", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ } | rate()`,
		"type":          "bogus",
	}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "invalid type")
}

func TestTempoGetTrace(t *testing.T) {
	ts, cleanup, payload := tempoDSQueryServer(t, data.Frames{testTraceFrame(testTraceData("stack"))})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("get_tempo_trace", map[string]any{
		"datasourceUid": "test-tempo",
		"trace_id":      testTraceID,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	assert.Equal(t, "0", (*payload)["from"], "zero bounds search all time")
	assert.Equal(t, "0", (*payload)["to"])
	q := dsQueryOf(t, *payload)
	assert.Equal(t, "traceId", q["queryType"])
	assert.Equal(t, testTraceID, q["query"])

	var trace tempoTrace
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &trace))
	assert.Equal(t, testTraceID, trace.TraceID)
	require.Len(t, trace.Services, 1, "spans from one resource share a service entry")
	svc := trace.Services[0]
	assert.Equal(t, "checkout", svc.ServiceName)
	assert.Equal(t, map[string]any{"service.name": "checkout", "deployment.environment.name": "production"}, svc.Resource)
	require.Len(t, svc.Spans, 2)
	root, child := svc.Spans[0], svc.Spans[1]
	assert.Equal(t, "0102030405060708", root.SpanID)
	assert.Equal(t, "POST /checkout", root.Name)
	assert.Equal(t, float64(1750000000000), root.StartTimeMs)
	assert.Equal(t, float64(125), root.DurationMs)
	assert.Empty(t, root.Status, "unset status is omitted")
	assert.Equal(t, map[string]any{"http.request.method": "POST"}, root.Attributes)
	require.Len(t, root.Events, 1)
	assert.Equal(t, "stack", root.Events[0].Attributes["exception.stacktrace"])
	assert.Equal(t, "0102030405060708", child.ParentSpanID)
	assert.Equal(t, "ok", child.Status)

	require.NotNil(t, result.StructuredContent)
	structured := result.StructuredContent.(TraceViewResult)
	require.Len(t, structured.Spans, 2)
	assert.Equal(t, "error", structured.Spans[0].Status, "the viewer marks spans with exceptions as errors")
	assert.Equal(t, "production", structured.Spans[0].Attributes["resource.deployment.environment.name"])
}

func TestTempoTraceFromFrames(t *testing.T) {
	t.Run("partial traces are returned without the viewer", func(t *testing.T) {
		frame := testTraceFrame(testTraceData("stack"))
		frame.Meta.Custom = map[string]any{"partial": true, "message": "trace exceeded max bytes"}
		trace, spans, err := tempoTraceFromFrames(testTraceID, data.Frames{frame})
		require.NoError(t, err)
		assert.True(t, trace.Partial)
		assert.Equal(t, "trace exceeded max bytes", trace.Message)
		assert.Len(t, trace.Services[0].Spans, 2)
		assert.Nil(t, spans)
	})
	t.Run("large integers stay exact", func(t *testing.T) {
		frame := testTraceFrame(testTraceData("stack"))
		f, _ := frame.FieldByName("tags")
		f.Set(0, json.RawMessage(`[{"key":"count","value":9007199254740993},{"key":"links","value":"[1,2]"}]`))
		trace, _, err := tempoTraceFromFrames(testTraceID, data.Frames{frame})
		require.NoError(t, err)
		assert.Equal(t, "9007199254740993", trace.Services[0].Spans[0].Attributes["count"])
		assert.Equal(t, "[1,2]", trace.Services[0].Spans[0].Attributes["links"])
	})
	t.Run("links", func(t *testing.T) {
		frame := testTraceFrame(testTraceData("stack"))
		f, _ := frame.FieldByName("references")
		f.Set(0, json.RawMessage(`[{"traceID":"t2","spanID":"s2","tags":[{"key":"kind","value":"follows"}]}]`))
		trace, _, err := tempoTraceFromFrames(testTraceID, data.Frames{frame})
		require.NoError(t, err)
		assert.Equal(t, []tempoTraceLink{{TraceID: "t2", SpanID: "s2", Attributes: map[string]any{"kind": "follows"}}}, trace.Services[0].Spans[0].Links)
	})
	t.Run("no frames", func(t *testing.T) {
		_, _, err := tempoTraceFromFrames(testTraceID, nil)
		assert.ErrorContains(t, err, "not found")
	})
	t.Run("status before Grafana 10.1", func(t *testing.T) {
		// Nothing before 10.1 has a statusCode column: the status is in the
		// otel.status_* tags.
		frame := data.NewFrame("Trace",
			data.NewField("spanID", nil, []string{"0102030405060708"}),
			data.NewField("serviceName", nil, []string{"checkout"}),
			data.NewField("tags", nil, []json.RawMessage{json.RawMessage(`[{"key":"otel.status_code","value":2},{"key":"otel.status_description","value":"boom"}]`)}),
		)
		trace, spans, err := tempoTraceFromFrames(testTraceID, data.Frames{frame})
		require.NoError(t, err)
		span := trace.Services[0].Spans[0]
		assert.Equal(t, "error", span.Status)
		assert.Equal(t, "boom", span.StatusMessage)
		require.Len(t, spans, 1)
		assert.Equal(t, "error", spans[0].Status)
	})
}

func TestFlattenTempoAttributes(t *testing.T) {
	var v map[string]any
	require.NoError(t, unmarshalTempoJSON(json.RawMessage(`{"attributes":[
		{"key":"s","value":{"stringValue":"GET"}},
		{"key":"i","value":{"intValue":"9007199254740993"}},
		{"key":"a","value":{"arrayValue":{"values":[{"stringValue":"x"},{"intValue":"2"}]}}},
		{"key":"kv","value":{"kvlistValue":{"values":[{"key":"k","value":{"boolValue":true}}]}}}
	]}`), &v))
	flattenTempoAttributes(v)
	out, err := json.Marshal(v)
	require.NoError(t, err)
	assert.JSONEq(t, `{"attributes":{"s":"GET","i":9007199254740993,"a":["x",2],"kv":{"k":true}}}`, string(out))
}

func TestTempoGetTrace_NotFound(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"results":{"A":{"error":"trace with id 0000000000000000000000000000dead not found","status":500}}}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("get_tempo_trace", map[string]any{
		"datasourceUid": "test-tempo",
		"trace_id":      "0000000000000000000000000000dead",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "trace with id 0000000000000000000000000000dead not found")
}

func TestTempoTraceDiff(t *testing.T) {
	var capturedPath string
	var capturedMethod string
	var capturedBody map[string]any

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"summary":{"base":{"spanCount":10},"compare":{"spanCount":12}}}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("diff_tempo_traces", map[string]any{
		"datasourceUid":    "test-tempo",
		"base_trace_id":    "trace-a",
		"compare_trace_id": "trace-b",
		"format":           "trace-summary-v0-native",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/v2/traces/diff", capturedPath)
	assert.Equal(t, "POST", capturedMethod)

	base := capturedBody["base"].(map[string]any)
	assert.Equal(t, "trace-a", base["traceID"])
	compare := capturedBody["compare"].(map[string]any)
	assert.Equal(t, "trace-b", compare["traceID"])
	assert.Equal(t, "trace-summary-v0-native", capturedBody["format"])
}

func TestTempoTraceDiff_WithTimeRanges(t *testing.T) {
	var capturedBody map[string]any

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	_, err := call(makeTempoRequest("diff_tempo_traces", map[string]any{
		"datasourceUid":    "test-tempo",
		"base_trace_id":    "trace-a",
		"compare_trace_id": "trace-b",
		"base_start":       "2025-01-01T00:00:00Z",
		"base_end":         "2025-01-01T01:00:00Z",
		"compare_start":    "2025-01-02T00:00:00Z",
		"compare_end":      "2025-01-02T01:00:00Z",
	}))
	require.NoError(t, err)

	base := capturedBody["base"].(map[string]any)
	assert.Equal(t, float64(1735689600000000000), base["start"])
	assert.Equal(t, float64(1735693200000000000), base["end"])
}

func TestTempoTraceDiff_MismatchedTimeRange(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("diff_tempo_traces", map[string]any{
		"datasourceUid":    "test-tempo",
		"base_trace_id":    "trace-a",
		"compare_trace_id": "trace-b",
		"base_start":       "2025-01-01T00:00:00Z",
		// base_end missing — should error
	}))
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
}

func TestTempoGetAttributeNames(t *testing.T) {
	var capturedPath string
	var capturedQuery url.Values

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scopes":[{"name":"span","tags":["http.method","http.status_code"]}]}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("list_tempo_attribute_names", map[string]any{
		"datasourceUid": "test-tempo",
		"scope":         "span",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/v2/search/tags", capturedPath)
	assert.Equal(t, "span", capturedQuery.Get("scope"))
}

func TestTempoGetAttributeNames_SummarizesLargeUnscopedResponse(t *testing.T) {
	// Generate a response larger than tempoAttributeNamesSummaryThreshold.
	tags := make([]string, 2000)
	for i := range tags {
		tags[i] = fmt.Sprintf("attribute.name.%04d.padding.to.make.it.long.enough", i)
	}
	tagsJSON, _ := json.Marshal(tags)
	responseBody := fmt.Sprintf(`{"scopes":[{"name":"resource","tags":%s},{"name":"span","tags":["http.method"]}]}`, tagsJSON)

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseBody))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("list_tempo_attribute_names", map[string]any{
		"datasourceUid": "test-tempo",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	text := result.Content[0].(*mcp.TextContent).Text
	assert.Contains(t, text, "resource: 2000 attributes")
	assert.Contains(t, text, "span: 1 attributes")
	assert.Contains(t, text, "scope parameter")
	assert.Equal(t, "attribute-names-summary", result.Meta["type"])
}

func TestTempoGetAttributeNames_NoSummaryWhenScoped(t *testing.T) {
	// Even if the response is large, when a scope is provided we return full results.
	tags := make([]string, 2000)
	for i := range tags {
		tags[i] = fmt.Sprintf("attribute.name.%04d.padding.to.make.it.long.enough", i)
	}
	tagsJSON, _ := json.Marshal(tags)
	responseBody := fmt.Sprintf(`{"scopes":[{"name":"resource","tags":%s}]}`, tagsJSON)

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseBody))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("list_tempo_attribute_names", map[string]any{
		"datasourceUid": "test-tempo",
		"scope":         "resource",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "attribute-names", result.Meta["type"])
}

func TestTempoGetAttributeValues(t *testing.T) {
	var capturedPath string
	var capturedQuery url.Values

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tagValues":[{"type":"string","value":"GET"},{"type":"string","value":"POST"}]}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("list_tempo_attribute_values", map[string]any{
		"datasourceUid": "test-tempo",
		"name":          "span.http.method",
		"filter-query":  `{ resource.service.name = "frontend" }`,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.True(t, strings.HasPrefix(capturedPath, "/api/datasources/proxy/uid/test-tempo/api/v2/search/tag/"))
	assert.Contains(t, capturedPath, "span.http.method")
	assert.Equal(t, `{ resource.service.name = "frontend" }`, capturedQuery.Get("q"))
}

func TestTempoGetAttributeValues_RejectsORFilter(t *testing.T) {
	called := false
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("list_tempo_attribute_values", map[string]any{
		"datasourceUid": "test-tempo",
		"name":          "resource.service.name",
		"filter-query":  `{ resource.service.name = "a" || resource.service.name = "b" }`,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "OR conditions")
	assert.False(t, called, "an OR filter must be rejected before any request to Tempo")
}

func TestTempoSearch_Non200Response(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid query syntax"}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         "invalid query",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "400")
}

func TestTempoSearch_MissingDatasourceUid(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach backend")
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"query": `{ }`,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
}

func TestTempoToolResult_HasMeta(t *testing.T) {
	result := tempoToolResult("test body", "search-results", "json")
	require.NotNil(t, result.Meta)
	assert.Equal(t, "search-results", result.Meta["type"])
	assert.Equal(t, "json", result.Meta["encoding"])
}

func listTempoTools(t *testing.T, enableQuery bool) map[string]bool {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil)
	AddTempoTools(s, enableQuery)

	ctx := t.Context()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = s.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestTempoFilterHasOR(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"bare OR", `{ a = "x" || b = "y" }`, true},
		{"OR inside quotes is OK", `{ span.db.statement = "a || b" }`, false},
		{"no OR", `{ a = "x" && b = "y" }`, false},
		{"empty", "", false},
		{"escaped quote before OR", `{ a = "x\"" || b = "y" }`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tempoFilterHasOR(tc.query))
		})
	}
}

func TestTempoGetAttributeValues_AllowsORInsideQuotes(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tagValues": [{"type": "string", "value": "test"}]}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("list_tempo_attribute_values", map[string]any{
		"datasourceUid": "test-tempo",
		"name":          "span.db.statement",
		"filter-query":  `{ span.db.statement = "a || b" }`,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "|| inside quotes should not be rejected")
}

func TestAddTempoTools_RegistersAllTools(t *testing.T) {
	expectedTools := []string{
		"search_tempo_traces",
		"query_tempo_metrics",
		"get_tempo_trace",
		"diff_tempo_traces",
		"list_tempo_attribute_names",
		"list_tempo_attribute_values",
		"get_tempo_traceql_docs",
	}

	tools := listTempoTools(t, true)
	for _, name := range expectedTools {
		assert.True(t, tools[name], "expected tool %q to be registered", name)
	}
	assert.Len(t, tools, len(expectedTools))
}

func TestAddTempoTools_DisableQueryRegistersNothing(t *testing.T) {
	tools := listTempoTools(t, false)
	for name := range tools {
		assert.False(t, strings.HasPrefix(name, "search_tempo") || strings.HasPrefix(name, "query_tempo") ||
			strings.HasPrefix(name, "get_tempo") || strings.HasPrefix(name, "diff_tempo") ||
			strings.HasPrefix(name, "list_tempo"),
			"no tempo tools should be registered when query disabled, found %q", name)
	}
}

func TestTempoAPIError_499EmptyBody(t *testing.T) {
	err := tempoAPIError(499, []byte(""))
	assert.Contains(t, err.Error(), "499")
	assert.Contains(t, err.Error(), "timed out")
}

func TestTempoAPIError_499WithBody(t *testing.T) {
	err := tempoAPIError(499, []byte("context cancelled"))
	assert.Contains(t, err.Error(), "499")
	assert.Contains(t, err.Error(), "context cancelled")
	assert.NotContains(t, err.Error(), "timed out")
}

func TestTempoAPIError_Non499(t *testing.T) {
	err := tempoAPIError(400, []byte("bad request"))
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "bad request")
}

func TestTempoSearch_499Timeout(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(499)
	})
	defer cleanup()

	call := tempoTestContextWithVersion(t, ts.URL, "12.2.0")
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         "{ }",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "timed out")
}

func TestTempoTraceQLDocs(t *testing.T) {
	for _, topic := range []string{"basic", "aggregates", "structural", "metrics"} {
		t.Run(topic, func(t *testing.T) {
			result, err := getTempoTraceQLDocs(t.Context(), GetTempoTraceQLDocsParams{Topic: topic})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.False(t, result.IsError)
			text := result.Content[0].(*mcp.TextContent).Text
			assert.Contains(t, text, "TraceQL")
			assert.Equal(t, "traceql-docs", result.Meta["type"])
			assert.Equal(t, "markdown", result.Meta["encoding"])
		})
	}
}

func TestTempoTraceQLDocsInvalidTopic(t *testing.T) {
	result, err := getTempoTraceQLDocs(t.Context(), GetTempoTraceQLDocsParams{Topic: "nonsense"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "invalid topic")
}

func TestAnnotateTraceQLParseError(t *testing.T) {
	parseErr := "tempo API returned 400: query parse error. Consult TraceQL docs tools: parse error at line 1, col 1: syntax error: unexpected quantile_over_time"
	got := annotateTraceQLParseError(parseErr)

	// The original message must survive; the hint is additive.
	assert.Contains(t, got, "unexpected quantile_over_time")
	// A copyable correct query, plus the habits that actually cause the failures.
	assert.Contains(t, got, "quantile_over_time(span:duration, .99) by (span.name)")
	assert.Contains(t, got, "[5m]")
	assert.Contains(t, got, "span:duration")
	assert.Contains(t, got, "_over_time")
	assert.Contains(t, got, "get_tempo_traceql_docs")
}

func TestAnnotateTraceQLParseErrorLeavesOtherErrorsAlone(t *testing.T) {
	for _, msg := range []string{
		"tempo API returned 499: query timed out - try narrowing the time range or simplifying the query",
		"datasource abc is of type loki, not tempo",
		"request failed: connection refused",
	} {
		assert.Equal(t, msg, annotateTraceQLParseError(msg), "non-parse errors must pass through untouched")
	}
}

func TestQueryTempoMetricsAnnotatesParseErrors(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("query parse error. Consult TraceQL docs tools: parse error at line 1, col 1: syntax error: unexpected rate"))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("query_tempo_metrics", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `rate(span.http.method = "POST" [5m])`,
	}))

	require.NoError(t, err)
	require.True(t, result.IsError)
	text := result.Content[0].(*mcp.TextContent).Text
	assert.Contains(t, text, "unexpected rate")
	assert.Contains(t, text, "quantile_over_time(span:duration, .99)")
}

func TestTempoGetTraceFocusValidation(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("invalid focus should not fetch trace") })
	defer cleanup()
	result, err := tempoTestContext(t, ts.URL)(makeTempoRequest("get_tempo_trace", map[string]any{"datasourceUid": "test-tempo", "trace_id": "abc", "focus_span_id": "bad"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "focus_span_id must be 16 hexadecimal characters")
}

func TestTempoGetTrace_TimeRange(t *testing.T) {
	ts, cleanup, payload := tempoDSQueryServer(t, tempoTracesFrame(t, nil))
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	_, err := call(makeTempoRequest("get_tempo_trace", map[string]any{
		"datasourceUid": "test-tempo",
		"trace_id":      "abc123def456",
		"start":         "2022-01-01T00:00:00Z",
		"end":           "2022-01-02T00:00:00Z",
	}))

	require.NoError(t, err)
	assert.Equal(t, "1640995200000", (*payload)["from"])
	assert.Equal(t, "1641081600000", (*payload)["to"])
}

func TestTempoGetTrace_TimeRangeRequiresBoth(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not be sent")
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("get_tempo_trace", map[string]any{
		"datasourceUid": "test-tempo",
		"trace_id":      "abc123def456",
		"start":         "2022-01-01T00:00:00Z",
	}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Equal(t, `arguments "start" and "end" must be provided together`, result.Content[0].(*mcp.TextContent).Text)
}
