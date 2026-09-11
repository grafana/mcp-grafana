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
		"tempo_traceql-search":          TempoSearchTool,
		"tempo_traceql-metrics-instant": TempoMetricsInstantTool,
		"tempo_traceql-metrics-range":   TempoMetricsRangeTool,
		"tempo_get-trace":               TempoGetTraceTool,
		"tempo_trace-diff":              TempoTraceDiffTool,
		"tempo_get-attribute-names":     TempoGetAttributeNamesTool,
		"tempo_get-attribute-values":    TempoGetAttributeValuesTool,
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
	result, err := call(makeTempoRequest("tempo_traceql-search", map[string]any{
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
	assert.Equal(t, "application/llm", capturedAccept)
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
	result, err := call(makeTempoRequest("tempo_traceql-metrics-instant", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ } | count_over_time()`,
		"start":         "2025-01-01T00:00:00Z",
		"end":           "2025-01-01T01:00:00Z",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/metrics/query", capturedPath)
	// Metrics queries use epoch nanoseconds
	assert.Equal(t, "1735689600000000000", capturedQuery.Get("start"))
	assert.Equal(t, "1735693200000000000", capturedQuery.Get("end"))
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
	result, err := call(makeTempoRequest("tempo_traceql-metrics-range", map[string]any{
		"datasourceUid": "test-tempo",
		"query":         `{ } | rate()`,
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/metrics/query_range", capturedPath)
}

func TestTempoGetTrace(t *testing.T) {
	var capturedPath string

	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"batches":[{"resource":{"attributes":[]},"scopeSpans":[]}]}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("tempo_get-trace", map[string]any{
		"datasourceUid": "test-tempo",
		"trace_id":      "abc123def456",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/v2/traces/abc123def456", capturedPath)
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
	result, err := call(makeTempoRequest("tempo_trace-diff", map[string]any{
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
	_, err := call(makeTempoRequest("tempo_trace-diff", map[string]any{
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
	result, err := call(makeTempoRequest("tempo_trace-diff", map[string]any{
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
	result, err := call(makeTempoRequest("tempo_get-attribute-names", map[string]any{
		"datasourceUid": "test-tempo",
		"scope":         "span",
	}))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "/api/datasources/proxy/uid/test-tempo/api/v2/search/tags", capturedPath)
	assert.Equal(t, "span", capturedQuery.Get("scope"))
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
	result, err := call(makeTempoRequest("tempo_get-attribute-values", map[string]any{
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

func TestTempoSearch_Non200Response(t *testing.T) {
	ts, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid query syntax"}`))
	})
	defer cleanup()

	call := tempoTestContext(t, ts.URL)
	result, err := call(makeTempoRequest("tempo_traceql-search", map[string]any{
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
	result, err := call(makeTempoRequest("tempo_traceql-search", map[string]any{
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

func TestAddTempoTools_RegistersAllTools(t *testing.T) {
	s := server.NewMCPServer("test", "0.1.0")
	AddTempoTools(s, true)

	expectedTools := []string{
		"tempo_traceql-search",
		"tempo_traceql-metrics-instant",
		"tempo_traceql-metrics-range",
		"tempo_get-trace",
		"tempo_trace-diff",
		"tempo_get-attribute-names",
		"tempo_get-attribute-values",
	}

	tools := s.ListTools()
	for _, name := range expectedTools {
		_, ok := tools[name]
		assert.True(t, ok, "expected tool %q to be registered", name)
	}
}

func TestAddTempoTools_DisableQueryRegistersNothing(t *testing.T) {
	s := server.NewMCPServer("test", "0.1.0")
	AddTempoTools(s, false)

	tools := s.ListTools()
	for name := range tools {
		assert.False(t, strings.HasPrefix(name, "tempo_"), "no tempo tools should be registered when query disabled, found %q", name)
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
