package mcpgrafana

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yosida95/uritemplate/v3"

	"github.com/grafana/grafana-openapi-client-go/client/datasources"
)

const (
	// mcpProbeTimeout is the timeout for probing a single datasource's MCP endpoint.
	// This is kept short to avoid slow startup when datasources are unreachable.
	mcpProbeTimeout = 5 * time.Second
)

// MCPDatasourceConfig defines configuration for a datasource type that supports MCP
type MCPDatasourceConfig struct {
	Type         string
	EndpointPath string // e.g., "/api/mcp"
}

// mcpEnabledDatasources is a registry of datasource types that support MCP
var mcpEnabledDatasources = map[string]MCPDatasourceConfig{
	"tempo": {Type: "tempo", EndpointPath: "/api/mcp"},
	// Future: add other datasource types here
}

// DiscoveredDatasource represents a datasource that supports MCP
type DiscoveredDatasource struct {
	UID    string
	Name   string
	Type   string
	MCPURL string // The MCP endpoint URL
}

// discoverMCPDatasources discovers datasources that support MCP
// Returns a list of datasources with MCP endpoints
func discoverMCPDatasources(ctx context.Context, logger *slog.Logger) ([]DiscoveredDatasource, error) {
	gc := GrafanaClientFromContext(ctx)
	if gc == nil {
		return nil, fmt.Errorf("grafana client not found in context")
	}

	var discovered []DiscoveredDatasource

	// List all datasources
	resp, err := gc.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list datasources: %w", err)
	}

	// Get the Grafana base URL from context
	config := GrafanaConfigFromContext(ctx)
	if config.URL == "" {
		return nil, fmt.Errorf("grafana url not found in context")
	}
	grafanaBaseURL := config.URL

	// Filter for datasources that support MCP and collect candidates
	type candidate struct {
		uid      string
		name     string
		dsType   string
		dsConfig MCPDatasourceConfig
	}
	var candidates []candidate
	for _, ds := range resp.Payload {
		// Check if this datasource type supports MCP
		dsConfig, supported := mcpEnabledDatasources[ds.Type]
		if !supported {
			continue
		}
		candidates = append(candidates, candidate{
			uid:      ds.UID,
			name:     ds.Name,
			dsType:   ds.Type,
			dsConfig: dsConfig,
		})
	}

	if len(candidates) == 0 {
		logger.DebugContext(ctx, "no candidate MCP datasources found")
		return nil, nil
	}

	transport, err := BuildTransport(&config, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   mcpProbeTimeout,
	}

	// Probe candidates in parallel with timeout
	type probeResult struct {
		ds      DiscoveredDatasource
		enabled bool
	}
	results := make(chan probeResult, len(candidates))
	var wg sync.WaitGroup

	for _, c := range candidates {
		wg.Add(1)
		go func(c candidate) {
			defer wg.Done()

			probeURL := fmt.Sprintf("%s/api/datasources/proxy/uid/%s%s", grafanaBaseURL, c.uid, c.dsConfig.EndpointPath)

			probeCtx, cancel := context.WithTimeoutCause(ctx, mcpProbeTimeout,
				fmt.Errorf("timed out after %s probing MCP endpoint for datasource %s (%s) at %s", mcpProbeTimeout, c.name, c.uid, probeURL))
			defer cancel()

			// Check if the datasource instance has MCP enabled
			// We use a DELETE request to probe the MCP endpoint since:
			// - GET would start an event stream and hang
			// - POST doesn't work with the Grafana OpenAPI client
			// - DELETE returns 200 if MCP is enabled, 404 if not
			req, err := http.NewRequestWithContext(probeCtx, http.MethodDelete, probeURL, nil)
			if err != nil {
				logger.DebugContext(ctx, "failed to create probe request", "datasource", c.uid, "error", err)
				return
			}

			resp, err := httpClient.Do(req)
			if err != nil {
				logger.DebugContext(ctx, "MCP probe failed", "datasource", c.uid, "error", contextCauseOrErr(probeCtx, err))
				return
			}
			defer func() { _ = resp.Body.Close() }()

			// MCP is enabled if we get a 200 response
			if resp.StatusCode == http.StatusOK {
				mcpURL := fmt.Sprintf("%s/api/datasources/proxy/uid/%s%s", grafanaBaseURL, c.uid, c.dsConfig.EndpointPath)
				results <- probeResult{
					ds: DiscoveredDatasource{
						UID:    c.uid,
						Name:   c.name,
						Type:   c.dsType,
						MCPURL: mcpURL,
					},
					enabled: true,
				}
			} else {
				logger.DebugContext(ctx, "MCP probe returned non-OK status", "datasource", c.uid, "status", resp.StatusCode, "url", probeURL)
			}
		}(c)
	}

	// Wait for all probes to complete and close results channel
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	for result := range results {
		if result.enabled {
			discovered = append(discovered, result.ds)
		}
	}

	logger.DebugContext(ctx, "discovered MCP datasources", "count", len(discovered), "candidates", len(candidates))
	return discovered, nil
}

