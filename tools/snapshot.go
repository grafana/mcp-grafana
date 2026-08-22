package tools

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// These descriptions are deliberately terse relative to admin_read's or
// sift_read's/sift_write's: 'list'/'get' and 'create'/'delete' are each two
// obvious, near-self-describing operations, so a bulleted "Operations:"
// section (the house style used elsewhere) would be pure overhead here, not
// guidance. See the #1092 discussion on right-sizing description detail to
// operation count and confusability, not applying one style uniformly.
const snapshotsReadDescription = `List ('list') or get ('get') Grafana dashboard snapshots. Read-only — see snapshots_write to create or delete.`

const snapshotsWriteDescription = `Create ('create') or delete ('delete') a Grafana dashboard snapshot. See snapshots_read to list or get.`

var SnapshotsRead = mcpgrafana.MustTool(
	"snapshots_read",
	snapshotsReadDescription,
	snapshotsRead,
	mcp.WithTitleAnnotation("List and get snapshots"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var SnapshotsWrite = mcpgrafana.MustTool(
	"snapshots_write",
	snapshotsWriteDescription,
	snapshotsWrite,
	mcp.WithTitleAnnotation("Create and delete snapshots"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddSnapshotTools registers snapshots_read (always) and snapshots_write
// (when write tools are enabled).
func AddSnapshotTools(s *server.MCPServer, enableWriteTools bool) {
	SnapshotsRead.Register(s)
	if enableWriteTools {
		SnapshotsWrite.Register(s)
	}
}
