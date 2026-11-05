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
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusAppRejectsOversizedResult(t *testing.T) {
	payload := strings.Repeat("x", maxMetricsViewBytes)
	frame := data.NewFrame("up",
		data.NewField("Time", nil, []time.Time{time.Unix(1700000000, 0)}),
		data.NewField("Value", data.Labels{"payload": payload}, []float64{1}),
	)
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/metrics-app":
			_, _ = w.Write([]byte(`{"uid":"metrics-app","type":"prometheus"}`))
		case "/api/ds/query":
			require.NoError(t, json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{
				"A": backend.DataResponse{Frames: data.Frames{frame}},
			}}))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(grafana.Close)
	ctx := mcpgrafana.WithGrafanaConfig(mockDatasourcesCtx(grafana), mcpgrafana.GrafanaConfig{URL: grafana.URL})
	c := appSession(t, ctx, QueryPrometheus)
	query := assertAppRejectsMissingView(t, c, "query_prometheus", QueryPrometheusParams{
		DatasourceUID: "metrics-app", Expr: "up", QueryType: "instant", EndTime: "2023-11-14T22:13:20Z",
	})
	text := query.Content[0].(*mcp.TextContent).Text
	assert.Greater(t, len(text), maxMetricsViewBytes)
	assert.Contains(t, text, payload)
}

func TestTraceAppRejectsUnavailableView(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		partial bool
	}{
		{name: "oversized", payload: strings.Repeat("x", maxTraceResultBytes)},
		{name: "partial", payload: "stack", partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := testTraceFrame(testTraceData(tc.payload))
			if tc.partial {
				frame.Meta.Custom = map[string]any{"partial": true, "message": "trace exceeded max bytes"}
			}
			grafana, cleanup := tempoTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				require.NoError(t, json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{
					"A": backend.DataResponse{Frames: data.Frames{frame}},
				}}))
			})
			t.Cleanup(cleanup)
			ctx := traceTestContext(t, mcpgrafana.GrafanaConfig{URL: grafana.URL})
			c := appSession(t, ctx, GetTempoTraceTool)
			query := assertAppRejectsMissingView(t, c, "get_tempo_trace", GetTempoTraceParams{
				DatasourceUID: "test-tempo", TraceID: testTraceID,
			})
			text := query.Content[0].(*mcp.TextContent).Text
			assert.Contains(t, text, tc.payload)
			if !tc.partial {
				assert.Greater(t, len(text), maxTraceResultBytes)
			}
		})
	}
}

func assertAppRejectsMissingView(t *testing.T, c *mcp.ClientSession, source string, args any) *mcp.CallToolResult {
	t.Helper()
	query, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: source, Arguments: args})
	require.NoError(t, err)
	require.False(t, query.IsError)
	require.Nil(t, query.StructuredContent)
	require.Len(t, query.Content, 1)
	view, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "render_grafana_app", Arguments: map[string]any{
		"toolName": source, "arguments": args,
	}})
	require.NoError(t, err)
	require.True(t, view.IsError)
	assert.Nil(t, view.StructuredContent)
	require.Len(t, view.Content, 1)
	assert.Equal(t, fmt.Sprintf("result too large or unsupported for the interactive view; use %s directly", source), view.Content[0].(*mcp.TextContent).Text)
	return query
}

func TestAppPreservesSourceResult(t *testing.T) {
	result, err := newMetricsAppResult(&QueryPrometheusResult{Data: model.Vector{}})
	require.NoError(t, err)
	original := *result
	source := QueryPrometheus
	source.Handler = func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return result, nil
	}
	c := appSession(t, t.Context(), source)
	view, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "render_grafana_app", Arguments: map[string]any{
		"toolName": source.Tool.Name, "arguments": QueryPrometheusParams{DatasourceUID: "metrics-app", Expr: "up"},
	}})
	require.NoError(t, err)
	require.False(t, view.IsError)
	assert.Equal(t, original, *result)
	assert.NotEqual(t, original.Content[0].(*mcp.TextContent).Text, view.Content[0].(*mcp.TextContent).Text)
}

type notedContent struct {
	Notes []string `json:"-"`
}

func (c notedContent) AppNotes() []string { return c.Notes }

func TestAppAppendsSourceNotes(t *testing.T) {
	long := strings.Repeat("x", maxAppNoteRunes+10)
	notes := []string{"Query B failed:\n  bad_data", "", long, " ", "3", "4", "5", "6"}
	source := mcpgrafana.MustTool("noted_source", "Test source.",
		func(context.Context, struct{}) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: "{}"}},
				StructuredContent: notedContent{Notes: notes},
			}, nil
		})
	c := appSession(t, t.Context(), source)

	view, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "render_grafana_app", Arguments: map[string]any{
		"toolName": "noted_source", "arguments": map[string]any{},
	}})
	require.NoError(t, err)
	require.False(t, view.IsError)
	require.Len(t, view.Content, 1)
	want := []string{
		"Displayed noted_source result in the Grafana App.",
		"Query B failed: bad_data",
		strings.Repeat("x", maxAppNoteRunes-1) + "…",
		"3", "4", "5",
	}
	assert.Equal(t, strings.Join(want, "\n"), view.Content[0].(*mcp.TextContent).Text)
}
