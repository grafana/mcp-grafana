package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	DefaultListAlertRulesLimit    = 200
	DefaultListContactPointsLimit = 100
)

const alertRulesReadDescription = `List and inspect Grafana alert rules with filtering capabilities.

When to use:
- Understanding why an alert is or isn't firing
- Auditing alert rule configuration (queries, conditions, labels, notification settings)
- Finding alert rules by state, folder, group, or name
- Comparing rule versions to see what changed

When NOT to use:
- Creating, updating, or deleting alert rules (use alerting_rules_write)
- Checking how alerts are routed to receivers (use alerting_manage_routing)`

const alertRulesWriteDescription = `Create, update, and delete Grafana alert rules.

When to use:
- Creating, updating, or deleting alert rules

To update a rule, first use alerting_rules_read with operation 'get' to retrieve its full configuration, then call this tool with operation 'update' and all required fields plus your changes.

When NOT to use:
- Listing or inspecting alert rules (use alerting_rules_read)
- Checking how alerts are routed to receivers (use alerting_manage_routing)`

var AlertRulesRead = mcpgrafana.MustTool(
	"alerting_rules_read",
	alertRulesReadDescription,
	manageRulesRead,
	mcpgrafana.WithTitleAnnotation("Read alert rules"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

var AlertRulesWrite = mcpgrafana.MustTool(
	"alerting_rules_write",
	alertRulesWriteDescription,
	manageRulesReadWrite,
	mcpgrafana.WithTitleAnnotation("Write alert rules"),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

func AddAlertingTools(s *mcp.Server, enableWriteTools bool) {
	AlertRulesRead.Register(s)
	if enableWriteTools {
		AlertRulesWrite.Register(s)
	}
	ManageRouting.Register(s)
	AlertSilencesRead.Register(s)
	if enableWriteTools {
		AlertSilencesWrite.Register(s)
	}
}