// addDatasourceUidParameter adds a required datasourceUid parameter to a tool's input schema
func addDatasourceUidParameter(tool mcp.Tool, datasourceType string) mcp.Tool {
	modifiedTool := tool
	// Prefix tool name with datasource type (e.g., "tempo_traceql-search")
	modifiedTool.Name = datasourceType + "_" + tool.Name

	// Add datasourceUid to the input schema
	if modifiedTool.InputSchema.Properties == nil {
		modifiedTool.InputSchema.Properties = make(map[string]any)
	}

	modifiedTool.InputSchema.Properties["datasourceUid"] = map[string]any{
		"type":        "string",
		"description": "UID of the " + datasourceType + " datasource to query",
	}

	// Add to required fields
	modifiedTool.InputSchema.Required = append(modifiedTool.InputSchema.Required, "datasourceUid")

	return modifiedTool
}

// parseProxiedToolName extracts datasource type and original tool name from a proxied tool name
// Format: <datasource_type>_<original_tool_name>
// Returns: datasourceType, originalToolName, error
func parseProxiedToolName(toolName string) (string, string, error) {
	parts := strings.SplitN(toolName, "_", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid proxied tool name format: %s", toolName)
	}
	return parts[0], parts[1], nil
}

// proxiedResourceURIScheme is the URN namespace identifier for resources proxied
// from upstream MCP servers. The full URI form is:
//
//	urn:mcp-grafana:<datasourceType>:<datasourceUID>:<percent-encoded-original-uri>
//
// A URN was chosen over a custom scheme because it is a valid RFC3986 URI and
// passes mcp-go's url.ParseRequestURI validation, while keeping the original
// upstream URI recoverable for forwarding.
const proxiedResourceURIScheme = "urn:mcp-grafana"

// namespaceResourceURI rewrites an upstream resource URI to a namespaced URN that
// embeds the datasource type and UID, allowing multiple datasources of the same
// type to expose colliding URIs without conflict.
func namespaceResourceURI(datasourceType, datasourceUID, originalURI string) string {
	return fmt.Sprintf("%s:%s:%s:%s",
		proxiedResourceURIScheme,
		datasourceType,
		datasourceUID,
		url.QueryEscape(originalURI),
	)
}

// parseNamespacedResourceURI is the inverse of namespaceResourceURI. It returns
// (datasourceType, datasourceUID, originalURI, error). If the URI does not match
// the proxied URN form, an error is returned.
func parseNamespacedResourceURI(uri string) (string, string, string, error) {
	if !strings.HasPrefix(uri, proxiedResourceURIScheme+":") {
		return "", "", "", fmt.Errorf("not a proxied resource URI: %s", uri)
	}
	rest := strings.TrimPrefix(uri, proxiedResourceURIScheme+":")
	// rest = <datasourceType>:<datasourceUID>:<encoded-original-uri>
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid proxied resource URI format: %s", uri)
	}
	original, err := url.QueryUnescape(parts[2])
	if err != nil {
		return "", "", "", fmt.Errorf("failed to decode original URI in %s: %w", uri, err)
	}
	return parts[0], parts[1], original, nil
}

