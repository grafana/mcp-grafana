package tools

import (
	"context"
	"fmt"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/server"
)

type ListAlertRulesParams struct{}

func listAlertRules(ctx context.Context, args ListAlertRulesParams) (models.ProvisionedAlertRules, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	alertRules, err := c.Provisioning.GetAlertRules()
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}
	return alertRules.Payload, nil
}

var ListAlertRules = mcpgrafana.MustTool(
	"list_alert_rules",
	"List alert rules",
	listAlertRules,
)

type GetAlertRuleByUIDParams struct {
	UID string `json:"uid" jsonschema:"required,description=The uid of the alert rule"`
}

func getAlertRuleByUID(ctx context.Context, args GetAlertRuleByUIDParams) (*models.ProvisionedAlertRule, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	alertRule, err := c.Provisioning.GetAlertRule(args.UID)
	if err != nil {
		return nil, fmt.Errorf("get alert rule by uid %s: %w", args.UID, err)
	}
	return alertRule.Payload, nil
}

var GetAlertRuleByUID = mcpgrafana.MustTool(
	"get_alert_rule_by_uid",
	"Get alert rule by uid",
	getAlertRuleByUID,
)

func AddAlertingTools(mcp *server.MCPServer) {
	ListAlertRules.Register(mcp)
	GetAlertRuleByUID.Register(mcp)
}
