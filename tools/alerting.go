package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/mark3labs/mcp-go/mcp"
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
	LabelSelectors []Selector `json:"label_selectors,omitempty" jsonschema:"description=Optionally\\, a list of matchers to filter alert rules by labels"`
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
	// State can be one of: pending, firing, error, recovering, inactive.
	// "inactive" means the alert state is normal, not firing.
	State          string            `json:"state"`
	Health         string            `json:"health,omitempty"`
	FolderUID      string            `json:"folderUID,omitempty"`
	RuleGroup      string            `json:"ruleGroup,omitempty"`
	For            string            `json:"for,omitempty"`
	LastEvaluation string            `json:"lastEvaluation,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}

func listAlertRules(ctx context.Context, args ListAlertRulesParams) ([]alertRuleSummary, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	// Get configuration data from provisioning API (has UIDs, configuration)
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	provisioningResponse, err := c.Provisioning.GetAlertRules()
	if err != nil {
		return nil, fmt.Errorf("list alert rules (provisioning): %w", err)
	}

	// Get runtime state data from alerting client API (has state, health, etc.)
	alertingClient, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("list alert rules (alerting client): %w", err)
	}
	runtimeResponse, err := alertingClient.GetRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list alert rules (runtime): %w", err)
	}

	// Extract runtime rules from groups
	var runtimeRules []alertingRule
	for _, group := range runtimeResponse.Data.RuleGroups {
		runtimeRules = append(runtimeRules, group.Rules...)
	}

	// Merge the data from both APIs
	mergedRules := mergeAlertRuleData(provisioningResponse.Payload, runtimeRules)

	filteredRules, err := filterMergedAlertRules(mergedRules, args.LabelSelectors)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	paginatedRules, err := applyPaginationToMerged(filteredRules, args.Limit, args.Page)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}

	return summarizeMergedAlertRules(paginatedRules), nil
}

// mergedAlertRule combines data from both provisioning API and runtime API
type mergedAlertRule struct {
	// From provisioning API (configuration)
	UID          string
	Title        string
	FolderUID    string
	RuleGroup    string
	Condition    string
	NoDataState  string
	ExecErrState string
	For          string
	Labels       map[string]string
	Annotations  map[string]string

	// From runtime API (state)
	State          string
	Health         string
	LastEvaluation string
	ActiveAt       string
}

// mergeAlertRuleData combines data from provisioning API and runtime API
func mergeAlertRuleData(provisionedRules []*models.ProvisionedAlertRule, runtimeRules []alertingRule) []mergedAlertRule {
	var merged []mergedAlertRule

	// Create a map of runtime rules by name for quick lookup
	runtimeByName := make(map[string]alertingRule)
	for _, runtime := range runtimeRules {
		runtimeByName[runtime.Name] = runtime
	}

	// Merge each provisioned rule with its runtime counterpart
	for _, provisioned := range provisionedRules {
		title := ""
		if provisioned.Title != nil {
			title = *provisioned.Title
		}

		mergedRule := mergedAlertRule{
			// From provisioning API
			UID:         provisioned.UID,
			Title:       title,
			Labels:      provisioned.Labels,
			Annotations: provisioned.Annotations,
		}

		if provisioned.FolderUID != nil {
			mergedRule.FolderUID = *provisioned.FolderUID
		}
		if provisioned.RuleGroup != nil {
			mergedRule.RuleGroup = *provisioned.RuleGroup
		}
		if provisioned.Condition != nil {
			mergedRule.Condition = *provisioned.Condition
		}
		if provisioned.NoDataState != nil {
			mergedRule.NoDataState = *provisioned.NoDataState
		}
		if provisioned.ExecErrState != nil {
			mergedRule.ExecErrState = *provisioned.ExecErrState
		}
		if provisioned.For != nil {
			mergedRule.For = provisioned.For.String()
		}

		// Try to find matching runtime data by title
		if runtime, found := runtimeByName[title]; found {
			mergedRule.State = runtime.State
			mergedRule.Health = runtime.Health
			mergedRule.LastEvaluation = runtime.LastEvaluation.Format(time.RFC3339)
			if runtime.ActiveAt != nil {
				mergedRule.ActiveAt = runtime.ActiveAt.Format(time.RFC3339)
			}
		}

		merged = append(merged, mergedRule)
	}

	return merged
}

// filterMergedAlertRules filters a list of merged alert rules based on label selectors
func filterMergedAlertRules(rules []mergedAlertRule, selectors []Selector) ([]mergedAlertRule, error) {
	if len(selectors) == 0 {
		return rules, nil
	}

	filteredResult := []mergedAlertRule{}
	for _, rule := range rules {
		match, err := matchesSelectorsForMerged(rule, selectors)
		if err != nil {
			return nil, fmt.Errorf("filtering alert rules: %w", err)
		}

		if match {
			filteredResult = append(filteredResult, rule)
		}
	}

	return filteredResult, nil
}

// matchesSelectorsForMerged checks if a merged alert rule matches all provided selectors
func matchesSelectorsForMerged(rule mergedAlertRule, selectors []Selector) (bool, error) {
	// Convert map[string]string to labels.Labels for compatibility with selector
	lbls := rule.Labels
	if lbls == nil {
		lbls = make(map[string]string)
	}

	for _, selector := range selectors {
		// Create a labels.Labels from the map for the selector
		labelsForSelector := labels.FromMap(lbls)

		match, err := selector.Matches(labelsForSelector)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}

func summarizeMergedAlertRules(alertRules []mergedAlertRule) []alertRuleSummary {
	result := make([]alertRuleSummary, 0, len(alertRules))
	for _, r := range alertRules {
		result = append(result, alertRuleSummary{
			UID:            r.UID,
			Title:          r.Title,
			State:          r.State,
			Health:         r.Health,
			FolderUID:      r.FolderUID,
			RuleGroup:      r.RuleGroup,
			For:            r.For,
			LastEvaluation: r.LastEvaluation,
			Labels:         r.Labels,
			Annotations:    r.Annotations,
		})
	}
	return result
}

// applyPaginationToMerged applies pagination to the list of merged alert rules.
// It doesn't sort the items and relies on the order returned by the API.
func applyPaginationToMerged(items []mergedAlertRule, limit, page int) ([]mergedAlertRule, error) {
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
	"Lists Grafana alert rules, returning a summary including UID, title, current state (e.g., 'pending', 'firing', 'inactive'), and labels. Supports filtering by labels using selectors and pagination. Example label selector: `[{'name': 'severity', 'type': '=', 'value': 'critical'}]`. Inactive state means the alert state is normal, not firing",
	listAlertRules,
	mcp.WithTitleAnnotation("List alert rules"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
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
	"Retrieves the full configuration and detailed status of a specific Grafana alert rule identified by its unique ID (UID). The response includes fields like title, condition, query data, folder UID, rule group, state settings (no data, error), evaluation interval, annotations, and labels.",
	getAlertRuleByUID,
	mcp.WithTitleAnnotation("Get alert rule details"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
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

	filteredContactPoints, err := applyLimitToContactPoints(response.Payload, args.Limit)
	if err != nil {
		return nil, fmt.Errorf("list contact points: %w", err)
	}

	return summarizeContactPoints(filteredContactPoints), nil
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
	"Lists Grafana notification contact points, returning a summary including UID, name, and type for each. Supports filtering by name - exact match - and limiting the number of results.",
	listContactPoints,
	mcp.WithTitleAnnotation("List notification contact points"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type CreateAlertRuleParams struct {
	Title        string            `json:"title" jsonschema:"required,description=The title of the alert rule"`
	RuleGroup    string            `json:"ruleGroup" jsonschema:"required,description=The rule group name"`
	FolderUID    string            `json:"folderUID" jsonschema:"required,description=The folder UID where the rule will be created"`
	Condition    string            `json:"condition" jsonschema:"required,description=The query condition identifier (e.g. 'A'\\, 'B')"`
	Data         any               `json:"data" jsonschema:"required,description=Array of query data objects"`
	NoDataState  string            `json:"noDataState" jsonschema:"required,description=State when no data (NoData\\, Alerting\\, OK)"`
	ExecErrState string            `json:"execErrState" jsonschema:"required,description=State on execution error (NoData\\, Alerting\\, OK)"`
	For          string            `json:"for" jsonschema:"required,description=Duration before alert fires (e.g. '5m')"`
	Annotations  map[string]string `json:"annotations,omitempty" jsonschema:"description=Optional annotations"`
	Labels       map[string]string `json:"labels,omitempty" jsonschema:"description=Optional labels"`
	UID          *string           `json:"uid,omitempty" jsonschema:"description=Optional UID for the alert rule"`
	OrgID        int64             `json:"orgID" jsonschema:"required,description=The organization ID"`
}

func (p CreateAlertRuleParams) validate() error {
	if p.Title == "" {
		return fmt.Errorf("title is required")
	}
	if p.RuleGroup == "" {
		return fmt.Errorf("ruleGroup is required")
	}
	if p.FolderUID == "" {
		return fmt.Errorf("folderUID is required")
	}
	if p.Condition == "" {
		return fmt.Errorf("condition is required")
	}
	if p.Data == nil {
		return fmt.Errorf("data is required")
	}
	if p.NoDataState == "" {
		return fmt.Errorf("noDataState is required")
	}
	if p.ExecErrState == "" {
		return fmt.Errorf("execErrState is required")
	}
	if p.For == "" {
		return fmt.Errorf("for duration is required")
	}
	if p.OrgID <= 0 {
		return fmt.Errorf("orgID is required and must be greater than 0")
	}
	return nil
}

func createAlertRule(ctx context.Context, args CreateAlertRuleParams) (*models.ProvisionedAlertRule, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("create alert rule: %w", err)
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)

	// Parse duration string
	duration, err := time.ParseDuration(args.For)
	if err != nil {
		return nil, fmt.Errorf("create alert rule: invalid duration format %q: %w", args.For, err)
	}

	// Convert Data field to AlertQuery array
	var alertQueries []*models.AlertQuery
	if args.Data != nil {
		// Convert interface{} to JSON and then to AlertQuery structs
		dataBytes, err := json.Marshal(args.Data)
		if err != nil {
			return nil, fmt.Errorf("create alert rule: failed to marshal data: %w", err)
		}
		if err := json.Unmarshal(dataBytes, &alertQueries); err != nil {
			return nil, fmt.Errorf("create alert rule: failed to unmarshal data to AlertQuery: %w", err)
		}
	}

	rule := &models.ProvisionedAlertRule{
		Title:        &args.Title,
		RuleGroup:    &args.RuleGroup,
		FolderUID:    &args.FolderUID,
		Condition:    &args.Condition,
		Data:         alertQueries,
		NoDataState:  &args.NoDataState,
		ExecErrState: &args.ExecErrState,
		For:          func() *strfmt.Duration { d := strfmt.Duration(duration); return &d }(),
		Annotations:  args.Annotations,
		Labels:       args.Labels,
		OrgID:        &args.OrgID,
	}

	if args.UID != nil {
		rule.UID = *args.UID
	}

	// Validate the rule using the built-in OpenAPI validation
	if err := rule.Validate(strfmt.Default); err != nil {
		return nil, fmt.Errorf("create alert rule: invalid rule configuration: %w", err)
	}

	params := provisioning.NewPostAlertRuleParams().WithContext(ctx).WithBody(rule)
	response, err := c.Provisioning.PostAlertRule(params)
	if err != nil {
		return nil, fmt.Errorf("create alert rule: %w", err)
	}

	return response.Payload, nil
}

var CreateAlertRule = mcpgrafana.MustTool(
	"create_alert_rule",
	"Creates a new Grafana alert rule with the specified configuration. Requires title, rule group, folder UID, condition, query data, no data state, execution error state, and duration settings.",
	createAlertRule,
	mcp.WithTitleAnnotation("Create alert rule"),
)

type UpdateAlertRuleParams struct {
	UID          string            `json:"uid" jsonschema:"required,description=The UID of the alert rule to update"`
	Title        string            `json:"title" jsonschema:"required,description=The title of the alert rule"`
	RuleGroup    string            `json:"ruleGroup" jsonschema:"required,description=The rule group name"`
	FolderUID    string            `json:"folderUID" jsonschema:"required,description=The folder UID where the rule will be created"`
	Condition    string            `json:"condition" jsonschema:"required,description=The query condition identifier (e.g. 'A'\\, 'B')"`
	Data         any               `json:"data" jsonschema:"required,description=Array of query data objects"`
	NoDataState  string            `json:"noDataState" jsonschema:"required,description=State when no data (NoData\\, Alerting\\, OK)"`
	ExecErrState string            `json:"execErrState" jsonschema:"required,description=State on execution error (NoData\\, Alerting\\, OK)"`
	For          string            `json:"for" jsonschema:"required,description=Duration before alert fires (e.g. '5m')"`
	Annotations  map[string]string `json:"annotations,omitempty" jsonschema:"description=Optional annotations"`
	Labels       map[string]string `json:"labels,omitempty" jsonschema:"description=Optional labels"`
	OrgID        int64             `json:"orgID" jsonschema:"required,description=The organization ID"`
}

func (p UpdateAlertRuleParams) validate() error {
	if p.UID == "" {
		return fmt.Errorf("uid is required")
	}
	if p.Title == "" {
		return fmt.Errorf("title is required")
	}
	if p.RuleGroup == "" {
		return fmt.Errorf("ruleGroup is required")
	}
	if p.FolderUID == "" {
		return fmt.Errorf("folderUID is required")
	}
	if p.Condition == "" {
		return fmt.Errorf("condition is required")
	}
	if p.Data == nil {
		return fmt.Errorf("data is required")
	}
	if p.NoDataState == "" {
		return fmt.Errorf("noDataState is required")
	}
	if p.ExecErrState == "" {
		return fmt.Errorf("execErrState is required")
	}
	if p.For == "" {
		return fmt.Errorf("for duration is required")
	}
	if p.OrgID <= 0 {
		return fmt.Errorf("orgID is required and must be greater than 0")
	}
	return nil
}

func updateAlertRule(ctx context.Context, args UpdateAlertRuleParams) (*models.ProvisionedAlertRule, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("update alert rule: %w", err)
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)

	// Parse duration string
	duration, err := time.ParseDuration(args.For)
	if err != nil {
		return nil, fmt.Errorf("update alert rule: invalid duration format %q: %w", args.For, err)
	}

	// Convert Data field to AlertQuery array
	var alertQueries []*models.AlertQuery
	if args.Data != nil {
		// Convert interface{} to JSON and then to AlertQuery structs
		dataBytes, err := json.Marshal(args.Data)
		if err != nil {
			return nil, fmt.Errorf("update alert rule: failed to marshal data: %w", err)
		}
		if err := json.Unmarshal(dataBytes, &alertQueries); err != nil {
			return nil, fmt.Errorf("update alert rule: failed to unmarshal data to AlertQuery: %w", err)
		}
	}

	rule := &models.ProvisionedAlertRule{
		UID:          args.UID,
		Title:        &args.Title,
		RuleGroup:    &args.RuleGroup,
		FolderUID:    &args.FolderUID,
		Condition:    &args.Condition,
		Data:         alertQueries,
		NoDataState:  &args.NoDataState,
		ExecErrState: &args.ExecErrState,
		For:          func() *strfmt.Duration { d := strfmt.Duration(duration); return &d }(),
		Annotations:  args.Annotations,
		Labels:       args.Labels,
		OrgID:        &args.OrgID,
	}

	// Validate the rule using the built-in OpenAPI validation
	if err := rule.Validate(strfmt.Default); err != nil {
		return nil, fmt.Errorf("update alert rule: invalid rule configuration: %w", err)
	}

	params := provisioning.NewPutAlertRuleParams().WithContext(ctx).WithUID(args.UID).WithBody(rule)
	response, err := c.Provisioning.PutAlertRule(params)
	if err != nil {
		return nil, fmt.Errorf("update alert rule %s: %w", args.UID, err)
	}

	return response.Payload, nil
}

var UpdateAlertRule = mcpgrafana.MustTool(
	"update_alert_rule",
	"Updates an existing Grafana alert rule identified by its UID. Requires all the same parameters as creating a new rule.",
	updateAlertRule,
	mcp.WithTitleAnnotation("Update alert rule"),
)

type DeleteAlertRuleParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the alert rule to delete"`
}