// namespacePromptName rewrites an upstream prompt name to be prefixed with the
// datasource type. Format: <datasourceType>_<originalName>. The datasource UID
// is supplied at call time via an injected argument (mirroring the tool pattern)
// rather than encoded in the name, because UIDs may contain underscores which
// would make a positional encoding ambiguous to parse.
func namespacePromptName(datasourceType, originalName string) string {
	return fmt.Sprintf("%s_%s", datasourceType, originalName)
}

// parseProxiedPromptName is the inverse of namespacePromptName.
// Returns (datasourceType, originalName, error).
func parseProxiedPromptName(name string) (string, string, error) {
	parts := strings.SplitN(name, "_", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid proxied prompt name format: %s", name)
	}
	return parts[0], parts[1], nil
}

// addDatasourceUidPromptArgument returns a copy of the prompt with a required
// datasourceUid argument prepended.
func addDatasourceUidPromptArgument(p mcp.Prompt, datasourceType string) mcp.Prompt {
	out := p
	out.Arguments = append([]mcp.PromptArgument{{
		Name:        "datasourceUid",
		Description: "UID of the " + datasourceType + " datasource to query",
		Required:    true,
	}}, p.Arguments...)
	return out
}

// namespaceResource returns a copy of the upstream resource with the URI
// rewritten to the proxied URN form and the human-readable name suffixed with
// the datasource name to disambiguate identical resources from multiple
// datasources of the same type.
func namespaceResource(r mcp.Resource, client *ProxiedClient) mcp.Resource {
	out := r
	out.URI = namespaceResourceURI(client.DatasourceType, client.DatasourceUID, r.URI)
	out.Name = fmt.Sprintf("%s (%s)", r.Name, client.DatasourceName)
	return out
}

// namespaceResourceTemplate is the resource-template counterpart of
// namespaceResource. The upstream URI template is wrapped in the proxied URN
// form so client substitutions still produce a valid namespaced URI.
func namespaceResourceTemplate(t mcp.ResourceTemplate, client *ProxiedClient) mcp.ResourceTemplate {
	out := t
	if t.URITemplate != nil {
		// We cannot percent-encode a URI template body without breaking its
		// variable expressions, so we embed it verbatim. This is acceptable
		// because URI templates produce URIs that the client passes back to us
		// in resources/read; we re-parse the namespaced URI there.
		raw := fmt.Sprintf("%s:%s:%s:%s",
			proxiedResourceURIScheme,
			client.DatasourceType,
			client.DatasourceUID,
			t.URITemplate.Raw(),
		)
		if parsed, err := uritemplate.New(raw); err == nil {
			out.URITemplate = &mcp.URITemplate{Template: parsed}
		}
	}
	out.Name = fmt.Sprintf("%s (%s)", t.Name, client.DatasourceName)
	return out
}

// namespacePrompt rewrites the prompt name to embed datasource type and adds
// a required datasourceUid argument that callers must supply at invocation.
func namespacePrompt(p mcp.Prompt, client *ProxiedClient) mcp.Prompt {
	out := addDatasourceUidPromptArgument(p, client.DatasourceType)
	out.Name = namespacePromptName(client.DatasourceType, p.Name)
	return out
}

// ToolManager manages proxied tools (either per-session or server-wide)
type ToolManager struct {
	sm     *SessionManager
	server *server.MCPServer
	logger *slog.Logger

	// Whether to enable proxied tools.
	enableProxiedTools bool

	// For stdio transport: store clients at manager level (single-tenant).
	// These will be unused for HTTP/SSE transports.
	serverMode    bool // true if using server-wide tools (stdio), false for per-session (HTTP/SSE)
	serverClients map[string]*ProxiedClient
	clientsMutex  sync.RWMutex
}

