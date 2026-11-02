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

Creation defaults to Grafana-managed, even when queries use an external datasource.
Use managed_by='datasource' when requested or when established conversation or environment context clearly identifies a datasource-managed workflow. An external query datasource or an empty Grafana rule list alone is insufficient. Ask if context conflicts.
Datasource-managed creation uses a Mimir/Cortex or Loki ruler with namespace, rule_group, title and expression. datasource_uid is optional if exactly one compatible datasource exists. Ambiguity returns candidates for selection. Never falls back to Grafana-managed creation.

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
	// Datasource-managed rules look up the datasource first.
	mcpgrafana.RequiresPermissions("datasources:read"),
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
		"datasources:read", "alert.rules.external:read", "alert.rules.external:write",
	),
)

// AddAlertingTools registers the alerting tools. Each write tool is registered
// only when writeEnabled reports its name as enabled, so it can be opted back in
// individually under --disable-write.
func AddAlertingTools(s *mcp.Server, writeEnabled func(name string) bool) {
	AlertRulesRead.Register(s)
	if writeEnabled(AlertRulesWrite.Tool.Name) {
		AlertRulesWrite.Register(s)
	}
	ManageRouting.Register(s)
	if writeEnabled(AlertRoutingWrite.Tool.Name) {
		AlertRoutingWrite.Register(s)
	}
	AlertSilencesRead.Register(s)
	if writeEnabled(AlertSilencesWrite.Tool.Name) {
		AlertSilencesWrite.Register(s)
	}
}
