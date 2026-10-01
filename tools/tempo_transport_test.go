//go:build unit

package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestTempoGetTraceStatelessStreamableHTTP(t *testing.T) {
	traceJSON, err := protojson.Marshal(testTraceData("stack"))
	require.NoError(t, err)
	body := `{"trace":` + string(traceJSON) + `}`
	requests := 0
	grafana, cleanup := tempoTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		assert.Equal(t, tempoAcceptLLM, r.Header.Get("Accept"))
		_, _ = w.Write([]byte(body))
	})
	defer cleanup()
	base := traceTestContext(t, mcpgrafana.GrafanaConfig{URL: grafana.URL})
	s := server.NewMCPServer("trace-transport-test", "1")
	AddTempoTools(s, true)
	httpServer := server.NewStreamableHTTPServer(s,
		server.WithStateLess(true),
		server.WithHTTPContextFunc(func(ctx context.Context, _ *http.Request) context.Context {
			return mcpgrafana.WithGrafanaClient(mcpgrafana.WithGrafanaConfig(ctx, mcpgrafana.GrafanaConfigFromContext(base)), mcpgrafana.GrafanaClientFromContext(base))
		}),
	)
	ts := httptest.NewServer(httpServer)
	defer ts.Close()
	c, err := client.NewStreamableHttpClient(ts.URL)
	require.NoError(t, err)
	require.NoError(t, c.Start(t.Context()))
	defer func() { _ = c.Close() }()
	initialize := mcp.InitializeRequest{}
	initialize.Params.ProtocolVersion = "2025-06-18"
	initialize.Params.ClientInfo = mcp.Implementation{Name: "trace-app-host", Version: "1"}
	initialize.Params.Capabilities = *tempoTestUICapabilities()
	_, err = c.Initialize(t.Context(), initialize)
	require.NoError(t, err)
	result, err := c.CallTool(t.Context(), makeTempoRequest("get_tempo_trace", map[string]any{"datasourceUid": "test-tempo", "trace_id": testTraceID}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, body, result.Content[0].(mcp.TextContent).Text)
	assert.Equal(t, "trace", result.Meta.AdditionalFields["type"])
	assert.Equal(t, "json", result.Meta.AdditionalFields["encoding"])
	assert.Equal(t, 1, requests)
	require.NotNil(t, result.StructuredContent, "stateless calls must retain interactive enrichment after initialize")
	spans := result.StructuredContent.(map[string]any)["spans"].([]any)
	assert.Len(t, spans, 2)
}
