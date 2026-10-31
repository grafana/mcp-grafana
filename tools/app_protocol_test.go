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
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func appSession(t *testing.T, ctx context.Context, sources ...mcpgrafana.Tool) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	t.Cleanup(cancel)
	s := mcp.NewServer(&mcp.Implementation{Name: "app-test", Version: "1"}, nil)
	if len(sources) == 0 {
		sources = []mcpgrafana.Tool{QueryPrometheus, GetTempoTraceTool}
	}
	for _, source := range sources {
		source.Register(s)
	}
	AddAppTool(s, sources...)
	mcpgrafana.RegisterGrafanaAppResource(s)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = s.Run(ctx, serverTransport) }()
	c, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestAppProtocolDiscovery(t *testing.T) {
	c := appSession(t, t.Context())
	listed, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	byName := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		byName[tool.Name] = tool
	}
	query := byName["query_prometheus"]
	chart := byName["render_grafana_app"]
	require.NotNil(t, query)
	require.NotNil(t, chart)
	assert.NotContains(t, query.Meta, "ui")
	assert.NotContains(t, byName["get_tempo_trace"].Meta, "ui")
	var schema struct {
		Properties map[string]struct {
			AnyOf []json.RawMessage `json:"anyOf"`
		} `json:"properties"`
	}
	body, err := json.Marshal(chart.InputSchema)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &schema))
	for i, name := range []string{"query_prometheus", "get_tempo_trace"} {
		original, err := json.Marshal(byName[name].InputSchema)
		require.NoError(t, err)
		assert.JSONEq(t, string(original), string(schema.Properties["arguments"].AnyOf[i]))
	}
	ui, ok := chart.Meta["ui"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, mcpgrafana.GrafanaAppResourceURI, ui["resourceUri"])
	resource, err := c.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: mcpgrafana.GrafanaAppResourceURI})
	require.NoError(t, err)
	require.Len(t, resource.Contents, 1)
	assert.Equal(t, "text/html;profile=mcp-app", resource.Contents[0].MIMEType)
	assert.Contains(t, resource.Contents[0].Text, "<!doctype html>")
}

func TestPrometheusQueryAndChartProtocolResults(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/metrics-app":
			_, _ = io.WriteString(w, `{"uid":"metrics-app","type":"prometheus"}`)
		case "/api/ds/query":
			assert.Equal(t, http.MethodPost, r.Method)
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			query := dsQueryOf(t, payload)
			assert.Equal(t, "up", query["expr"])
			assert.Equal(t, map[string]any{"uid": "metrics-app", "type": "prometheus"}, query["datasource"])
			assert.Equal(t, "Bearer test", r.Header.Get("Authorization"))
			assert.Equal(t, "42", r.Header.Get("X-Grafana-Org-Id"))
			require.NoError(t, json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{
				"A": backend.DataResponse{Frames: data.Frames{data.NewFrame("up",
					data.NewField("Time", nil, []time.Time{time.Unix(1700000000, 0)}),
					data.NewField("Value", nil, []float64{1}),
				)}},
			}}))
		case "/api/datasources/uid/denied":
			http.Error(w, "permission denied", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	ctx := mcpgrafana.WithGrafanaConfig(mockDatasourcesCtx(backend), mcpgrafana.GrafanaConfig{
		URL: backend.URL, APIKey: "test", OrgID: 42,
	})
	c := appSession(t, ctx)
	for _, tc := range []struct {
		name      string
		queryType string
		uid       string
		step      int
		wantError bool
	}{
		{name: "range", queryType: "range", uid: "metrics-app", step: 15},
		{name: "instant", queryType: "instant", uid: "metrics-app"},
		{name: "invalid range", queryType: "range", uid: "metrics-app", wantError: true},
		{name: "denied datasource", queryType: "instant", uid: "denied", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := QueryPrometheusParams{
				DatasourceUID: tc.uid,
				Expr:          "up",
				QueryType:     tc.queryType,
				StartTime:     "2023-11-14T22:13:20Z",
				EndTime:       "2023-11-14T22:14:20Z",
				StepSeconds:   tc.step,
			}
			query, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "query_prometheus", Arguments: args})
			require.NoError(t, err)
			chart, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "render_grafana_app", Arguments: map[string]any{"toolName": "query_prometheus", "arguments": args}})
			require.NoError(t, err)
			assert.Equal(t, tc.wantError, query.IsError)
			assert.Equal(t, query, chart)
			if !tc.wantError {
				assert.NotNil(t, chart.StructuredContent)
			}
		})
	}
}

