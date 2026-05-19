package mcpgrafana

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	mcp_client "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// ProxiedClient represents a connection to a remote MCP server (e.g., Tempo datasource).
// It caches the upstream server's tools, resources, resource templates, and prompts
// so they can be re-advertised by the parent server without re-querying the upstream.
type ProxiedClient struct {
	DatasourceUID     string
	DatasourceName    string
	DatasourceType    string
	Client            *mcp_client.Client
	Tools             []mcp.Tool
	Resources         []mcp.Resource
	ResourceTemplates []mcp.ResourceTemplate
	Prompts           []mcp.Prompt
	mutex             sync.RWMutex
}

// NewProxiedClient creates a new connection to a remote MCP server
func NewProxiedClient(ctx context.Context, datasourceUID, datasourceName, datasourceType, mcpEndpoint string) (*ProxiedClient, error) {
	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()

	rt, err := BuildTransport(&config, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build transport: %w", err)
	}

	logger.DebugContext(ctx, "connecting to MCP server", "datasource", datasourceUID, "url", mcpEndpoint)
	httpTransport, err := transport.NewStreamableHTTP(
		mcpEndpoint,
		transport.WithHTTPBasicClient(&http.Client{Transport: rt}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP transport: %w", err)
	}

	// Create MCP client
	mcpClient := mcp_client.NewClient(httpTransport)

	// Initialize the connection
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "mcp-grafana-proxy",
		Version: Version(),
	}

	_, err = mcpClient.Initialize(ctx, initReq)
	if err != nil {
		_ = mcpClient.Close()
		return nil, fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	// List available tools from the remote server. Tools are mandatory: if the
	// upstream advertises no tool capability we still try (some servers do not
	// declare capabilities precisely) and tolerate METHOD_NOT_FOUND gracefully.
	var tools []mcp.Tool
	toolsResult, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	switch {
	case err == nil:
		tools = toolsResult.Tools
	case errors.Is(err, mcp.ErrMethodNotFound):
		logger.DebugContext(ctx, "remote MCP server does not support tools/list",
			"datasource", datasourceUID)
	default:
		_ = mcpClient.Close()
		return nil, fmt.Errorf("failed to list tools from remote MCP server: %w", err)
	}

	caps := mcpClient.GetServerCapabilities()

	// Fetch resources and resource templates if advertised. Many servers (Tempo
	// included) register resources without explicitly declaring the capability,
	// so we also probe when caps.Resources is nil and ignore METHOD_NOT_FOUND.
	resources, resourceTemplates := listRemoteResources(ctx, mcpClient, caps, logger, datasourceUID)

	// Fetch prompts only if advertised. We don't probe blindly here because
	// stricter upstreams may treat unknown methods as errors and we have no
	// reason to expect a prompt-less server to suddenly grow prompts.
	var prompts []mcp.Prompt
	if caps.Prompts != nil {
		promptsResult, err := mcpClient.ListPrompts(ctx, mcp.ListPromptsRequest{})
		switch {
		case err == nil:
			prompts = promptsResult.Prompts
		case errors.Is(err, mcp.ErrMethodNotFound):
			logger.DebugContext(ctx, "remote MCP server advertised prompts but returned method not found",
				"datasource", datasourceUID)
		default:
			logger.WarnContext(ctx, "failed to list prompts from remote MCP server",
				"datasource", datasourceUID, "error", err)
		}
	}

	logger.DebugContext(ctx, "connected to proxied MCP server",
		"datasource", datasourceUID,
		"type", datasourceType,
		"tools", len(tools),
		"resources", len(resources),
		"resource_templates", len(resourceTemplates),
		"prompts", len(prompts))

	return &ProxiedClient{
		DatasourceUID:     datasourceUID,
		DatasourceName:    datasourceName,
		DatasourceType:    datasourceType,
		Client:            mcpClient,
		Tools:             tools,
		Resources:         resources,
		ResourceTemplates: resourceTemplates,
		Prompts:           prompts,
	}, nil
}

// listRemoteResources fetches resources and resource templates from the upstream
// server. Errors are logged and treated as empty results so a partially-supporting
// upstream still works.
func listRemoteResources(
	ctx context.Context,
	c *mcp_client.Client,
	caps mcp.ServerCapabilities,
	logger *slog.Logger,
	datasourceUID string,
) ([]mcp.Resource, []mcp.ResourceTemplate) {
	// We probe even when caps.Resources is nil because many MCP servers
	// (Tempo today) register resources without setting the capability flag.
	var resources []mcp.Resource
	resourcesResult, err := c.ListResources(ctx, mcp.ListResourcesRequest{})
	switch {
	case err == nil:
		resources = resourcesResult.Resources
	case errors.Is(err, mcp.ErrMethodNotFound):
		logger.DebugContext(ctx, "remote MCP server does not support resources/list",
			"datasource", datasourceUID)
	default:
		// If capability was advertised this is a real failure; otherwise it is
		// merely informational.
		if caps.Resources != nil {
			logger.WarnContext(ctx, "failed to list resources from remote MCP server",
				"datasource", datasourceUID, "error", err)
		} else {
			logger.DebugContext(ctx, "resources/list probe failed",
				"datasource", datasourceUID, "error", err)
		}
	}

	var resourceTemplates []mcp.ResourceTemplate
	templatesResult, err := c.ListResourceTemplates(ctx, mcp.ListResourceTemplatesRequest{})
	switch {
	case err == nil:
		resourceTemplates = templatesResult.ResourceTemplates
	case errors.Is(err, mcp.ErrMethodNotFound):
		logger.DebugContext(ctx, "remote MCP server does not support resources/templates/list",
			"datasource", datasourceUID)
	default:
		if caps.Resources != nil {
			logger.WarnContext(ctx, "failed to list resource templates from remote MCP server",
				"datasource", datasourceUID, "error", err)
		} else {
			logger.DebugContext(ctx, "resources/templates/list probe failed",
				"datasource", datasourceUID, "error", err)
		}
	}

	return resources, resourceTemplates
}

// CallTool forwards a tool call to the remote MCP server
func (pc *ProxiedClient) CallTool(ctx context.Context, toolName string, arguments map[string]any) (*mcp.CallToolResult, error) {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

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

	// Create the call tool request
	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = arguments

	// Forward the call to the remote server
	result, err := pc.Client.CallTool(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to call tool on remote MCP server: %w", err)
	}

	return result, nil
}

// ListTools returns the tools available from this remote server
// Note: This method doesn't take a context parameter as the tools are cached locally
func (pc *ProxiedClient) ListTools() []mcp.Tool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	// Return a copy to prevent external modification
	result := make([]mcp.Tool, len(pc.Tools))
	copy(result, pc.Tools)
	return result
}

