package tools

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// list/get are two obvious, non-confusable operations, so this stays terse
// rather than following a bulleted "Operations:" house style that would be
// pure overhead here — see the #1092 discussion on right-sizing description
// detail to operation count and confusability.
const incidentsReadDescription = `List ('list') or get ('get') a Grafana incident by ID. See incidents_write to create, update, or add an activity note.`

// create/update/add_activity are also non-confusable by name, but two
// things earn their own clause here rather than a one-liner: 'add_activity'
// isn't self-evident from its name, and the "used judiciously" warning on
// 'create' is safety guidance, not overhead, and must survive consolidation.
const incidentsWriteDescription = `Create ('create'), update ('update'), or add a timeline note to ('add_activity') a Grafana incident. 'add_activity' appends a note to the incident's activity feed; URLs in the body are parsed and attached as context. Creating an incident may notify or alarm many people — use judiciously, sparingly, and only after user confirmation. See incidents_read to list or get incidents.`

var IncidentsRead = mcpgrafana.MustTool(
	"incidents_read",
	incidentsReadDescription,
	incidentsRead,
	mcp.WithTitleAnnotation("List and get incidents"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var IncidentsWrite = mcpgrafana.MustTool(
	"incidents_write",
	incidentsWriteDescription,
	incidentsWrite,
	mcp.WithTitleAnnotation("Create, update, and annotate incidents"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddIncidentTools registers incidents_read (always) and incidents_write
// (when write tools are enabled).
func AddIncidentTools(mcp *server.MCPServer, enableWriteTools bool) {
	IncidentsRead.Register(mcp)
	if enableWriteTools {
		IncidentsWrite.Register(mcp)
	}
}