// NewToolManager creates a new ToolManager
func NewToolManager(sm *SessionManager, mcpServer *server.MCPServer, opts ...toolManagerOption) *ToolManager {
	tm := &ToolManager{
		sm:            sm,
		server:        mcpServer,
		serverClients: make(map[string]*ProxiedClient),
	}
	for _, opt := range opts {
		opt(tm)
	}
	if tm.logger == nil {
		tm.logger = slog.Default()
	}
	return tm
}

type toolManagerOption func(*ToolManager)

// WithProxiedTools sets whether proxied tools are enabled
func WithProxiedTools(enabled bool) toolManagerOption {
	return func(tm *ToolManager) {
		tm.enableProxiedTools = enabled
	}
}

// WithToolManagerLogger sets the logger for the ToolManager.
func WithToolManagerLogger(logger *slog.Logger) toolManagerOption {
	return func(tm *ToolManager) {
		tm.logger = logger
	}
}

// loggerFromCtx returns the logger from the context's GrafanaConfig if available,
// otherwise falls back to the ToolManager's logger.
func (tm *ToolManager) loggerFromCtx(ctx context.Context) *slog.Logger {
	config := GrafanaConfigFromContext(ctx)
	if config.Logger != nil {
		return config.Logger
	}
	return tm.logger
}

// InitializeAndRegisterServerTools discovers datasources and registers tools on the server (for stdio transport)
// This should be called once at server startup for single-tenant stdio servers
func (tm *ToolManager) InitializeAndRegisterServerTools(ctx context.Context) error {
	if !tm.enableProxiedTools {
		return nil
	}

	// Mark as server mode (stdio transport)
	tm.serverMode = true

	logger := tm.loggerFromCtx(ctx)

	// Discover datasources with MCP support
	discovered, err := discoverMCPDatasources(ctx, logger)
	if err != nil {
		return fmt.Errorf("failed to discover MCP datasources: %w", err)
	}

	if len(discovered) == 0 {
		logger.InfoContext(ctx, "no MCP datasources discovered")
		return nil
	}

	// Connect to each datasource and store in manager
	tm.clientsMutex.Lock()
	for _, ds := range discovered {
		client, err := NewProxiedClient(ctx, ds.UID, ds.Name, ds.Type, ds.MCPURL)
		if err != nil {
			logger.ErrorContext(ctx, "failed to create proxied client", "datasource", ds.UID, "error", err)
			continue
		}
		key := ds.Type + "_" + ds.UID
		tm.serverClients[key] = client
	}
	clientCount := len(tm.serverClients)
	tm.clientsMutex.Unlock()

	if clientCount == 0 {
		logger.WarnContext(ctx, "no proxied clients created")
		return nil
	}

	logger.InfoContext(ctx, "connected to proxied MCP servers", "datasources", clientCount)

	// Collect and register all unique tools, namespaced resources, namespaced
	// resource templates, and namespaced prompts. Namespacing keys (tool/prompt
	// name, resource URI) embed the datasource type and UID so multiple
	// datasources of the same type can coexist without collisions.
	tm.clientsMutex.RLock()
	toolMap := make(map[string]mcp.Tool)
	var resources []server.ServerResource
	var resourceTemplates []server.ServerResourceTemplate
	var prompts []server.ServerPrompt

	resourceHandler := NewProxiedResourceHandler(tm.sm, tm)
	promptHandler := NewProxiedPromptHandler(tm.sm, tm)

	for _, client := range tm.serverClients {
		for _, tool := range client.ListTools() {
			toolName := client.DatasourceType + "_" + tool.Name
			if _, exists := toolMap[toolName]; !exists {
				modifiedTool := addDatasourceUidParameter(tool, client.DatasourceType)
				toolMap[toolName] = modifiedTool
			}
		}
		for _, res := range client.ListResources() {
			resources = append(resources, server.ServerResource{
				Resource: namespaceResource(res, client),
				Handler:  resourceHandler.Handle,
			})
		}
		for _, tmpl := range client.ListResourceTemplates() {
			resourceTemplates = append(resourceTemplates, server.ServerResourceTemplate{
				Template: namespaceResourceTemplate(tmpl, client),
				Handler:  resourceHandler.Handle,
			})
		}
		for _, prompt := range client.ListPrompts() {
			prompts = append(prompts, server.ServerPrompt{
				Prompt:  namespacePrompt(prompt, client),
				Handler: promptHandler.Handle,
			})
		}
	}
	tm.clientsMutex.RUnlock()

	for toolName, tool := range toolMap {
		handler := NewProxiedToolHandler(tm.sm, tm, toolName)
		tm.server.AddTool(tool, handler.Handle)
	}
	if len(resources) > 0 {
		tm.server.AddResources(resources...)
	}
	if len(resourceTemplates) > 0 {
		tm.server.AddResourceTemplates(resourceTemplates...)
	}
	if len(prompts) > 0 {
		tm.server.AddPrompts(prompts...)
	}

	logger.InfoContext(ctx, "registered proxied capabilities on server",
		"tools", len(toolMap),
		"resources", len(resources),
		"resource_templates", len(resourceTemplates),
		"prompts", len(prompts))
	return nil
}

