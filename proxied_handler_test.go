package mcpgrafana

import (
	"context"
	"testing"

	mcp_client "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeUpstreamClient builds an in-process MCP server exposing one tool, one
// resource, and one prompt, then connects a ProxiedClient to it. The returned
// client is suitable for testing handler dispatch logic without HTTP.
func newFakeUpstreamClient(t *testing.T, datasourceUID, datasourceName, datasourceType string) *ProxiedClient {
	t.Helper()

	srv := server.NewMCPServer("fake-upstream", "0.0.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
		server.WithPromptCapabilities(false),
	)

	srv.AddTool(mcp.Tool{
		Name:        "echo",
		Description: "Echoes the input",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{"msg": map[string]any{"type": "string"}},
			Required:   []string{"msg"},
		},
	}, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		msg, _ := args["msg"].(string)
		return mcp.NewToolResultText("echo: " + msg), nil
	})

	srv.AddResource(mcp.Resource{
		URI:      "docs://traceql/basic",
		Name:     "TraceQL Basics",
		MIMEType: "text/markdown",
	}, func(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "docs://traceql/basic",
				MIMEType: "text/markdown",
				Text:     "# TraceQL\nbasic docs",
			},
		}, nil
	})

	srv.AddPrompt(mcp.Prompt{
		Name:        "trace-summary",
		Description: "Summarize a trace",
		Arguments: []mcp.PromptArgument{
			{Name: "trace_id", Required: true},
		},
	}, func(_ context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		traceID := req.Params.Arguments["trace_id"]
		return &mcp.GetPromptResult{
			Description: "Summary",
			Messages: []mcp.PromptMessage{
				{
					Role:    mcp.RoleUser,
					Content: mcp.TextContent{Type: "text", Text: "summarize trace " + traceID},
				},
			},
		}, nil
	})

	mcpClient, err := mcp_client.NewInProcessClient(srv)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, mcpClient.Start(ctx))

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test-proxy", Version: "0.0.0"}
	_, err = mcpClient.Initialize(ctx, initReq)
	require.NoError(t, err)

	tools, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	require.NoError(t, err)
	resources, err := mcpClient.ListResources(ctx, mcp.ListResourcesRequest{})
	require.NoError(t, err)
	prompts, err := mcpClient.ListPrompts(ctx, mcp.ListPromptsRequest{})
	require.NoError(t, err)

	pc := &ProxiedClient{
		DatasourceUID:  datasourceUID,
		DatasourceName: datasourceName,
		DatasourceType: datasourceType,
		Client:         mcpClient,
		Tools:          tools.Tools,
		Resources:      resources.Resources,
		Prompts:        prompts.Prompts,
	}
	t.Cleanup(func() { _ = pc.Close() })
	return pc
}

func TestProxiedResourceHandler_ForwardsToUpstream(t *testing.T) {
	pc := newFakeUpstreamClient(t, "abc-123", "Prod Tempo", "tempo")

	sm := NewSessionManager()
	t.Cleanup(sm.Close)
	mcpServer := server.NewMCPServer("test-host", "0.0.0")
	tm := NewToolManager(sm, mcpServer, WithProxiedTools(true))
	tm.serverMode = true
	tm.serverClients["tempo_abc-123"] = pc

	handler := NewProxiedResourceHandler(sm, tm)
	req := mcp.ReadResourceRequest{}
	req.Params.URI = namespaceResourceURI("tempo", "abc-123", "docs://traceql/basic")

	contents, err := handler.Handle(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, contents, 1)
	text, ok := contents[0].(mcp.TextResourceContents)
	require.True(t, ok)
	assert.Equal(t, "docs://traceql/basic", text.URI)
	assert.Contains(t, text.Text, "TraceQL")
}

func TestProxiedResourceHandler_BadURIIsRejected(t *testing.T) {
	sm := NewSessionManager()
	t.Cleanup(sm.Close)
	mcpServer := server.NewMCPServer("test-host", "0.0.0")
	tm := NewToolManager(sm, mcpServer, WithProxiedTools(true))
	tm.serverMode = true

	handler := NewProxiedResourceHandler(sm, tm)
	req := mcp.ReadResourceRequest{}
	req.Params.URI = "https://not-namespaced/foo"

	_, err := handler.Handle(context.Background(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a proxied resource URI")
}

func TestProxiedPromptHandler_ForwardsToUpstream(t *testing.T) {
	pc := newFakeUpstreamClient(t, "abc-123", "Prod Tempo", "tempo")

	sm := NewSessionManager()
	t.Cleanup(sm.Close)
	mcpServer := server.NewMCPServer("test-host", "0.0.0")
	tm := NewToolManager(sm, mcpServer, WithProxiedTools(true))
	tm.serverMode = true
	tm.serverClients["tempo_abc-123"] = pc

	handler := NewProxiedPromptHandler(sm, tm)
	req := mcp.GetPromptRequest{}
	req.Params.Name = namespacePromptName("tempo", "trace-summary")
	req.Params.Arguments = map[string]string{
		"datasourceUid": "abc-123",
		"trace_id":      "abc",
	}

	res, err := handler.Handle(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, res.Messages, 1)
	tc, ok := res.Messages[0].Content.(mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "summarize trace abc", tc.Text)
}

func TestProxiedPromptHandler_MissingDatasourceUid(t *testing.T) {
	sm := NewSessionManager()
	t.Cleanup(sm.Close)
	mcpServer := server.NewMCPServer("test-host", "0.0.0")
	tm := NewToolManager(sm, mcpServer, WithProxiedTools(true))
	tm.serverMode = true

	handler := NewProxiedPromptHandler(sm, tm)
	req := mcp.GetPromptRequest{}
	req.Params.Name = namespacePromptName("tempo", "trace-summary")
	req.Params.Arguments = map[string]string{"trace_id": "abc"}

	_, err := handler.Handle(context.Background(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "datasourceUid")
}

func TestProxiedClient_FetchesAllCapabilities(t *testing.T) {
	pc := newFakeUpstreamClient(t, "abc-123", "Prod Tempo", "tempo")

	assert.Len(t, pc.ListTools(), 1)
	assert.Equal(t, "echo", pc.ListTools()[0].Name)

	assert.Len(t, pc.ListResources(), 1)
	assert.Equal(t, "docs://traceql/basic", pc.ListResources()[0].URI)

	assert.Len(t, pc.ListPrompts(), 1)
	assert.Equal(t, "trace-summary", pc.ListPrompts()[0].Name)
}
