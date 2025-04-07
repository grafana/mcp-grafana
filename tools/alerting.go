package tools

import (
	"context"
	"fmt"

	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/prometheus/model/labels"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	DefaultListAlertRulesLimit    = 100
	DefaultListContactPointsLimit = 100
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

	alertRules, err := filterAlertRules(response.Payload, args.LabelSelectors)
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
func filterAlertRules(rules models.ProvisionedAlertRules, selectors []Selector) (models.ProvisionedAlertRules, error) {
	if len(selectors) == 0 {
		return rules, nil
	}

	filteredResult := models.ProvisionedAlertRules{}
	for _, rule := range rules {
		if rule == nil {
			continue
		}

		match, err := matchesSelectors(*rule, selectors)
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
func matchesSelectors(rule models.ProvisionedAlertRule, selectors []Selector) (bool, error) {
	promLabels := labels.FromMap(rule.Labels)

	for _, selector := range selectors {
		match, err := selector.Matches(promLabels)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
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

type ListContactPointsParams struct {
	Limit int     `json:"limit,omitempty" jsonschema:"description=The maximum number of results to return. Default is 100."`
	Name  *string `json:"name,omitempty" jsonschema:"description=Filter contact points by name"`
}

func (p ListContactPointsParams) validate() error {
	if p.Limit < 0 {
		return fmt.Errorf("invalid limit: %d, must be greater than 0", p.Limit)
	}
	return nil
}

type contactPointSummary struct {
	UID  string  `json:"uid"`
	Name string  `json:"name"`
	Type *string `json:"type,omitempty"`
}

func listContactPoints(ctx context.Context, args ListContactPointsParams) ([]contactPointSummary, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("list contact points: %w", err)
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)

	params := provisioning.NewGetContactpointsParams().WithContext(ctx)
	if args.Name != nil {
		params.Name = args.Name
	}

	response, err := c.Provisioning.GetContactpoints(params)
	if err != nil {
		return nil, fmt.Errorf("list contact points: %w", err)
	}

	pagedContactPoints, err := applyLimitToContactPoints(response.Payload, args.Limit)
	if err != nil {
		return nil, fmt.Errorf("list contact points: %w", err)
	}

	return summarizeContactPoints(pagedContactPoints), nil
}

func summarizeContactPoints(contactPoints []*models.EmbeddedContactPoint) []contactPointSummary {
	result := make([]contactPointSummary, 0, len(contactPoints))
	for _, cp := range contactPoints {
		result = append(result, contactPointSummary{
			UID:  cp.UID,
			Name: cp.Name,
			Type: cp.Type,
		})
	}
	return result
}

func applyLimitToContactPoints(items []*models.EmbeddedContactPoint, limit int) ([]*models.EmbeddedContactPoint, error) {
	if limit == 0 {
		limit = DefaultListContactPointsLimit
	}

	if limit > len(items) {
		return items, nil
	}

	return items[:limit], nil
}

var ListContactPoints = mcpgrafana.MustTool(
	"list_contact_points",
	"List contact points",
	listContactPoints,
)

func AddAlertingTools(mcp *server.MCPServer) {
	ListAlertRules.Register(mcp)
	GetAlertRuleByUID.Register(mcp)
	ListContactPoints.Register(mcp)
}
