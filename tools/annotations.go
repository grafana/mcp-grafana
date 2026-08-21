package tools

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const annotationsReadDescription = `Fetch Grafana annotations and annotation tags.

Operations:
- list: fetch annotations, filtered by time range, dashboard, panel, alert, user, type, or tags
- tags: list annotation tag names, optionally filtered by tag name

This tool is read-only. See annotations_write to create, update, or delete annotations.`

const annotationsWriteDescription = `Create, update, or delete Grafana annotations.

Operations:
- create: create a new annotation on a dashboard/panel, or organization-wide (standard or Graphite format)
- update: modify fields on an existing annotation by ID (only provided fields are changed)
- delete: delete an annotation by ID

See annotations_read to fetch annotations and annotation tags.`

var AnnotationsRead = mcpgrafana.MustTool(
	"annotations_read",
	annotationsReadDescription,
	annotationsRead,
	mcp.WithTitleAnnotation("Fetch annotations"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var AnnotationsWrite = mcpgrafana.MustTool(
	"annotations_write",
	annotationsWriteDescription,
	annotationsWrite,
	mcp.WithTitleAnnotation("Manage annotations"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithOpenWorldHintAnnotation(false),
)

func AddAnnotationTools(mcp *server.MCPServer, enableWriteTools bool) {
	AnnotationsRead.Register(mcp)
	if enableWriteTools {
		AnnotationsWrite.Register(mcp)
	}
}
