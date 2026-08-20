package mcpgrafana

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// resolveProxiedClient locates the ProxiedClient for one datasource, for any
// kind of proxied request (tool call, resource read, prompt get).
//
// release, when non-nil, MUST be called (deferred) once the forwarded request
// completes. On the session (HTTP/SSE) path it holds an in-flight reference on
// the shared proxied tool set, so a concurrent teardown cannot Close the client
// mid-request. The stdio path is single-tenant and process-level, so its clients
// outlive every request and release is nil there.
//
// The returned error is already phrased for the MCP caller: the underlying
// lookup error distinguishes "no such datasource" from "not permitted", which
// the caller cannot act on differently and which would leak which datasource
// UIDs exist.
func resolveProxiedClient(ctx context.Context, sm *SessionManager, tm *ToolManager, datasourceType, datasourceUID string) (*ProxiedClient, func(), error) {
	var (
		client  *ProxiedClient
		release func()
		err     error
	)

	if tm.serverMode {
		// Server mode (stdio): clients stored at manager level.
		client, err = tm.GetServerClient(datasourceType, datasourceUID)
	} else {
		// Session mode (HTTP/SSE): clients live in the session's shared set.
		client, release, err = sm.GetProxiedClient(ctx, datasourceType, datasourceUID)
		if err != nil {
			// Fallback to server-level in case of mixed mode.
			client, err = tm.GetServerClient(datasourceType, datasourceUID)
		}
	}

	if err != nil {
		return nil, nil, fmt.Errorf("datasource '%s' not found or not accessible. Ensure the datasource exists and you have permission to access it", datasourceUID)
	}
	return client, release, nil
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

	client, release, err := resolveProxiedClient(ctx, h.sessionManager, h.toolManager, datasourceType, datasourceUID)
	if err != nil {
		return nil, err
	}
	if release != nil {
		defer release()
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
// from upstream MCP servers. The URI it receives is the namespaced URN this
// server advertised (see namespaceResourceURI), whether the client took it from
// a static resource listing or produced it by expanding a proxied resource
// template; either way it is parsed back into a datasource and an upstream URI
// before forwarding.
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

	// Record where this read was routed on the active span. The duration and
	// error of the resources/read itself are already captured generically by the
	// observability hooks; what those cannot know is which datasource instance
	// and upstream URI the namespaced URN resolved to.
	annotateProxiedSpan(ctx,
		attribute.String(attrDatasourceType, datasourceType),
		attribute.String(attrDatasourceUID, datasourceUID),
		attribute.String(attrProxiedUpstreamURI, originalURI),
	)

	// A session is required only on the non-stdio transports, where the client
	// lives in the session's shared proxied tool set.
	if !h.toolManager.serverMode {
		if session := server.ClientSessionFromContext(ctx); session == nil {
			return nil, fmt.Errorf("session not found in context")
		}
	}

	client, release, err := resolveProxiedClient(ctx, h.sessionManager, h.toolManager, datasourceType, datasourceUID)
	if err != nil {
		return nil, err
	}
	if release != nil {
		defer release()
	}

	result, err := client.ReadResource(ctx, originalURI)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("remote MCP server returned nil read-resource result")
	}
	return result.Contents, nil
}

// ProxiedPromptHandler implements PromptHandlerFunc for prompts proxied from
// upstream MCP servers. The prompt name is namespaced (see namespacePromptName)
// with the datasource type, and the datasource instance is selected by the
// datasourceUid argument injected when the prompt was registered — the same
// split used for proxied tools, because a UID may itself contain underscores and
// so cannot be positionally encoded in the name.
//
// Only the stdio transport reaches this handler today: prompts are not
// registered per session (see InitializeAndRegisterProxiedCapabilities).
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
func (h *ProxiedPromptHandler) Handle(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	datasourceType, originalName, err := parseProxiedPromptName(request.Params.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxied prompt name: %w", err)
	}

	datasourceUID, ok := request.Params.Arguments["datasourceUid"]
	if !ok || datasourceUID == "" {
		return nil, fmt.Errorf("datasourceUid argument is required")
	}

	annotateProxiedSpan(ctx,
		attribute.String(attrDatasourceType, datasourceType),
		attribute.String(attrDatasourceUID, datasourceUID),
		attribute.String(attrProxiedUpstreamPrompt, originalName),
	)

	if !h.toolManager.serverMode {
		if session := server.ClientSessionFromContext(ctx); session == nil {
			return nil, fmt.Errorf("session not found in context")
		}
	}

	client, release, err := resolveProxiedClient(ctx, h.sessionManager, h.toolManager, datasourceType, datasourceUID)
	if err != nil {
		return nil, err
	}
	if release != nil {
		defer release()
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

// annotateProxiedSpan adds attributes to the active span, if one is recording.
// With stdio, or with tracing disabled, there is no recording span and this is a
// no-op, so callers need no guard of their own.
func annotateProxiedSpan(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(attrs...)
}
