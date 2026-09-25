package tools

import "github.com/mark3labs/mcp-go/server"

// AddMCPAppTools registers the read-only interactive apps. Query execution must
// be enabled because these tools fetch datasource data. Embedding servers also
// call mcpgrafana.RegisterAppResources to serve the bundled UI.
func AddMCPAppTools(s *server.MCPServer, enableQueryTools bool) {
	AddTraceAppTools(s, enableQueryTools)
	AddServiceHealthAppTools(s, enableQueryTools)
}
