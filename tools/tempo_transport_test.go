//go:build unit

package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	s := mcp.NewServer(&mcp.Implementation{Name: "trace-transport-test", Version: "1"}, nil)
	AddTempoTools(s, true)
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx = mcpgrafana.WithGrafanaClient(mcpgrafana.WithGrafanaConfig(ctx, mcpgrafana.GrafanaConfigFromContext(base)), mcpgrafana.GrafanaClientFromContext(base))
			return next(ctx, method, req)
		}
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	c := mcp.NewClient(&mcp.Implementation{Name: "trace-app-host", Version: "1"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{Extensions: map[string]any{"io.modelcontextprotocol/ui": map[string]any{"mimeTypes": []any{"text/html;profile=mcp-app"}}}},
	})
	session, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_tempo_trace", Arguments: map[string]any{"datasourceUid": "test-tempo", "trace_id": testTraceID}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, body, result.Content[0].(*mcp.TextContent).Text)
	assert.Equal(t, "trace", result.Meta["type"])
	assert.Equal(t, "json", result.Meta["encoding"])
	assert.Equal(t, 1, requests)
	require.NotNil(t, result.StructuredContent, "stateless calls must retain interactive enrichment after initialize")
	spans := result.StructuredContent.(map[string]any)["spans"].([]any)
	assert.Len(t, spans, 2)
}
