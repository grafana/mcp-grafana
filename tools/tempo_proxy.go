package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// Environment variable names
	TEMPO_PROXY_ENABLED_ENV = "TEMPO_PROXY_ENABLED"
	TEMPO_POLLING_INTERVAL_ENV = "TEMPO_POLLING_INTERVAL"
	
	// Default configuration values
	DEFAULT_POLLING_INTERVAL = 5 * time.Minute
	
	// Smart polling threshold - skip datasources checked within this time
	SKIP_RECENT_CHECK_THRESHOLD = 4 * time.Minute
)

// Register Tempo as a proxy handler on package initialization
func init() {
	RegisterProxyHandler("tempo", tempoHandler)
}

// TempoProxyHandler implements ProxyHandler for Tempo datasources
type TempoProxyHandler struct {
	registry *tempoToolRegistry
}

// Initialize discovers and registers Tempo tools
func (h *TempoProxyHandler) Initialize(ctx context.Context, mcp *server.MCPServer) {
	// Get proxy configuration from context
	config := ProxyConfigFromContext(ctx)
	
	// Check if proxy is disabled
	if !config.TempoEnabled {
		slog.Info("Tempo proxy disabled")
		return
	}
	
	// Initialize the registry
	h.registry = &tempoToolRegistry{
		registeredTools:   make(map[string]*registeredTool),
		datasourceTools:   make(map[string][]string),
		toolToDatasources: make(map[string][]string),
		mcp:               mcp,
		stopPoller:        make(chan struct{}),
	}
	
	// Check if Grafana configuration is available
	grafanaConfig := mcpgrafana.GrafanaConfigFromContext(ctx)
	if grafanaConfig.URL == "" {
		slog.Info("GRAFANA_URL not set - skipping Tempo tool discovery")
		return
	}
	
	// Do initial discovery
	if err := h.registry.discoverAndUpdateTools(ctx); err != nil {
		slog.Error("Error discovering Tempo tools", "error", err)
	}
	
	// Start periodic polling with configured interval
	h.registry.startPolling(ctx, config.TempoPollingInterval)
	slog.Info("Tempo proxy initialized", "polling_interval", config.TempoPollingInterval)
}

// Shutdown stops polling and cleans up resources
func (h *TempoProxyHandler) Shutdown() {
	if h.registry != nil {
		h.registry.shutdown()
	}
}

// TempoProxyParams represents the expected parameters for proxied tempo tools
type TempoProxyParams struct {
	DatasourceUID string `json:"datasource_uid" jsonschema:"required,description=The UID of the Tempo datasource to use"`
}

// DynamicTempoToolParams represents parameters for a dynamically wrapped Tempo tool
type DynamicTempoToolParams struct {
	DatasourceUID string                 `json:"datasource_uid" jsonschema:"required,description=The UID of the Tempo datasource to use"`
	Arguments     map[string]interface{} `json:"arguments,omitempty" jsonschema:"description=Tool-specific arguments"`
}

// tempoToolRegistry manages the lifecycle of Tempo tools
type tempoToolRegistry struct {
	mu                sync.RWMutex
	registeredTools   map[string]*registeredTool // tool name -> registration info
	datasourceTools   map[string][]string        // datasource UID -> tool names
	toolToDatasources map[string][]string        // tool name -> datasource UIDs that provide it
	mcp               *server.MCPServer
	stopPoller        chan struct{}
	pollerRunning     bool
}

// discoveryResult holds the result of discovering tools from a datasource
type discoveryResult struct {
	uid   string
	ds    ProxyDatasource
	tools []mcp.Tool
	err   error
}

// toolDiscovery represents a tool discovered from a specific datasource
type toolDiscovery struct {
	tool           mcp.Tool
	datasourceUID  string
	datasourceName string
}

// registeredTool tracks information about a registered tool
type registeredTool struct {
	name         string   // The registered name (with tempo_ prefix)
	originalName string   // The original tool name from Tempo
	description  string
	schemaHash   string   // Hash of the tool schema for deduplication
	datasources  []string // UIDs of datasources that provide this tool
	handler      interface{}
	lastChecked  map[string]time.Time // datasource UID -> last successful check time
}

var (
	// Global handler instance for tempo
	tempoHandler = &TempoProxyHandler{}
)

// computeToolHash generates a hash of the tool's schema for comparison
func computeToolHash(tool mcp.Tool) string {
	// Create a normalized representation of the tool for comparison
	normalized := map[string]interface{}{
		"description": tool.Description,
		"inputSchema": tool.InputSchema,
	}
	
	data, _ := json.Marshal(normalized)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash)
}

