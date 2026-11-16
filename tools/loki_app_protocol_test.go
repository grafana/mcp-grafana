//go:build unit

package tools

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestLokiLogsAppProtocol(t *testing.T) {
	for _, format := range []string{"full", "compact"} {
		t.Run(format, func(t *testing.T) {
			server, _ := lokiQueryStub([2]string{"1700000000000000000", "a failed request"})
			t.Cleanup(server.Close)
			session := appSession(t, mockDatasourcesCtx(server), QueryLokiLogs)
			args := QueryLokiLogsParams{DatasourceUID: "loki", LogQL: `{app="a"}`, StartRFC3339: "2023-11-14T22:13:20Z", EndRFC3339: "2023-11-14T22:14:20Z", Format: format}
			query, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "query_loki_logs", Arguments: args})
			require.NoError(t, err)
			require.False(t, query.IsError)
			expected, err := queryLokiLogs(mockDatasourcesCtx(server), args)
			require.NoError(t, err)
			sourceJSON, err := json.Marshal(expected)
			require.NoError(t, err)
			require.JSONEq(t, string(sourceJSON), query.Content[0].(*mcp.TextContent).Text)
			chart, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "render_grafana_app", Arguments: map[string]any{"toolName": "query_loki_logs", "arguments": args}})
			require.NoError(t, err)
			assertAppDisplayResult(t, "query_loki_logs", query, chart)
			view := chart.StructuredContent.(map[string]any)
			require.Equal(t, "logs", view["kind"])
			require.Len(t, view["logs"].(map[string]any)["data"], 1)
		})
	}
}