// ListResources returns the static resources cached from this remote server.
func (pc *ProxiedClient) ListResources() []mcp.Resource {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()
	result := make([]mcp.Resource, len(pc.Resources))
	copy(result, pc.Resources)
	return result
}

// ListResourceTemplates returns the resource templates cached from this remote server.
func (pc *ProxiedClient) ListResourceTemplates() []mcp.ResourceTemplate {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()
	result := make([]mcp.ResourceTemplate, len(pc.ResourceTemplates))
	copy(result, pc.ResourceTemplates)
	return result
}

// ListPrompts returns the prompts cached from this remote server.
func (pc *ProxiedClient) ListPrompts() []mcp.Prompt {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()
	result := make([]mcp.Prompt, len(pc.Prompts))
	copy(result, pc.Prompts)
	return result
}

// ReadResource forwards a resources/read call to the remote MCP server.
// uri is the upstream's original URI (not the namespaced URI exposed to clients).
func (pc *ProxiedClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	req := mcp.ReadResourceRequest{}
	req.Params.URI = uri
	result, err := pc.Client.ReadResource(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource on remote MCP server: %w", err)
	}
	return result, nil
}

// GetPrompt forwards a prompts/get call to the remote MCP server.
// promptName is the upstream's original prompt name (not the namespaced name).
func (pc *ProxiedClient) GetPrompt(ctx context.Context, promptName string, args map[string]string) (*mcp.GetPromptResult, error) {
	req := mcp.GetPromptRequest{}
	req.Params.Name = promptName
	req.Params.Arguments = args
	result, err := pc.Client.GetPrompt(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt on remote MCP server: %w", err)
	}
	return result, nil
}

// Close closes the connection to the remote MCP server
func (pc *ProxiedClient) Close() error {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	if pc.Client != nil {
		if err := pc.Client.Close(); err != nil {
			return fmt.Errorf("failed to close MCP client: %w", err)
		}
	}

	return nil
}