// normalizeTempoToolName converts hyphenated tool names to underscored and adds tempo_ prefix
func normalizeTempoToolName(originalName string) string {
	// Convert hyphens to underscores and add tempo_ prefix
	normalized := strings.ReplaceAll(originalName, "-", "_")
	return fmt.Sprintf("tempo_%s", normalized)
}

// makeUniqueToolName creates a unique tool name when there are conflicts
func makeUniqueToolName(baseName string, datasourceName string) string {
	// Clean the datasource name to be safe for tool names
	safeDsName := strings.ReplaceAll(datasourceName, "-", "_")
	safeDsName = strings.ReplaceAll(safeDsName, " ", "_")
	safeDsName = strings.ToLower(safeDsName)
	
	return fmt.Sprintf("%s_%s", baseName, safeDsName)
}

// createTempoToolHandler creates a handler function for a discovered Tempo tool
func createTempoToolHandler(toolName string, allowedDatasources []string) func(context.Context, DynamicTempoToolParams) (string, error) {
	return func(ctx context.Context, args DynamicTempoToolParams) (string, error) {
		// Check if datasource_uid is provided
		if args.DatasourceUID == "" {
			return "", fmt.Errorf("datasource_uid is required")
		}
		
		// Verify the datasource is allowed for this tool
		allowed := false
		for _, uid := range allowedDatasources {
			if uid == args.DatasourceUID {
				allowed = true
				break
			}
		}
		
		if !allowed {
			return "", fmt.Errorf("datasource %s does not provide tool %s", args.DatasourceUID, toolName)
		}
		
		// Extract the arguments map, or use empty map if nil
		additionalArgs := args.Arguments
		if additionalArgs == nil {
			additionalArgs = make(map[string]interface{})
		}
		
		// Call the proxied tool with the datasource UID and arguments
		return callProxiedTempoTool(ctx, toolName, TempoProxyParams{
			DatasourceUID: args.DatasourceUID,
		}, additionalArgs)
	}
}

// callProxiedTempoTool calls a tool on the Tempo MCP server
func callProxiedTempoTool(ctx context.Context, toolName string, args TempoProxyParams, additionalArgs map[string]interface{}) (string, error) {
	if args.DatasourceUID == "" {
		return "", fmt.Errorf("datasource_uid is required")
	}
	
	// Mark this datasource as used (needs re-check on next poll)
	tempoHandler.registry.mu.Lock()
	if tool, exists := tempoHandler.registry.registeredTools[toolName]; exists {
		if tool.lastChecked != nil {
			// Set to zero time to force re-check on next poll
			tool.lastChecked[args.DatasourceUID] = time.Time{}
		}
	}
	tempoHandler.registry.mu.Unlock()
	
	// Ensure session is initialized
	if err := ensureSession(ctx, args.DatasourceUID); err != nil {
		return "", fmt.Errorf("failed to ensure Tempo session: %w", err)
	}
	
	// Get the original tool name from registry
	tempoHandler.registry.mu.RLock()
	tool, exists := tempoHandler.registry.registeredTools[toolName]
	tempoHandler.registry.mu.RUnlock()
	
	if !exists {
		return "", fmt.Errorf("tool %s not found in registry", toolName)
	}
	
	originalToolName := tool.originalName
	
	// Prepare call parameters
	callParams := mcp.CallToolParams{
		Name:      originalToolName,
		Arguments: additionalArgs,
	}
	
	// Make the proxied call
	resp, err := callMCP(ctx, args.DatasourceUID, "tools/call", callParams)
	if err != nil {
		return "", fmt.Errorf("failed to call Tempo tool %s: %w", originalToolName, err)
	}
	
	// Parse call result
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal call result: %w", err)
	}
	
	var callResult mcp.CallToolResult
	if err := json.Unmarshal(resultBytes, &callResult); err != nil {
		return "", fmt.Errorf("failed to unmarshal call result: %w", err)
	}
	
	// Format the response to include proxy information for test validation
	var responseText string
	if len(callResult.Content) > 0 {
		// Type assertion needed since Content is []mcp.Content (interface)
		if textContent, ok := callResult.Content[0].(mcp.TextContent); ok {
			responseText = textContent.Text
		}
	}
	
	// Add proxy indicators for test validation
	proxyResponse := fmt.Sprintf("Proxied call to %s via datasource %s: %s", 
		originalToolName, args.DatasourceUID, responseText)
	
	return proxyResponse, nil
}

