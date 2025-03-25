package tools

import (
	"context"
	"fmt"
	"regexp"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/server"
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
	Labels map[string]string `json:"labels,omitempty"`
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

	// Apply selectors if any
	alertRules := response.Payload
	if len(args.LabelSelectors) > 0 {
		filteredResult := models.ProvisionedAlertRules{}
		for _, rule := range alertRules {
			if rule == nil {
				continue
			}
			match, err := matchesSelectors(*rule, args.LabelSelectors)
			if err != nil {
				return nil, fmt.Errorf("list alert rules: %w", err)
			}
			if match {
				filteredResult = append(filteredResult, rule)
			}
		}
		alertRules = filteredResult
	}

	alertRules, err = applyPagination(alertRules, args.Limit, args.Page)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	return summarizeAlertRules(alertRules), nil
}

// matchesSelectors checks if an alert rule matches all provided selectors
func matchesSelectors(rule models.ProvisionedAlertRule, selectors []Selector) (bool, error) {
	for _, selector := range selectors {
		match, err := matchesSelector(rule, selector)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}

// matchesSelector checks if an alert rule matches a single selector
func matchesSelector(rule models.ProvisionedAlertRule, selector Selector) (bool, error) {
	for _, filter := range selector.Filters {
		value, exists := rule.Labels[filter.Name]

		if !exists {
			// Only match if we're looking for inequality
			if filter.Type == "!=" || filter.Type == "!~" {
				continue // This filter matches, check the next one
			}
			return false, nil
		}

		match, err := matchLabelFilter(value, filter)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}

// matchLabelFilter checks if a label value matches the filter
func matchLabelFilter(value string, filter LabelMatcher) (bool, error) {
	switch filter.Type {
	case "", "=":
		return value == filter.Value, nil
	case "!=":
		return value != filter.Value, nil
	case "=~":
		return regexp.MatchString(filter.Value, value)
	case "!~":
		matched, err := regexp.MatchString(filter.Value, value)
		if err != nil {
			return false, err
		}
		return !matched, nil
	default:
		return false, nil
	}
}

func summarizeAlertRules(alertRules models.ProvisionedAlertRules) []alertRuleSummary {
	result := make([]alertRuleSummary, 0, len(alertRules))
	for _, r := range alertRules {
		title := ""
		if r.Title != nil {
			title = *r.Title
		}

		result = append(result, alertRuleSummary{
			UID:    r.UID,
			Title:  title,
			Labels: r.Labels,
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
