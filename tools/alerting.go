package tools

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	DefaultListAlertRulesLimit    = 200
	DefaultListContactPointsLimit = 100
)

const manageAlertRulesDescriptionFmt = `%s

When to use:
- Understanding why an alert is or isn't firing
- Auditing alert rule configuration (queries, conditions, labels, notification settings)
- Finding alert rules by state, folder, group, or name
%s
When NOT to use:
- Checking how alerts are routed to receivers (use alerting_manage_routing)%s`

func manageAlertRulesDescription(readOnly bool) string {
	if readOnly {
		return fmt.Sprintf(manageAlertRulesDescriptionFmt,
			"List and inspect Grafana alert rules with filtering capabilities.",
			"- Comparing rule versions to see what changed\n",
			"\n- Modifying or creating alert rules (read-only tool)",
		)
	}
	return fmt.Sprintf(manageAlertRulesDescriptionFmt,
		"Manage Grafana alert rules with full CRUD capabilities and filtering.",
		"- Creating, updating, or deleting alert rules\n- Comparing rule versions to see what changed\n",
		"",
	)
}

var ManageRulesRead = mcpgrafana.MustTool(
	"alerting_manage_rules",
	manageAlertRulesDescription(true),
	manageRulesRead,
	mcp.WithTitleAnnotation("Manage alert rules"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var ManageRulesReadWrite = mcpgrafana.MustTool(
	"alerting_manage_rules",
	manageAlertRulesDescription(false),
	manageRulesReadWrite,
	mcp.WithTitleAnnotation("Manage alert rules"),
	mcp.WithDestructiveHintAnnotation(true),
)

func AddAlertingTools(mcp *server.MCPServer, enableWriteTools bool) {
	if enableWriteTools {
		ManageRulesReadWrite.Register(mcp)
		ManageAlertmanagerReadWrite.Register(mcp)
	} else {
		ManageRulesRead.Register(mcp)
		ManageAlertmanagerRead.Register(mcp)
	}
	ManageRouting.Register(mcp)
}