// startPolling starts the background polling goroutine
func (r *tempoToolRegistry) startPolling(ctx context.Context, interval time.Duration) {
	r.mu.Lock()
	if r.pollerRunning {
		r.mu.Unlock()
		return
	}
	r.pollerRunning = true
	r.mu.Unlock()
	
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		
		for {
			select {
			case <-ticker.C:
				if err := r.discoverAndUpdateTools(ctx); err != nil {
					slog.Error("Error during periodic Tempo tool discovery", "error", err)
				}
			case <-r.stopPoller:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// stopPolling stops the background polling
func (r *tempoToolRegistry) stopPolling() {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if !r.pollerRunning {
		return
	}
	
	// Safely close the channel only once
	if r.stopPoller != nil {
		close(r.stopPoller)
		r.stopPoller = nil
	}
	r.pollerRunning = false
}

// discoverAndUpdateTools discovers tools and updates registrations
func (r *tempoToolRegistry) discoverAndUpdateTools(ctx context.Context) error {
	startTime := time.Now()
	
	// Discover all Tempo datasources
	datasources, err := discoverDatasources(ctx, "tempo")
	if err != nil {
		slog.Warn("No Tempo datasources available", "error", err)
		return nil
	}
	
	if len(datasources) == 0 {
		slog.Info("No Tempo datasources found")
		// Unregister all tools if no datasources exist
		r.unregisterAllTools()
		return nil
	}
	
	slog.Info("Starting Tempo tool discovery", 
		"datasource_count", len(datasources),
		"parallel", true)
	
	// Perform parallel discovery
	results, metrics := r.performParallelDiscovery(ctx, datasources)
	
	// Process results into tool mappings
	toolsByHash, successfulDatasources := r.processDiscoveryResults(results)
	
	// Update tool registrations
	r.updateToolRegistrations(toolsByHash, successfulDatasources)
	
	// Remove tools from datasources that no longer exist
	seenDatasources := make(map[string]bool)
	for uid := range datasources {
		seenDatasources[uid] = true
	}
	r.cleanupRemovedDatasources(seenDatasources)
	
	slog.Info("Tempo tool discovery completed",
		"duration", time.Since(startTime),
		"total_datasources", len(datasources),
		"checked", metrics.checked,
		"skipped", metrics.skipped,
		"failed", metrics.failed,
		"successful", metrics.successful)
	
	return nil
}

// discoveryMetrics holds metrics from a discovery run
type discoveryMetrics struct {
	checked    int
	skipped    int
	failed     int
	successful int
}

// performParallelDiscovery discovers tools from datasources in parallel
func (r *tempoToolRegistry) performParallelDiscovery(ctx context.Context, datasources map[string]ProxyDatasource) ([]discoveryResult, discoveryMetrics) {
	metrics := discoveryMetrics{}
	
	// Channel for collecting discovery results
	resultChan := make(chan discoveryResult, len(datasources))
	var wg sync.WaitGroup
	
	// Discover tools from each datasource in parallel
	for uid, ds := range datasources {
		// Skip if recently checked
		if !r.shouldRediscover(uid) {
			slog.Debug("Skipping recently checked datasource", "datasource_uid", uid)
			metrics.skipped++
			continue
		}
		
		metrics.checked++
		wg.Add(1)
		go func(uid string, ds ProxyDatasource) {
			defer wg.Done()
			
			result := discoveryResult{
				uid: uid,
				ds:  ds,
			}
			
			// Initialize session to get tools
			if err := ensureSession(ctx, uid); err != nil {
				result.err = err
				resultChan <- result
				return
			}
			
			// Get session to access discovered tools
			session := sessionManager.GetSession(uid, ds.ID)
			result.tools = session.Tools
			
			resultChan <- result
		}(uid, ds)
	}
	
	// Wait for all discoveries to complete
	wg.Wait()
	close(resultChan)
	
	// Collect results
	results := make([]discoveryResult, 0, metrics.checked)
	for result := range resultChan {
		if result.err != nil {
			metrics.failed++
			slog.Warn("Failed to initialize session for datasource", 
				"datasource_uid", result.uid, 
				"error", result.err)
		} else {
			metrics.successful++
		}
		results = append(results, result)
	}
	
	return results, metrics
}

// shouldRediscover checks if a datasource needs re-discovery
func (r *tempoToolRegistry) shouldRediscover(uid string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	// Always discover if we don't know about this datasource
	foundInAnyTool := false
	for _, tool := range r.registeredTools {
		if lastCheck, exists := tool.lastChecked[uid]; exists {
			foundInAnyTool = true
			// Skip if checked recently (within 80% of polling interval to avoid edge cases)
			skipThreshold := SKIP_RECENT_CHECK_THRESHOLD
			if time.Since(lastCheck) < skipThreshold {
				return false
			}
		}
	}
	
	// Rediscover if it's new (not found in any tool) or hasn't been checked recently
	return !foundInAnyTool || true // Always rediscover even if known, unless recently checked
}

// processDiscoveryResults processes discovery results into tool mappings
func (r *tempoToolRegistry) processDiscoveryResults(results []discoveryResult) (map[string][]toolDiscovery, map[string]time.Time) {
	toolsByHash := make(map[string][]toolDiscovery)
	successfulDatasources := make(map[string]time.Time)
	
	for _, result := range results {
		if result.err != nil {
			continue
		}
		
		// Mark as successfully checked
		successfulDatasources[result.uid] = time.Now()
		
		// Process each tool
		for _, tool := range result.tools {
			hash := computeToolHash(tool)
			toolsByHash[hash] = append(toolsByHash[hash], toolDiscovery{
				tool:           tool,
				datasourceUID:  result.uid,
				datasourceName: result.ds.Name,
			})
		}
	}
	
	return toolsByHash, successfulDatasources
}

// updateToolRegistrations updates the tool registry based on discovered tools
func (r *tempoToolRegistry) updateToolRegistrations(toolsByHash map[string][]toolDiscovery, successfulDatasources map[string]time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Track which tools we've processed
	processedTools := make(map[string]bool)
	
	for hash, discoveries := range toolsByHash {
		if len(discoveries) == 0 {
			continue
		}
		
		// Use the first discovery as the representative
		representative := discoveries[0]
		normalizedName := normalizeTempoToolName(representative.tool.Name)
		
		// Check if tools with same functionality but different names exist
		if len(discoveries) > 1 {
			// Check if all discoveries have the same original name
			sameName := true
			for _, d := range discoveries[1:] {
				if d.tool.Name != representative.tool.Name {
					sameName = false
					break
				}
			}
			
			if sameName {
				// All datasources provide the same tool - register once
				r.registerOrUpdateTool(normalizedName, representative.tool, discoveries, hash, successfulDatasources)
				processedTools[normalizedName] = true
			} else {
				// Different datasources provide tools with different names but same schema
				// Register each with a unique name
				for _, discovery := range discoveries {
					uniqueName := makeUniqueToolName(
						normalizeTempoToolName(discovery.tool.Name),
						discovery.datasourceName,
					)
					r.registerOrUpdateTool(uniqueName, discovery.tool, []toolDiscovery{discovery}, hash, successfulDatasources)
					processedTools[uniqueName] = true
				}
			}
		} else {
			// Only one datasource provides this tool
			r.registerOrUpdateTool(normalizedName, representative.tool, discoveries, hash, successfulDatasources)
			processedTools[normalizedName] = true
		}
	}
	
	// Unregister tools that are no longer provided by any datasource
	for toolName := range r.registeredTools {
		if !processedTools[toolName] {
			r.unregisterTool(toolName)
		}
	}
}

// registerOrUpdateTool registers a new tool or updates an existing one
func (r *tempoToolRegistry) registerOrUpdateTool(toolName string, tool mcp.Tool, discoveries []toolDiscovery, hash string, successfulDatasources map[string]time.Time) {
	datasourceUIDs := make([]string, len(discoveries))
	datasourceNames := make([]string, len(discoveries))
	for i, d := range discoveries {
		datasourceUIDs[i] = d.datasourceUID
		datasourceNames[i] = d.datasourceName
	}
	
	existing, exists := r.registeredTools[toolName]
	if exists {
		// Update existing tool
		existing.datasources = datasourceUIDs
		existing.schemaHash = hash
		existing.originalName = tool.Name // Update in case it changed
		
		// Update lastChecked times for successful datasources
		if existing.lastChecked == nil {
			existing.lastChecked = make(map[string]time.Time)
		}
		for uid, checkTime := range successfulDatasources {
			// Only update if this datasource provides this tool
			for _, dsUID := range datasourceUIDs {
				if dsUID == uid {
					existing.lastChecked[uid] = checkTime
					break
				}
			}
		}
		
		// Update mappings
		r.updateMappings(toolName, datasourceUIDs)
	} else {
		// Register new tool
		var description string
		if len(datasourceUIDs) > 1 {
			description = fmt.Sprintf("%s (via Tempo datasources: %s)", 
				tool.Description, strings.Join(datasourceNames, ", "))
		} else {
			description = fmt.Sprintf("%s (via Tempo datasource: %s)", 
				tool.Description, datasourceNames[0])
		}
		
		handler := createTempoToolHandler(toolName, datasourceUIDs)
		
		convertedTool := mcpgrafana.MustTool(
			toolName,
			description,
			handler,
		)
		
		convertedTool.Register(r.mcp)
		
		// Initialize lastChecked map
		lastChecked := make(map[string]time.Time)
		for _, uid := range datasourceUIDs {
			if checkTime, ok := successfulDatasources[uid]; ok {
				lastChecked[uid] = checkTime
			}
		}
		
		r.registeredTools[toolName] = &registeredTool{
			name:         toolName,
			originalName: tool.Name,
			description:  description,
			schemaHash:   hash,
			datasources:  datasourceUIDs,
			handler:      handler,
			lastChecked:  lastChecked,
		}
		
		// Update mappings
		r.updateMappings(toolName, datasourceUIDs)
		
		slog.Info("Registered tool", "tool_name", toolName)
	}
}

// updateMappings updates the datasource-to-tool mappings
func (r *tempoToolRegistry) updateMappings(toolName string, datasourceUIDs []string) {
	// Clear old mappings for this tool
	if oldUIDs, exists := r.toolToDatasources[toolName]; exists {
		for _, uid := range oldUIDs {
			r.removeToolFromDatasource(uid, toolName)
		}
	}
	
	// Set new mappings
	r.toolToDatasources[toolName] = datasourceUIDs
	for _, uid := range datasourceUIDs {
		if r.datasourceTools[uid] == nil {
			r.datasourceTools[uid] = []string{}
		}
		r.datasourceTools[uid] = append(r.datasourceTools[uid], toolName)
	}
}

// removeToolFromDatasource removes a tool from a datasource's tool list
func (r *tempoToolRegistry) removeToolFromDatasource(datasourceUID, toolName string) {
	tools := r.datasourceTools[datasourceUID]
	filtered := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != toolName {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) > 0 {
		r.datasourceTools[datasourceUID] = filtered
	} else {
		delete(r.datasourceTools, datasourceUID)
	}
}

// unregisterTool removes a tool from the registry
func (r *tempoToolRegistry) unregisterTool(toolName string) {
	tool, exists := r.registeredTools[toolName]
	if !exists {
		return
	}
	
	// Remove from MCP server
	r.mcp.DeleteTools(toolName)
	
	delete(r.registeredTools, toolName)
	delete(r.toolToDatasources, toolName)
	
	// Clean up datasource mappings
	for _, uid := range tool.datasources {
		r.removeToolFromDatasource(uid, toolName)
	}
	
	slog.Info("Unregistered tool", "tool_name", toolName)
}

// cleanupRemovedDatasources removes tools from datasources that no longer exist
func (r *tempoToolRegistry) cleanupRemovedDatasources(seenDatasources map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Find datasources that were removed
	removedDatasources := []string{}
	for uid := range r.datasourceTools {
		if !seenDatasources[uid] {
			removedDatasources = append(removedDatasources, uid)
		}
	}
	
	// Remove tools associated with removed datasources
	for _, uid := range removedDatasources {
		tools := r.datasourceTools[uid]
		for _, toolName := range tools {
			// Check if this tool is still provided by other datasources
			if otherUIDs := r.toolToDatasources[toolName]; len(otherUIDs) > 1 {
				// Tool is provided by other datasources, just update mappings
				filtered := make([]string, 0, len(otherUIDs)-1)
				for _, otherUID := range otherUIDs {
					if otherUID != uid {
						filtered = append(filtered, otherUID)
					}
				}
				r.toolToDatasources[toolName] = filtered
				
				// Update the registered tool
				if tool := r.registeredTools[toolName]; tool != nil {
					tool.datasources = filtered
				}
			} else {
				// Tool is only provided by this datasource, unregister it
				r.unregisterTool(toolName)
			}
		}
		delete(r.datasourceTools, uid)
	}
}

// unregisterAllTools removes all registered tools
func (r *tempoToolRegistry) unregisterAllTools() {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	for toolName := range r.registeredTools {
		r.unregisterTool(toolName)
	}
}

// shutdown performs a graceful shutdown of the registry
func (r *tempoToolRegistry) shutdown() {
	// Stop polling first
	r.stopPolling()
	
	// Unregister all tools
	r.unregisterAllTools()
	
	slog.Info("Tempo proxy shutdown complete")
} 
