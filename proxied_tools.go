package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

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

// proxiedClientKey is the map key for a proxied client, scoped by org so the
// same datasource UID in different orgs maps to distinct clients.
func proxiedClientKey(orgID int64, datasourceType, datasourceUID string) string {
	return fmt.Sprintf("%d|%s|%s", orgID, datasourceType, datasourceUID)
}

// DiscoveredDatasource represents a datasource that supports MCP
type DiscoveredDatasource struct {
	UID    string
	Name   string
	Type   string
	MCPURL string // The MCP endpoint URL
	OrgID  int64  // The Grafana org the datasource was discovered in
}

// accessibleOrgIDs returns the orgs to discover proxied datasources in, and the
// connection's default org (used when a call omits orgId).
//
// With dynamic multi-org off it returns just the default org (current behavior).
// With it on it returns every org the user belongs to (GET /api/user/orgs),
// always including the default org; for credentials that can't enumerate orgs
// (e.g. service-account tokens, which are single-org) it falls back to the
// default org.
func accessibleOrgIDs(ctx context.Context, logger *slog.Logger) (orgs []int64, defaultOrg int64) {
	defaultOrg = resolveDefaultOrgID(ctx, logger)
	if !DynamicMultiOrgEnabled {
		return []int64{defaultOrg}, defaultOrg
	}
	userOrgs, err := fetchUserOrgIDs(ctx)
	if err != nil || len(userOrgs) == 0 {
		logger.DebugContext(ctx, "could not enumerate user orgs for proxied discovery; using default org", "error", err)
		return []int64{defaultOrg}, defaultOrg
	}
	if !slices.Contains(userOrgs, defaultOrg) {
		userOrgs = append(userOrgs, defaultOrg)
	}
	return userOrgs, defaultOrg
}

// resolveDefaultOrgID returns the org the connection targets when no orgId is
// given: the current org reported by /api/org, falling back to the configured
// OrgID (0 when unset, which Grafana treats as the identity's active org).
func resolveDefaultOrgID(ctx context.Context, logger *slog.Logger) int64 {
	cfg := GrafanaConfigFromContext(ctx)
	var org struct {
		ID int64 `json:"id"`
	}
	if err := grafanaGetJSON(ctx, &cfg, "/api/org", &org); err != nil || org.ID == 0 {
		logger.DebugContext(ctx, "could not resolve default org from /api/org; using configured OrgID", "orgID", cfg.OrgID, "error", err)
		return cfg.OrgID
	}
	return org.ID
}

