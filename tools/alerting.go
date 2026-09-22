package tools

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	DefaultListAlertRulesLimit    = 200
	DefaultListContactPointsLimit = 100
)

const getAlertRulesDescription = `List and inspect Grafana alert rules with filtering capabilities.

When to use:
- Understanding why an alert is or isn't firing
- Auditing alert rule configuration (queries, conditions, labels, notification settings)
- Finding alert rules by state, folder, group, or name
- Comparing rule versions to see what changed

When NOT to use:
- Creating, updating, or deleting alert rules (use alerting_manage_rules)
- Checking how alerts are routed to receivers (use alerting_manage_routing)`

const manageAlertRulesDescriptionFmt = `Manage Grafana alert rules: create, update, and delete.

When to use:
- Creating, updating, or deleting alert rules
%s
When NOT to use:
- Listing or inspecting alert rules (use alerting_get_rules)
- Checking how alerts are routed to receivers (use alerting_manage_routing)`

func manageAlertRulesDescription() string {
	return fmt.Sprintf(manageAlertRulesDescriptionFmt,
		"\nTo update a rule, first use alerting_get_rules with operation 'get' to retrieve its full configuration, then call this tool with operation 'update' and all required fields plus your changes.\n",
	)
}

var GetRules = mcpgrafana.MustTool(
	"alerting_get_rules",
	getAlertRulesDescription,
	manageRulesRead,
	mcpgrafana.WithTitleAnnotation("Get alert rules"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

var ManageRulesReadWrite = mcpgrafana.MustTool(
	"alerting_manage_rules",
	manageAlertRulesDescription(),
	manageRulesReadWrite,
	mcpgrafana.WithTitleAnnotation("Manage alert rules"),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

func AddAlertingTools(s *mcp.Server, enableWriteTools bool) {
	GetRules.Register(s)
	if enableWriteTools {
		ManageRulesReadWrite.Register(s)
	}
	ManageRouting.Register(s)
	GetSilences.Register(s)
	if enableWriteTools {
		ManageSilencesReadWrite.Register(s)
	}
}
