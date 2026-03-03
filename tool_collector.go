package mcpgrafana

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ToolCollector satisfies ToolAdder by collecting tools into a map
// instead of registering them with an MCPServer. This is used by CLI mode
// to build a tool registry without starting an MCP server.
type ToolCollector struct {
	tools map[string]Tool
}

// NewToolCollector creates a new ToolCollector.
func NewToolCollector() *ToolCollector {
	return &ToolCollector{tools: make(map[string]Tool)}
}

// AddTool implements ToolAdder by storing the tool in the collector's map.
func (c *ToolCollector) AddTool(tool mcp.Tool, handler server.ToolHandlerFunc) {
	c.tools[tool.Name] = Tool{Tool: tool, Handler: handler}
}

// Tools returns the collected tools as a map keyed by tool name.
func (c *ToolCollector) Tools() map[string]Tool {
	return c.tools
}
