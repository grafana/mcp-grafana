package tools

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const siftReadDescription = `Retrieve Grafana Sift investigations and their analyses.

Operations:
- list_investigations: list recent Sift investigations, with an optional limit
- get_investigation: retrieve an existing investigation by UUID
- get_analysis: retrieve a specific analysis from an investigation

This tool is read-only. See sift_write to create new investigations.`

const siftWriteDescription = `Create a Sift investigation to find error patterns or slow requests, wait for it to complete, and return the analysis.

Operations:
- find_error_pattern_logs: searches Loki logs for elevated error patterns compared to the last day's average
- find_slow_requests: searches relevant Tempo datasources for slow requests

Both operations create an investigation scoped by name, labels, and an optional time range, then poll until it finishes (or times out after 5 minutes) and return the resulting analysis. See sift_read to retrieve an investigation or analysis again later without re-running it.`

var SiftRead = mcpgrafana.MustTool(
	"sift_read",
	siftReadDescription,
	siftRead,
	mcp.WithTitleAnnotation("Retrieve Sift investigations"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var SiftWrite = mcpgrafana.MustTool(
	"sift_write",
	siftWriteDescription,
	siftWrite,
	mcp.WithTitleAnnotation("Create Sift investigations"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddSiftTools registers sift_read (always) and sift_write (when write
// tools are enabled).
func AddSiftTools(mcp *server.MCPServer, enableWriteTools bool) {
	SiftRead.Register(mcp)
	if enableWriteTools {
		SiftWrite.Register(mcp)
	}
}
