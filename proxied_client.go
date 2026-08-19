package mcpgrafana

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// mcpClientInitTimeout is the timeout for initializing a proxied MCP client
	// (connecting, handshaking, and listing tools). Kept short to avoid blocking
	// server startup when a datasource's MCP endpoint is slow or unreachable.
	mcpClientInitTimeout = 30 * time.Second
)

// ProxiedClient represents a connection to a remote MCP server (e.g., Tempo datasource)
type ProxiedClient struct {
	DatasourceUID  string
	DatasourceName string
	DatasourceType string
	Session        *mcp.ClientSession
	Tools          []*mcp.Tool
	mutex          sync.RWMutex

	// closeHook, when set, runs inside Close (under the client mutex). It is a
	// test seam so lifecycle tests can observe exactly how many times a client is
	// Closed without standing up a real remote MCP transport. Always nil in
	// production; do not use it for behavior.
	closeHook func()
}

// contextCauseOrErr returns the context cause if the error is due to context
// cancellation, otherwise returns the original error.
func contextCauseOrErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
	}
	return err
}

// NewProxiedClient creates a new connection to a remote MCP server
func NewProxiedClient(ctx context.Context, datasourceUID, datasourceName, datasourceType, mcpEndpoint string) (*ProxiedClient, error) {
	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()

	initCtx, cancel := context.WithTimeoutCause(ctx, mcpClientInitTimeout,
		fmt.Errorf("timed out after %s connecting to MCP server for datasource %s (%s) at %s", mcpClientInitTimeout, datasourceName, datasourceUID, mcpEndpoint))
	defer cancel()

	rt, err := BuildTransport(&config, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build transport: %w", err)
	}

	logger.DebugContext(initCtx, "connecting to MCP server", "datasource", datasourceUID, "url", mcpEndpoint)
	clientTransport := &mcp.StreamableClientTransport{
		Endpoint:   mcpEndpoint,
		HTTPClient: &http.Client{Transport: rt},
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "mcp-grafana-proxy",
		Version: Version(),
	}, nil)

	// Connect performs the initialize handshake.
	session, err := client.Connect(initCtx, clientTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MCP client: %w", contextCauseOrErr(initCtx, err))
	}

	// List available tools from the remote server
	toolsResult, err := session.ListTools(initCtx, nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("failed to list tools from remote MCP server: %w", contextCauseOrErr(initCtx, err))
	}

	logger.DebugContext(initCtx, "connected to proxied MCP server",
		"datasource", datasourceUID,
		"type", datasourceType,
		"tools", len(toolsResult.Tools))

	return &ProxiedClient{
		DatasourceUID:  datasourceUID,
		DatasourceName: datasourceName,
		DatasourceType: datasourceType,
		Session:        session,
		Tools:          toolsResult.Tools,
	}, nil
}

// CallTool forwards a tool call to the remote MCP server
func (pc *ProxiedClient) CallTool(ctx context.Context, toolName string, arguments map[string]any) (*mcp.CallToolResult, error) {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	// Validate the tool exists
	var toolExists bool
	for _, tool := range pc.Tools {
		if tool.Name == toolName {
			toolExists = true
			break
		}
	}
	if !toolExists {
		return nil, fmt.Errorf("tool %s not found in remote MCP server", toolName)
	}

	// Forward the call to the remote server
	result, err := pc.Session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call tool on remote MCP server: %w", err)
	}

	return result, nil
}

// ListTools returns the tools available from this remote server
// Note: This method doesn't take a context parameter as the tools are cached locally
func (pc *ProxiedClient) ListTools() []*mcp.Tool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	// Return a copy to prevent external modification
	result := make([]*mcp.Tool, len(pc.Tools))
	copy(result, pc.Tools)
	return result
}

// Close closes the connection to the remote MCP server
func (pc *ProxiedClient) Close() error {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	if pc.closeHook != nil {
		pc.closeHook()
	}

	if pc.Session != nil {
		if err := pc.Session.Close(); err != nil {
			return fmt.Errorf("failed to close MCP client: %w", err)
		}
	}

	return nil
}
