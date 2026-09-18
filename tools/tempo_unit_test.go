//go:build unit

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tempoTestServer creates a mock server that handles both the Grafana datasource
// API (for getDatasourceByUID) and the Tempo proxy endpoints.
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

	// Frontend settings
	mux.HandleFunc("/api/frontend/settings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})

	ts := httptest.NewServer(mux)

	return ts, ts.Close
}

func tempoTestContext(t *testing.T, serverURL string) func(mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	t.Helper()

	u, _ := url.Parse(serverURL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"

	c := client.NewHTTPClientWithConfig(nil, cfg)
	ctx := mcpgrafana.WithGrafanaClient(
		mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: serverURL}),
		&mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c},
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

	return func(req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tool, ok := tools[req.Params.Name]
		if !ok {
			return nil, fmt.Errorf("unknown tool: %s", req.Params.Name)
		}
		return tool.Handler(ctx, req)
	}
}

func makeTempoRequest(toolName string, args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}
}

func TestTempoSearch(t *testing.T) {
	var capturedPath string
	var capturedQuery url.Values
	var capturedAccept string

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query()
		capturedAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"traces":[{"traceID":"abc123","rootServiceName":"frontend"}]}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ span.http.status_code >= 500 }`,
		"start":         "2025-01-01T00:00:00Z",
		"end":           "2025-01-01T01:00:00Z",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "abc123")

	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/search", capturedPath)
	assert.Equal(t, `{ span.http.status_code >= 500 }`, capturedQuery.Get("q"))
	assert.Equal(t, "1735689600", capturedQuery.Get("start"))
	assert.Equal(t, "1735693200", capturedQuery.Get("end"))
	assert.Equal(t, "application/vnd.grafana.llm", capturedAccept)
}

func TestTempoSearch_DefaultTimeBounds(t *testing.T) {
	var capturedQuery url.Values

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"traces":[]}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ }`,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.NotEmpty(t, capturedQuery.Get("start"), "start should be set when omitted by caller")
	assert.NotEmpty(t, capturedQuery.Get("end"), "end should be set when omitted by caller")
}

func TestTempoSearch_PartialTimeBounds(t *testing.T) {
	var capturedQuery url.Values

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"traces":[]}`))
	})
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
	assert.Equal(t, "1735689600", capturedQuery.Get("start"))
	assert.NotEmpty(t, capturedQuery.Get("end"), "end should be defaulted when only start is provided")
}

func TestTempoMetricsInstant(t *testing.T) {
	var capturedPath string
	var capturedQuery url.Values

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"series":[{"labels":{},"samples":[{"value":42}]}]}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
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
	var capturedQuery url.Values

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"series":[]}`))
	})
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
	assert.NotEmpty(t, capturedQuery.Get("start"), "start should be set when omitted by caller")
	assert.NotEmpty(t, capturedQuery.Get("end"), "end should be set when omitted by caller")
}

func TestTempoMetricsRange(t *testing.T) {
	var capturedPath string

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"series":[]}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("query_tempo_metrics", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ } | rate()`,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/metrics/query_range", capturedPath)
}

func TestTempoMetricsDefaultsToRange(t *testing.T) {
	var capturedPath string

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"series":[]}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("query_tempo_metrics", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ } | rate()`,
	}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/metrics/query_range", capturedPath)
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
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "invalid type")
}

func TestTempoGetTrace(t *testing.T) {
	var capturedPath string
	var capturedAccept string

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"batches":[{"resource":{"attributes":[]},"scopeSpans":[]}]}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("get_tempo_trace", map[string]any{
		"datasourceUid": "test-tempo",
		"trace_id":      "abc123def456",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/v2/traces/abc123def456", capturedPath)
	assert.Equal(t, "application/vnd.grafana.llm", capturedAccept, "should request LLM format without JSON fallback")
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

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "resource: 2000 attributes")
	assert.Contains(t, text, "span: 1 attributes")
	assert.Contains(t, text, "scope parameter")
	assert.Equal(t, "attribute-names-summary", result.Meta.AdditionalFields["type"])
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
	assert.Equal(t, "attribute-names", result.Meta.AdditionalFields["type"])
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
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "OR conditions")
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
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "400")
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
	assert.Equal(t, "search-results", result.Meta.AdditionalFields["type"])
	assert.Equal(t, "json", result.Meta.AdditionalFields["encoding"])
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
	s := server.NewMCPServer("test", "0.1.0")
	AddTempoTools(s, true)

	expectedTools := []string{
		"search_tempo_traces",
		"query_tempo_metrics",
		"get_tempo_trace",
		"diff_tempo_traces",
		"list_tempo_attribute_names",
		"list_tempo_attribute_values",
		"get_tempo_traceql_docs",
	}

	tools := s.ListTools()
	for _, name := range expectedTools {
		_, ok := tools[name]
		assert.True(t, ok, "expected tool %q to be registered", name)
	}
	assert.Len(t, tools, len(expectedTools))
}

func TestAddTempoTools_DisableQueryRegistersNothing(t *testing.T) {
	s := server.NewMCPServer("test", "0.1.0")
	AddTempoTools(s, false)

	tools := s.ListTools()
	for name := range tools {
		assert.False(t, strings.HasPrefix(name, "search_tempo") || strings.HasPrefix(name, "query_tempo") ||
			strings.HasPrefix(name, "get_tempo") || strings.HasPrefix(name, "diff_tempo") ||
			strings.HasPrefix(name, "list_tempo"),
			"no tempo tools should be registered when query disabled, found %q", name)
	}
}

func TestTempoParseStartToEpochSeconds(t *testing.T) {
	epoch, err := tempoParseStartToEpochSeconds("2025-01-01T00:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, "1735689600", epoch)
}

func TestTempoParseEndToEpochSeconds(t *testing.T) {
	epoch, err := tempoParseEndToEpochSeconds("2025-01-01T00:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, "1735689600", epoch)
}

func TestTempoParseStartToEpochNanos(t *testing.T) {
	epoch, err := tempoParseStartToEpochNanos("2025-01-01T00:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, "1735689600000000000", epoch)
}

func TestTempoParseEndToEpochNanos(t *testing.T) {
	epoch, err := tempoParseEndToEpochNanos("2025-01-01T00:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, "1735689600000000000", epoch)
}

func TestTempoParseStartToEpochSeconds_InvalidInput(t *testing.T) {
	_, err := tempoParseStartToEpochSeconds("not-a-date")
	require.Error(t, err)
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

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("search_tempo_traces", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         "{ }",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "timed out")
}

func TestTempoTraceQLDocs(t *testing.T) {
	for _, topic := range []string{"basic", "aggregates", "structural", "metrics"} {
		t.Run(topic, func(t *testing.T) {
			result, err := getTempoTraceQLDocs(t.Context(), GetTempoTraceQLDocsParams{Topic: topic})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.False(t, result.IsError)
			text := result.Content[0].(mcp.TextContent).Text
			assert.Contains(t, text, "TraceQL")
			assert.Equal(t, "traceql-docs", result.Meta.AdditionalFields["type"])
			assert.Equal(t, "markdown", result.Meta.AdditionalFields["encoding"])
		})
	}
}

func TestTempoTraceQLDocsInvalidTopic(t *testing.T) {
	result, err := getTempoTraceQLDocs(t.Context(), GetTempoTraceQLDocsParams{Topic: "nonsense"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "invalid topic")
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
	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "unexpected rate")
	assert.Contains(t, text, "quantile_over_time(span:duration, .99)")
}
