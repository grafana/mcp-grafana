package tools

import (
	"context"
	"fmt"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/server"
)

const (
	DefaultListAlertRulesLimit = 100
)

type ListAlertRulesParams struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=The maximum number of results to return. Default is 100."`
	Page  int `json:"page,omitempty" jsonschema:"description=The page number to return."`
}

func (p ListAlertRulesParams) validate() error {
	if p.Limit < 0 {
		return fmt.Errorf("invalid limit: %d, must be greater than 0", p.Limit)
	}
	if p.Page < 0 {
		return fmt.Errorf("invalid page: %d, must be greater than 0", p.Page)
	}

	return nil
}

type alertRuleSummary struct {
	UID   string `json:"uid"`
	Title string `json:"title"`
}

func listAlertRules(ctx context.Context, args ListAlertRulesParams) ([]alertRuleSummary, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)
	response, err := c.Provisioning.GetAlertRules()
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	alertRules, err := applyPagination(response.Payload, args.Limit, args.Page)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	return summarizeAlertRules(alertRules), nil
}

func summarizeAlertRules(alertRules models.ProvisionedAlertRules) []alertRuleSummary {
	result := make([]alertRuleSummary, 0, len(alertRules))
	for _, r := range alertRules {
		title := ""
		if r.Title != nil {
			title = *r.Title
		}

		result = append(result, alertRuleSummary{
			UID:   r.UID,
			Title: title,
		})
	}
	return result
}

// applyPagination applies pagination to the list of alert rules.
// It doesn't sort the items and relies on the order returned by the API.
func applyPagination(items models.ProvisionedAlertRules, limit, page int) (models.ProvisionedAlertRules, error) {
	if limit == 0 {
		limit = DefaultListAlertRulesLimit
	}
	if page == 0 {
		page = 1
	}

	start := (page - 1) * limit
	end := start + limit

	if start >= len(items) {
		return models.ProvisionedAlertRules{}, nil
	} else if end > len(items) {
		return items[start:], nil
	}

	return items[start:end], nil
}

var ListAlertRules = mcpgrafana.MustTool(
	"list_alert_rules",
	"List alert rules",
	listAlertRules,
)

type GetAlertRuleByUIDParams struct {
	UID string `json:"uid" jsonschema:"required,description=The uid of the alert rule"`
}

func (p GetAlertRuleByUIDParams) validate() error {
	if p.UID == "" {
		return fmt.Errorf("uid is required")
	}

	return nil
}

func getAlertRuleByUID(ctx context.Context, args GetAlertRuleByUIDParams) (*models.ProvisionedAlertRule, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("get alert rule by uid: %w", err)
	}

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