// InitializeAndRegisterProxiedTools discovers datasources, creates clients, and registers tools per-session
// This should be called in OnBeforeListTools and OnBeforeCallTool hooks for HTTP/SSE transports
func (tm *ToolManager) InitializeAndRegisterProxiedTools(ctx context.Context, session server.ClientSession) {
	if !tm.enableProxiedTools {
		return
	}

	logger := tm.loggerFromCtx(ctx)

	sessionID := session.SessionID()
	state, exists := tm.sm.GetSession(sessionID)
	if !exists {
		// Session exists in server context but not in our SessionManager yet
		tm.sm.CreateSession(ctx, session)
		state, exists = tm.sm.GetSession(sessionID)
		if !exists {
			logger.ErrorContext(ctx, "failed to create session in SessionManager", "sessionID", sessionID)
			return
		}
	}

	// Step 1: Discover and connect (guaranteed to run exactly once per session)
	state.initOnce.Do(func() {
		// Discover datasources with MCP support
		discovered, err := discoverMCPDatasources(ctx, logger)
		if err != nil {
			logger.ErrorContext(ctx, "failed to discover MCP datasources", "error", err)
			state.mutex.Lock()
			state.proxiedToolsInitialized = true
			state.mutex.Unlock()
			return
		}

		state.mutex.Lock()
		// For each discovered datasource, create a proxied client
		for _, ds := range discovered {
			client, err := NewProxiedClient(ctx, ds.UID, ds.Name, ds.Type, ds.MCPURL)
			if err != nil {
				logger.ErrorContext(ctx, "failed to create proxied client", "datasource", ds.UID, "error", err)
				continue
			}

			// Store the client
			key := ds.Type + "_" + ds.UID
			state.proxiedClients[key] = client
		}
		state.proxiedToolsInitialized = true
		state.mutex.Unlock()

		logger.InfoContext(ctx, "connected to proxied MCP servers", "session", sessionID, "datasources", len(state.proxiedClients))
	})

	// Step 2: Register tools with the MCP server
	state.mutex.Lock()
	defer state.mutex.Unlock()

	if state.proxiedCapabilitiesRegistered {
		return
	}

	if len(state.proxiedClients) == 0 {
		state.proxiedCapabilitiesRegistered = true
		return
	}

	// First pass: collect all unique tools, namespaced resources, and templates
	// across the session's clients. Track which datasources support each tool.
	toolMap := make(map[string]mcp.Tool) // unique tools by name
	var serverResources []server.ServerResource
	var serverResourceTemplates []server.ServerResourceTemplate
	var promptCount int

	resourceHandler := NewProxiedResourceHandler(tm.sm, tm)

	for key, client := range state.proxiedClients {
		for _, tool := range client.ListTools() {
			// Tool name format: datasourceType_originalToolName (e.g., "tempo_traceql-search")
			toolName := client.DatasourceType + "_" + tool.Name
			if _, exists := toolMap[toolName]; !exists {
				modifiedTool := addDatasourceUidParameter(tool, client.DatasourceType)
				toolMap[toolName] = modifiedTool
			}
			state.toolToDatasources[toolName] = append(state.toolToDatasources[toolName], key)
		}

		for _, res := range client.ListResources() {
			ns := namespaceResource(res, client)
			serverResources = append(serverResources, server.ServerResource{
				Resource: ns,
				Handler:  resourceHandler.Handle,
			})
			state.proxiedResources = append(state.proxiedResources, ns)
		}
		for _, tmpl := range client.ListResourceTemplates() {
			ns := namespaceResourceTemplate(tmpl, client)
			serverResourceTemplates = append(serverResourceTemplates, server.ServerResourceTemplate{
				Template: ns,
				Handler:  resourceHandler.Handle,
			})
			state.proxiedResourceTemplates = append(state.proxiedResourceTemplates, ns)
		}
		// Prompts are intentionally skipped on per-session transports.
		// mcp-go v0.46 has no AddSessionPrompts; registering server-wide would
		// leak prompts across tenants. Tempo today exposes no prompts so this
		// is purely defensive.
		promptCount += len(client.ListPrompts())
	}

	// Second pass: register all unique tools at once (reduces listChanged notifications)
	var serverTools []server.ServerTool
	for toolName, tool := range toolMap {
		handler := NewProxiedToolHandler(tm.sm, tm, toolName)
		serverTools = append(serverTools, server.ServerTool{
			Tool:    tool,
			Handler: handler.Handle,
		})
		state.proxiedTools = append(state.proxiedTools, tool)
	}

	if len(serverTools) > 0 {
		if err := tm.server.AddSessionTools(sessionID, serverTools...); err != nil {
			logger.WarnContext(ctx, "failed to add session tools", "session", sessionID, "error", err)
		} else {
			logger.InfoContext(ctx, "registered proxied tools", "session", sessionID, "tools", len(state.proxiedTools))
		}
	}
	if len(serverResources) > 0 {
		if err := tm.server.AddSessionResources(sessionID, serverResources...); err != nil {
			tm.logger.Warn("failed to add session resources", "session", sessionID, "error", err)
		}
	}
	if len(serverResourceTemplates) > 0 {
		if err := tm.server.AddSessionResourceTemplates(sessionID, serverResourceTemplates...); err != nil {
			tm.logger.Warn("failed to add session resource templates", "session", sessionID, "error", err)
		}
	}
	if promptCount > 0 {
		tm.logger.Warn("upstream MCP servers exposed prompts but per-session prompt registration is not supported in this transport; prompts will be ignored",
			"session", sessionID, "prompt_count", promptCount)
	}

	state.proxiedCapabilitiesRegistered = true

	tm.logger.Info("registered proxied capabilities",
		"session", sessionID,
		"tools", len(state.proxiedTools),
		"resources", len(state.proxiedResources),
		"resource_templates", len(state.proxiedResourceTemplates),
		"prompts_skipped", promptCount)
}

// GetServerClient retrieves a proxied client from server-level storage (for stdio transport)
func (tm *ToolManager) GetServerClient(datasourceType, datasourceUID string) (*ProxiedClient, error) {
	tm.clientsMutex.RLock()
	defer tm.clientsMutex.RUnlock()

	key := datasourceType + "_" + datasourceUID
	client, exists := tm.serverClients[key]
	if !exists {
		// List available datasources to help with debugging
		var availableUIDs []string
		for _, c := range tm.serverClients {
			if c.DatasourceType == datasourceType {
				availableUIDs = append(availableUIDs, c.DatasourceUID)
			}
		}

		if len(availableUIDs) > 0 {
			return nil, fmt.Errorf("datasource '%s' not found. Available %s datasources: %v", datasourceUID, datasourceType, availableUIDs)
		}
		return nil, fmt.Errorf("datasource '%s' not found. No %s datasources with MCP support are configured", datasourceUID, datasourceType)
	}

	return client, nil
}
