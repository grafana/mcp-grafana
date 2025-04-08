package tools

import (
	"context"
	"fmt"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	DefaultListAlertRulesLimit = 100
)

type ListAlertRulesParams struct {
	Limit          int        `json:"limit,omitempty" jsonschema:"description=The maximum number of results to return. Default is 100."`
	Page           int        `json:"page,omitempty" jsonschema:"description=The page number to return."`
	LabelSelectors []Selector `json:"label_selectors,omitempty" jsonschema:"description=Optionally, a list of matchers to filter alert rules by labels"`
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
	UID    string            `json:"uid"`
	Title  string            `json:"title"`
	State  string            `json:"state"`
	Labels map[string]string `json:"labels,omitempty"`
}

func listAlertRules(ctx context.Context, args ListAlertRulesParams) ([]alertRuleSummary, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	c, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}
	response, err := c.GetRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	alertRules := []AlertingRule{}
	for _, group := range response.Data.RuleGroups {
		alertRules = append(alertRules, group.Rules...)
	}

	alertRules, err = filterAlertRules(alertRules, args.LabelSelectors)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	alertRules, err = applyPagination(alertRules, args.Limit, args.Page)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	return summarizeAlertRules(alertRules), nil
}

// filterAlertRules filters a list of alert rules based on label selectors
func filterAlertRules(rules []AlertingRule, selectors []Selector) ([]AlertingRule, error) {
	if len(selectors) == 0 {
		return rules, nil
	}

	filteredResult := []AlertingRule{}
	for _, rule := range rules {
		match, err := matchesSelectors(rule, selectors)
		if err != nil {
			return nil, fmt.Errorf("filtering alert rules: %w", err)
		}

		if match {
			filteredResult = append(filteredResult, rule)
		}
	}

	return filteredResult, nil
}

// matchesSelectors checks if an alert rule matches all provided selectors
func matchesSelectors(rule AlertingRule, selectors []Selector) (bool, error) {
	for _, selector := range selectors {
		match, err := selector.Matches(rule.Labels)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}

func summarizeAlertRules(alertRules []AlertingRule) []alertRuleSummary {
	result := make([]alertRuleSummary, 0, len(alertRules))
	for _, r := range alertRules {
		result = append(result, alertRuleSummary{
			UID:    r.UID,
			Title:  r.Name,
			State:  r.State,
			Labels: r.Labels.Map(),
		})
	}
	return result
}

// applyPagination applies pagination to the list of alert rules.
// It doesn't sort the items and relies on the order returned by the API.
func applyPagination(items []AlertingRule, limit, page int) ([]AlertingRule, error) {
	if limit == 0 {
		limit = DefaultListAlertRulesLimit
	}
	if page == 0 {
		page = 1
	}

	start := (page - 1) * limit
	end := start + limit

	if start >= len(items) {
		return nil, nil
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