// fetchUserOrgIDs lists the orgs the current user belongs to via /api/user/orgs.
// Returns an error for identities that aren't users (e.g. service-account
// tokens), so callers fall back to the single default org.
// OrgInfo describes an organization the current user is a member of.
type OrgInfo struct {
	OrgID int64  `json:"orgId"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

// ListUserOrgs returns the organizations the current user belongs to
// (GET /api/user/orgs). It returns an error for identities that cannot
// enumerate orgs (e.g. service-account tokens, which are single-org).
func ListUserOrgs(ctx context.Context) ([]OrgInfo, error) {
	cfg := GrafanaConfigFromContext(ctx)
	var orgs []OrgInfo
	if err := grafanaGetJSON(ctx, &cfg, "/api/user/orgs", &orgs); err != nil {
		return nil, err
	}
	return orgs, nil
}

// DefaultOrgID returns the org the connection targets when no orgId is given.
func DefaultOrgID(ctx context.Context) int64 {
	return resolveDefaultOrgID(ctx, GrafanaConfigFromContext(ctx).LoggerOrDefault())
}

// UserInfo describes the signed-in identity for the current request.
type UserInfo struct {
	Login          string    `json:"login,omitempty"`
	Email          string    `json:"email,omitempty"`
	Name           string    `json:"name,omitempty"`
	IsGrafanaAdmin bool      `json:"isGrafanaAdmin"`
	CurrentOrgID   int64     `json:"currentOrgId"`
	Orgs           []OrgInfo `json:"orgs"`
}

// CurrentUserInfo returns the signed-in user's identity (GET /api/user) plus the
// organizations the credential can access (GET /api/user/orgs). Org membership
// is best-effort: it is empty for identities that can't enumerate orgs (e.g.
// service-account tokens), which remain scoped to their single CurrentOrgID.
func CurrentUserInfo(ctx context.Context) (UserInfo, error) {
	cfg := GrafanaConfigFromContext(ctx)
	var u struct {
		Login          string `json:"login"`
		Email          string `json:"email"`
		Name           string `json:"name"`
		IsGrafanaAdmin bool   `json:"isGrafanaAdmin"`
		OrgID          int64  `json:"orgId"`
	}
	if err := grafanaGetJSON(ctx, &cfg, "/api/user", &u); err != nil {
		return UserInfo{}, err
	}
	info := UserInfo{
		Login:          u.Login,
		Email:          u.Email,
		Name:           u.Name,
		IsGrafanaAdmin: u.IsGrafanaAdmin,
		CurrentOrgID:   u.OrgID,
	}
	if orgs, err := ListUserOrgs(ctx); err == nil {
		info.Orgs = orgs
	}
	return info, nil
}

func fetchUserOrgIDs(ctx context.Context) ([]int64, error) {
	orgs, err := ListUserOrgs(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(orgs))
	for _, o := range orgs {
		if o.OrgID > 0 {
			ids = append(ids, o.OrgID)
		}
	}
	return ids, nil
}

// grafanaGetJSON performs an authenticated GET against the Grafana API and
// decodes a JSON response, using the same transport chain as the rest of the
// server (auth, OrgID header, TLS, etc.).
func grafanaGetJSON(ctx context.Context, cfg *GrafanaConfig, path string, out any) error {
	transport, err := BuildTransport(cfg, nil)
	if err != nil {
		return fmt.Errorf("build transport: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.URL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// discoverMCPDatasources discovers datasources that support MCP
// discoverMCPDatasources discovers MCP-capable datasources across every org the
// credential can access (just the default org when dynamic multi-org is off),
// returning the union tagged with the org each was found in, plus the default
// org a call targets when it omits orgId. Per-org discovery runs in parallel.
func discoverMCPDatasources(ctx context.Context, logger *slog.Logger) ([]DiscoveredDatasource, int64, error) {
	orgs, defaultOrg := accessibleOrgIDs(ctx, logger)

	perOrg := make([][]DiscoveredDatasource, len(orgs))
	var wg sync.WaitGroup
	for i, org := range orgs {
		wg.Add(1)
		go func(i int, org int64) {
			defer wg.Done()
			found, err := discoverMCPDatasourcesForOrg(ctx, org, logger)
			if err != nil {
				logger.DebugContext(ctx, "MCP datasource discovery failed for org", "org", org, "error", err)
				return
			}
			perOrg[i] = found
		}(i, org)
	}
	wg.Wait()

	var discovered []DiscoveredDatasource
	for _, found := range perOrg {
		discovered = append(discovered, found...)
	}
	return discovered, defaultOrg, nil
}

// discoverMCPDatasourcesForOrg discovers MCP-capable datasources within a single
// org. It scopes the request to orgID so the datasource list and MCP probes
// carry X-Grafana-Org-Id: orgID, and tags each result with the org.
func discoverMCPDatasourcesForOrg(ctx context.Context, orgID int64, logger *slog.Logger) ([]DiscoveredDatasource, error) {
	cfg := GrafanaConfigFromContext(ctx)
	cfg.OrgID = orgID
	ctx = WithGrafanaConfig(ctx, cfg)

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
						OrgID:  orgID,
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

	// When dynamic multi-org is enabled, advertise the optional orgId so the
	// datasourceUid can be resolved in a non-default org. OrgIDOverrideMiddleware
	// reads it into the request context, which the handler uses to route.
	if DynamicMultiOrgEnabled {
		modifiedTool.InputSchema.Properties[OrgIDArgument] = map[string]any{
			"type":        "integer",
			"description": orgIDArgumentDescription,
		}
	}

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

	// defaultOrgID is the org a proxied call targets when it omits orgId
	// (server/stdio mode). Resolved during discovery.
	defaultOrgID int64
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
	discovered, defaultOrg, err := discoverMCPDatasources(ctx, logger)
	if err != nil {
		return fmt.Errorf("failed to discover MCP datasources: %w", err)
	}
	tm.defaultOrgID = defaultOrg

	if len(discovered) == 0 {
		logger.InfoContext(ctx, "no MCP datasources discovered")
		return nil
	}

	// Connect to each datasource (in its org) and store in manager
	tm.clientsMutex.Lock()
	for _, ds := range discovered {
		client, err := NewProxiedClient(ctx, ds.OrgID, ds.UID, ds.Name, ds.Type, ds.MCPURL)
		if err != nil {
			logger.ErrorContext(ctx, "failed to create proxied client", "datasource", ds.UID, "org", ds.OrgID, "error", err)
			continue
		}
		tm.serverClients[proxiedClientKey(ds.OrgID, ds.Type, ds.UID)] = client
	}
	clientCount := len(tm.serverClients)
	tm.clientsMutex.Unlock()

	if clientCount == 0 {
		logger.WarnContext(ctx, "no proxied clients created")
		return nil
	}

	logger.InfoContext(ctx, "connected to proxied MCP servers", "datasources", clientCount)

	// Collect and register all unique tools
	tm.clientsMutex.RLock()
	toolMap := make(map[string]mcp.Tool)
	for _, client := range tm.serverClients {
		for _, tool := range client.ListTools() {
			toolName := client.DatasourceType + "_" + tool.Name
			if _, exists := toolMap[toolName]; !exists {
				modifiedTool := addDatasourceUidParameter(tool, client.DatasourceType)
				toolMap[toolName] = modifiedTool
			}
		}
	}
	tm.clientsMutex.RUnlock()

	// Register tools on the server (not per-session)
	for toolName, tool := range toolMap {
		handler := NewProxiedToolHandler(tm.sm, tm, toolName)
		tm.server.AddTool(tool, handler.Handle)
	}

	logger.InfoContext(ctx, "registered proxied tools on server", "tools", len(toolMap))
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
		discovered, defaultOrg, err := discoverMCPDatasources(ctx, logger)
		if err != nil {
			logger.ErrorContext(ctx, "failed to discover MCP datasources", "error", err)
			state.mutex.Lock()
			state.proxiedToolsInitialized = true
			state.mutex.Unlock()
			return
		}

		state.mutex.Lock()
		state.defaultOrgID = defaultOrg
		// For each discovered datasource, create a proxied client scoped to its org
		for _, ds := range discovered {
			client, err := NewProxiedClient(ctx, ds.OrgID, ds.UID, ds.Name, ds.Type, ds.MCPURL)
			if err != nil {
				logger.ErrorContext(ctx, "failed to create proxied client", "datasource", ds.UID, "org", ds.OrgID, "error", err)
				continue
			}

			// Store the client, keyed by org so the same UID in different orgs is distinct
			state.proxiedClients[proxiedClientKey(ds.OrgID, ds.Type, ds.UID)] = client
		}
		state.proxiedToolsInitialized = true
		state.mutex.Unlock()

		logger.InfoContext(ctx, "connected to proxied MCP servers", "session", sessionID, "datasources", len(state.proxiedClients))
	})

	// Step 2: Register tools with the MCP server
	state.mutex.Lock()
	defer state.mutex.Unlock()

	// Check if tools already registered
	if len(state.proxiedTools) > 0 {
		return
	}

	// Check if we have any clients (discovery should have happened above)
	if len(state.proxiedClients) == 0 {
		return
	}

	// First pass: collect all unique tools and track which datasources support them
	toolMap := make(map[string]mcp.Tool) // unique tools by name

	for key, client := range state.proxiedClients {
		remoteTools := client.ListTools()

		for _, tool := range remoteTools {
			// Tool name format: datasourceType_originalToolName (e.g., "tempo_traceql-search")
			toolName := client.DatasourceType + "_" + tool.Name

			// Store the tool if we haven't seen it yet
			if _, exists := toolMap[toolName]; !exists {
				// Add datasourceUid parameter to the tool
				modifiedTool := addDatasourceUidParameter(tool, client.DatasourceType)
				toolMap[toolName] = modifiedTool
			}

			// Track which datasources support this tool
			state.toolToDatasources[toolName] = append(state.toolToDatasources[toolName], key)
		}
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

	if err := tm.server.AddSessionTools(sessionID, serverTools...); err != nil {
		logger.WarnContext(ctx, "failed to add session tools", "session", sessionID, "error", err)
	} else {
		logger.InfoContext(ctx, "registered proxied tools", "session", sessionID, "tools", len(state.proxiedTools))
	}
}

// GetServerClient retrieves a proxied client from server-level storage (for stdio transport)
func (tm *ToolManager) GetServerClient(orgID int64, datasourceType, datasourceUID string) (*ProxiedClient, error) {
	tm.clientsMutex.RLock()
	defer tm.clientsMutex.RUnlock()

	// A call that omits orgId targets the connection's default org.
	if orgID <= 0 {
		orgID = tm.defaultOrgID
	}
	key := proxiedClientKey(orgID, datasourceType, datasourceUID)
	client, exists := tm.serverClients[key]
	if !exists {
		// List available datasources (in this org) to help with debugging
		var availableUIDs []string
		for _, c := range tm.serverClients {
			if c.DatasourceType == datasourceType && c.OrgID == orgID {
				availableUIDs = append(availableUIDs, c.DatasourceUID)
			}
		}

		if len(availableUIDs) > 0 {
			return nil, fmt.Errorf("datasource '%s' not found in org %d. Available %s datasources: %v", datasourceUID, orgID, datasourceType, availableUIDs)
		}
		return nil, fmt.Errorf("datasource '%s' not found in org %d. No %s datasources with MCP support are configured", datasourceUID, orgID, datasourceType)
	}

	return client, nil
}
