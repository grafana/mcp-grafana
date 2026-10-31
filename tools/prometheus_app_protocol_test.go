//go:build unit

package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prometheusAppSession(t *testing.T, ctx context.Context) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	t.Cleanup(cancel)
	s := mcp.NewServer(&mcp.Implementation{Name: "metrics-test", Version: "1"}, nil)
	AddPrometheusTools(s, true)
	mcpgrafana.RegisterMetricsAppResource(s)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = s.Run(ctx, serverTransport) }()
	c, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestPrometheusAppProtocolDiscovery(t *testing.T) {
	c := prometheusAppSession(t, t.Context())
	listed, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	byName := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		byName[tool.Name] = tool
	}
	query := byName["query_prometheus"]
	chart := byName["render_prometheus_chart"]
	require.NotNil(t, query)
	require.NotNil(t, chart)
	assert.NotContains(t, query.Meta, "ui")
	assert.Equal(t, query.InputSchema, chart.InputSchema)
	ui, ok := chart.Meta["ui"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, mcpgrafana.MetricsViewerResourceURI, ui["resourceUri"])
	resource, err := c.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: mcpgrafana.MetricsViewerResourceURI})
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
		case "/api/datasources/uid/metrics-app/resources/api/v1/query_range":
			assert.Equal(t, "up", r.FormValue("query"))
			assert.Equal(t, "Bearer test", r.Header.Get("Authorization"))
			assert.Equal(t, "42", r.Header.Get("X-Grafana-Org-Id"))
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"up"},"values":[[1700000000,"1"]]}]}}`)
		case "/api/datasources/uid/metrics-app/resources/api/v1/query":
			assert.Equal(t, "up", r.FormValue("query"))
			assert.Equal(t, "Bearer test", r.Header.Get("Authorization"))
			assert.Equal(t, "42", r.Header.Get("X-Grafana-Org-Id"))
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up"},"value":[1700000000,"1"]}]}}`)
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
	c := prometheusAppSession(t, ctx)
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
			chart, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "render_prometheus_chart", Arguments: args})
			require.NoError(t, err)
			assert.Equal(t, tc.wantError, query.IsError)
			assert.Equal(t, query, chart)
			if !tc.wantError {
				assert.NotNil(t, chart.StructuredContent)
			}
		})
	}
}
