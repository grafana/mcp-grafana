package mcpgrafana

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mark3labs/mcp-go/server"
)

// Toolset represents a category of related tools that can be dynamically enabled or disabled
type Toolset struct {
	Name        string
	Description string
	Tools       []Tool
	ToolNames   []string // Names of tools provided by this toolset (e.g., ["grafana_query_prometheus", "grafana_list_prometheus_metric_metadata"])
	AddFunc     func(*server.MCPServer)
}

// DynamicToolManager manages dynamic tool registration and discovery
type DynamicToolManager struct {
	server   *server.MCPServer
	toolsets map[string]*Toolset
	enabled  map[string]bool
	mu       sync.RWMutex
}

// NewDynamicToolManager creates a new dynamic tool manager
func NewDynamicToolManager(srv *server.MCPServer) *DynamicToolManager {
	return &DynamicToolManager{
		server:   srv,
		toolsets: make(map[string]*Toolset),
		enabled:  make(map[string]bool),
	}
}

// RegisterToolset registers a toolset for dynamic discovery
func (dtm *DynamicToolManager) RegisterToolset(toolset *Toolset) {
	dtm.mu.Lock()
	defer dtm.mu.Unlock()
	dtm.toolsets[toolset.Name] = toolset
	slog.Debug("Registered toolset", "name", toolset.Name, "description", toolset.Description)
}

// EnableToolset enables a specific toolset by name
func (dtm *DynamicToolManager) EnableToolset(ctx context.Context, name string) error {
	dtm.mu.Lock()
	defer dtm.mu.Unlock()

	toolset, exists := dtm.toolsets[name]
	if !exists {
		return fmt.Errorf("toolset not found: %s", name)
	}

	if dtm.enabled[name] {
		slog.Debug("Toolset already enabled", "name", name)
		return nil
	}

	// Add tools using the toolset's AddFunc
	// Note: The mcp-go library automatically sends a tools/list_changed notification
	// when AddTool is called (via the Register method), so we don't need to manually
	// send notifications here. This happens because WithToolCapabilities(true) was set
	// during server initialization.
	if toolset.AddFunc != nil {
		toolset.AddFunc(dtm.server)
	}

	dtm.enabled[name] = true
	slog.Info("Enabled toolset", "name", name)
	return nil
}

// DisableToolset disables a specific toolset
// Note: mcp-go doesn't support removing tools at runtime, so this just marks it as disabled
func (dtm *DynamicToolManager) DisableToolset(name string) error {
	dtm.mu.Lock()
	defer dtm.mu.Unlock()

	if _, exists := dtm.toolsets[name]; !exists {
		return fmt.Errorf("toolset not found: %s", name)
	}

	dtm.enabled[name] = false
	slog.Info("Disabled toolset", "name", name)
	return nil
}

// ListToolsets returns information about all available toolsets
func (dtm *DynamicToolManager) ListToolsets() []ToolsetInfo {
	dtm.mu.RLock()
	defer dtm.mu.RUnlock()

	toolsets := make([]ToolsetInfo, 0, len(dtm.toolsets))
	for name, toolset := range dtm.toolsets {
		toolsets = append(toolsets, ToolsetInfo{
			Name:        name,
			Description: toolset.Description,
			Enabled:     dtm.enabled[name],
			ToolNames:   toolset.ToolNames,
		})
	}
	return toolsets
}

// ToolsetInfo provides information about a toolset
type ToolsetInfo struct {
	Name        string   `json:"name" jsonschema:"required,description=The name of the toolset"`
	Description string   `json:"description" jsonschema:"description=Description of what the toolset provides"`
	Enabled     bool     `json:"enabled" jsonschema:"description=Whether the toolset is currently enabled"`
	ToolNames   []string `json:"toolNames" jsonschema:"description=List of tool names provided by this toolset (e.g., ['grafana_query_prometheus', 'grafana_list_prometheus_metric_metadata'])"`
}

// AddDynamicDiscoveryTools adds the list and enable toolset tools to the server
func AddDynamicDiscoveryTools(dtm *DynamicToolManager, srv *server.MCPServer) {
	// Tool to list all available toolsets
	type ListToolsetsRequest struct{}

	listToolsetsHandler := func(ctx context.Context, request ListToolsetsRequest) ([]ToolsetInfo, error) {
		return dtm.ListToolsets(), nil
	}

	listToolsetsTool := MustTool(
		"grafana_list_toolsets",
		"List all available Grafana toolsets that can be enabled dynamically. Each toolset provides a category of related functionality.",
		listToolsetsHandler,
	)
	listToolsetsTool.Register(srv)

	// Tool to enable a specific toolset
	type EnableToolsetRequest struct {
		Toolset string `json:"toolset" jsonschema:"required,description=The name of the toolset to enable (e.g. 'prometheus' 'loki' 'dashboard' 'incident')"`
	}

	enableToolsetHandler := func(ctx context.Context, request EnableToolsetRequest) (string, error) {
		if err := dtm.EnableToolset(ctx, request.Toolset); err != nil {
			return "", err
		}

		// Get toolset info to provide better guidance
		toolsetInfo := dtm.getToolsetInfo(request.Toolset)
		if toolsetInfo == nil {
			return fmt.Sprintf("Successfully enabled toolset: %s. The tools are now available for use.", request.Toolset), nil
		}

		return fmt.Sprintf("Successfully enabled toolset: %s\n\nDescription: %s\n\nNote: All tools are already registered and available. You can now use the tools from this toolset directly.",
			request.Toolset, toolsetInfo.Description), nil
	}

	enableToolsetTool := MustTool(
		"grafana_enable_toolset",
		"Enable a specific Grafana toolset to make its tools available. Use grafana_list_toolsets to see available toolsets.",
		enableToolsetHandler,
	)
	enableToolsetTool.Register(srv)
}

// getToolsetInfo returns information about a specific toolset
func (dtm *DynamicToolManager) getToolsetInfo(name string) *Toolset {
	dtm.mu.RLock()
	defer dtm.mu.RUnlock()
	return dtm.toolsets[name]
}
