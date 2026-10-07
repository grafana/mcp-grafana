package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
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

Read-only: does not create, update, or delete rules, and does not show notification routing.`

const alertRulesWriteDescription = `Create, update, and delete Grafana alert rules.

When to use:
- Creating, updating, or deleting alert rules

Operation 'update' replaces the whole rule: it takes all required fields, not only the changed ones.

Does not list or inspect rules, and does not manage notification routing.`

var AlertRulesRead = mcpgrafana.MustTool(
	"alerting_rules_read",
	alertRulesReadDescription,
	manageRulesRead,
	mcpgrafana.WithTitleAnnotation("Read alert rules"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("alert.rules:read", "alert.rules.external:read", "folders:read"),
)

var AlertRulesWrite = mcpgrafana.MustTool(
	"alerting_rules_write",
	alertRulesWriteDescription,
	manageRulesReadWrite,
	mcpgrafana.WithTitleAnnotation("Write alert rules"),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(
		"alert.rules:read", "alert.rules:create", "alert.rules:write", "alert.rules:delete",
		"alert.provisioning.provenance:write", "folders:read",
	),
)

func AddAlertingTools(s *mcp.Server, enableWriteTools bool) {
	AlertRulesRead.Register(s)
	if enableWriteTools {
		AlertRulesWrite.Register(s)
	}
	ManageRouting.Register(s)
	if enableWriteTools {
		AlertRoutingWrite.Register(s)
	}
	AlertSilencesRead.Register(s)
	if enableWriteTools {
		AlertSilencesWrite.Register(s)
	}
}