func TestTraceQueryAndAppProtocolResults(t *testing.T) {
	requests := 0
	wantFrom, wantTo := "0", "0"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/tempo-main":
			writeTempoDatasource(t, w)
		case "/api/user":
			_, _ = io.WriteString(w, `{"orgId":42}`)
		case "/api/ds/query":
			requests++
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "Bearer trace-token", r.Header.Get("Authorization"))
			assert.Equal(t, "42", r.Header.Get("X-Grafana-Org-Id"))
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			query := dsQueryOf(t, payload)
			assert.Equal(t, "traceId", query["queryType"])
			assert.Equal(t, testTraceID, query["query"])
			assert.Equal(t, map[string]any{"uid": "tempo-main", "type": "tempo"}, query["datasource"])
			assert.Equal(t, wantFrom, payload["from"])
			assert.Equal(t, wantTo, payload["to"])
			writeTraceFrameResponse(t, w, testTraceData("stack"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	ctx := traceTestContext(t, mcpgrafana.GrafanaConfig{URL: backend.URL, APIKey: "trace-token", OrgID: 42})
	client := mcpgrafana.GrafanaClientFromContext(ctx)
	client.PublicURL, client.Version = backend.URL, "11.0.0"
	c := appSession(t, ctx)
	for _, tc := range []struct {
		name      string
		start     string
		end       string
		from      string
		to        string
		wantError bool
	}{
		{name: "all time", from: "0", to: "0"},
		{
			name:  "bounded",
			start: time.Unix(1750000000, 0).UTC().Format(time.RFC3339), end: time.Unix(1750000060, 0).UTC().Format(time.RFC3339),
			from: "1750000000000", to: "1750000060000",
		},
		{name: "missing end", start: time.Unix(1750000000, 0).UTC().Format(time.RFC3339), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantFrom, wantTo = tc.from, tc.to
			args := GetTempoTraceParams{
				TraceID: testTraceID, DatasourceUID: "tempo-main", FocusSpanID: stringPointer(testSpanID),
				Start: tc.start, End: tc.end,
			}
			query, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_tempo_trace", Arguments: args})
			require.NoError(t, err)
			view, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "render_grafana_app", Arguments: map[string]any{
				"toolName": "get_tempo_trace", "arguments": args,
			}})
			require.NoError(t, err)
			assert.Equal(t, tc.wantError, view.IsError)
			assert.Equal(t, query, view)
			if !tc.wantError {
				assert.NotNil(t, view.StructuredContent)
			}
		})
	}
	assert.Equal(t, 4, requests)
}

func TestAppRejectsUnavailableSourcesAndInvalidArguments(t *testing.T) {
	c := appSession(t, t.Context(), QueryPrometheus)
	for _, tc := range []struct {
		name   string
		source string
		args   map[string]any
	}{
		{name: "disabled source", source: "get_tempo_trace", args: map[string]any{"trace_id": testTraceID, "datasourceUid": "tempo-main"}},
		{name: "arbitrary tool", source: "delete_dashboard", args: map[string]any{"uid": "prod"}},
		{name: "recursive call", source: "render_grafana_app", args: map[string]any{}},
		{name: "unknown input", source: "query_prometheus", args: map[string]any{"unexpected": true}},
		{name: "nested organization", source: "query_prometheus", args: map[string]any{"orgId": 42}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "render_grafana_app", Arguments: map[string]any{
				"toolName": tc.source, "arguments": tc.args,
			}})
			require.NoError(t, err)
			require.True(t, result.IsError)
		})
	}
}
