package mcpgrafana

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// resolveProxiedClient locates a cached ProxiedClient for the given datasource,
// preferring per-session storage (HTTP/SSE) and falling back to manager-level
// storage (stdio). It returns a user-facing error suitable for surfacing back
// to the MCP caller.
func resolveProxiedClient(ctx context.Context, sm *SessionManager, tm *ToolManager, datasourceType, datasourceUID string) (*ProxiedClient, error) {
	if tm.serverMode {
		client, err := tm.GetServerClient(datasourceType, datasourceUID)
		if err != nil {
			return nil, fmt.Errorf("datasource '%s' not found or not accessible. Ensure the datasource exists and you have permission to access it", datasourceUID)
		}
		return client, nil
	}

	if client, err := sm.GetProxiedClient(ctx, datasourceType, datasourceUID); err == nil {
		return client, nil
	}
	// Mixed-mode fallback: try server-level storage in case stdio-style
	// registration was used while an HTTP session is active.
	client, err := tm.GetServerClient(datasourceType, datasourceUID)
	if err != nil {
		return nil, fmt.Errorf("datasource '%s' not found or not accessible. Ensure the datasource exists and you have permission to access it", datasourceUID)
	}
	return client, nil
}

// ProxiedToolHandler implements the CallToolHandler interface for proxied tools
type ProxiedToolHandler struct {
	sessionManager *SessionManager
	toolManager    *ToolManager
	toolName       string
}

// NewProxiedToolHandler creates a new handler for a proxied tool
func NewProxiedToolHandler(sm *SessionManager, tm *ToolManager, toolName string) *ProxiedToolHandler {
	return &ProxiedToolHandler{
		sessionManager: sm,
		toolManager:    tm,
		toolName:       toolName,
	}
}

// Handle forwards the tool call to the appropriate remote MCP server
func (h *ProxiedToolHandler) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Check if session is in context
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		return nil, fmt.Errorf("session not found in context")
	}

	// Extract arguments
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid arguments type")
	}

	// Extract required datasourceUid parameter
	datasourceUidRaw, ok := args["datasourceUid"]
	if !ok {
		return nil, fmt.Errorf("datasourceUid parameter is required")
	}
	datasourceUID, ok := datasourceUidRaw.(string)
	if !ok {
		return nil, fmt.Errorf("datasourceUid must be a string")
	}

	// Parse the tool name to get datasource type and original tool name
	// Format: datasourceType_originalToolName (e.g., "tempo_traceql-search")
	datasourceType, originalToolName, err := parseProxiedToolName(h.toolName)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tool name: %w", err)
	}

	client, err := resolveProxiedClient(ctx, h.sessionManager, h.toolManager, datasourceType, datasourceUID)
	if err != nil {
		return nil, err
	}

	// Remove datasourceUid from args before forwarding to remote server
	forwardArgs := make(map[string]any)
	for k, v := range args {
		if k != "datasourceUid" {
			forwardArgs[k] = v
		}
	}

	// Forward the call to the remote MCP server
	return client.CallTool(ctx, originalToolName, forwardArgs)
}

// ProxiedResourceHandler implements ResourceHandlerFunc for resources proxied
// from upstream MCP servers. The URI received in the request is the namespaced
// URN (see namespaceResourceURI); we parse it to recover the datasource and
// upstream URI before forwarding.
type ProxiedResourceHandler struct {
	sessionManager *SessionManager
	toolManager    *ToolManager
}

// NewProxiedResourceHandler creates a new handler for proxied resources.
func NewProxiedResourceHandler(sm *SessionManager, tm *ToolManager) *ProxiedResourceHandler {
	return &ProxiedResourceHandler{
		sessionManager: sm,
		toolManager:    tm,
	}
}

// Handle forwards a resources/read request to the appropriate upstream MCP server.
func (h *ProxiedResourceHandler) Handle(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	datasourceType, datasourceUID, originalURI, err := parseNamespacedResourceURI(request.Params.URI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxied resource URI: %w", err)
	}

	// Session is only required when running in non-stdio mode; resolveProxiedClient
	// handles both transports.
	if !h.toolManager.serverMode {
		if session := server.ClientSessionFromContext(ctx); session == nil {
			return nil, fmt.Errorf("session not found in context")
		}
	}

	client, err := resolveProxiedClient(ctx, h.sessionManager, h.toolManager, datasourceType, datasourceUID)
	if err != nil {
		return nil, err
	}

	result, err := client.ReadResource(ctx, originalURI)
	if err != nil {
		return nil, err
	}
	return result.Contents, nil
}

// ProxiedPromptHandler implements PromptHandlerFunc for prompts proxied from
// upstream MCP servers. The prompt name is namespaced (see namespacePromptName)
// to embed the datasource type and UID, removing the need for a separate
// datasourceUid argument.
type ProxiedPromptHandler struct {
	sessionManager *SessionManager
	toolManager    *ToolManager
}

// NewProxiedPromptHandler creates a new handler for proxied prompts.
func NewProxiedPromptHandler(sm *SessionManager, tm *ToolManager) *ProxiedPromptHandler {
	return &ProxiedPromptHandler{
		sessionManager: sm,
		toolManager:    tm,
	}
}

// Handle forwards a prompts/get request to the appropriate upstream MCP server.
// The datasourceUid argument (injected when the prompt was registered) selects
// which upstream client to forward to.
func (h *ProxiedPromptHandler) Handle(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	datasourceType, originalName, err := parseProxiedPromptName(request.Params.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxied prompt name: %w", err)
	}

	datasourceUID, ok := request.Params.Arguments["datasourceUid"]
	if !ok || datasourceUID == "" {
		return nil, fmt.Errorf("datasourceUid argument is required")
	}

	if !h.toolManager.serverMode {
		if session := server.ClientSessionFromContext(ctx); session == nil {
			return nil, fmt.Errorf("session not found in context")
		}
	}

	client, err := resolveProxiedClient(ctx, h.sessionManager, h.toolManager, datasourceType, datasourceUID)
	if err != nil {
		return nil, err
	}

	// Strip the synthetic datasourceUid arg before forwarding upstream.
	forwardArgs := make(map[string]string, len(request.Params.Arguments))
	for k, v := range request.Params.Arguments {
		if k != "datasourceUid" {
			forwardArgs[k] = v
		}
	}

	return client.GetPrompt(ctx, originalName, forwardArgs)
}