func (p DeleteAlertRuleParams) validate() error {
	if p.UID == "" {
		return fmt.Errorf("uid is required")
	}
	return nil
}

func deleteAlertRule(ctx context.Context, args DeleteAlertRuleParams) (string, error) {
	if err := args.validate(); err != nil {
		return "", fmt.Errorf("delete alert rule: %w", err)
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)

	params := provisioning.NewDeleteAlertRuleParams().WithContext(ctx).WithUID(args.UID)
	_, err := c.Provisioning.DeleteAlertRule(params)
	if err != nil {
		return "", fmt.Errorf("delete alert rule %s: %w", args.UID, err)
	}

	return fmt.Sprintf("Alert rule %s deleted successfully", args.UID), nil
}

var DeleteAlertRule = mcpgrafana.MustTool(
	"delete_alert_rule",
	"Deletes a Grafana alert rule by its UID. This action cannot be undone.",
	deleteAlertRule,
	mcp.WithTitleAnnotation("Delete alert rule"),
)

func AddAlertingTools(mcp *server.MCPServer) {
	ListAlertRules.Register(mcp)
	GetAlertRuleByUID.Register(mcp)
	CreateAlertRule.Register(mcp)
	UpdateAlertRule.Register(mcp)
	DeleteAlertRule.Register(mcp)
	ListContactPoints.Register(mcp)
}
