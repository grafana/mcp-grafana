package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/prometheus/prometheus/model/labels"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

func manageRulesRead(ctx context.Context, args ManageRulesReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("alerting_manage_rules: %w", err)
	}

	switch args.Operation {
	case "list":
		opts, err := args.toGetRulesOpts()
		if err != nil {
			return nil, fmt.Errorf("alerting_manage_rules: %w", err)
		}
		selectors, err := args.parseLabelSelectors()
		if err != nil {
			return nil, fmt.Errorf("alerting_manage_rules: %w", err)
		}
		if args.DatasourceUID != nil && *args.DatasourceUID != "" {
			return listDatasourceAlertRules(ctx, *args.DatasourceUID, opts, selectors)
		}
		return listGrafanaRules(ctx, opts, selectors)
	case "get":
		return getAlertRuleDetail(ctx, args.RuleUID, args.LimitAlerts)
	case "versions":
		return getAlertRuleVersions(ctx, args.RuleUID)
	default:
		return nil, fmt.Errorf("alerting_manage_rules: unknown operation %q", args.Operation)
	}
}

func manageRulesReadWrite(ctx context.Context, args ManageRulesReadWriteParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("alerting_manage_rules: %w", err)
	}

	switch args.Operation {
	case "list":
		opts, err := args.toGetRulesOpts()
		if err != nil {
			return nil, fmt.Errorf("alerting_manage_rules: %w", err)
		}
		selectors, err := args.parseLabelSelectors()
		if err != nil {
			return nil, fmt.Errorf("alerting_manage_rules: %w", err)
		}
		if args.DatasourceUID != nil && *args.DatasourceUID != "" {
			return listDatasourceAlertRules(ctx, *args.DatasourceUID, opts, selectors)
		}
		return listGrafanaRules(ctx, opts, selectors)
	case "get":
		return getAlertRuleDetail(ctx, args.RuleUID, args.LimitAlerts)
	case "versions":
		return getAlertRuleVersions(ctx, args.RuleUID)
	case "create":
		cp, err := args.toCreateParams()
		if err != nil {
			return nil, fmt.Errorf("alerting_manage_rules: %w", err)
		}
		return createAlertRule(ctx, cp)
	case "update":
		up, err := args.toUpdateParams()
		if err != nil {
			return nil, fmt.Errorf("alerting_manage_rules: %w", err)
		}
		return updateAlertRule(ctx, up)
	case "delete":
		return deleteAlertRule(ctx, DeleteAlertRuleParams{
			UID: args.RuleUID,
		})
	default:
		return nil, fmt.Errorf("alerting_manage_rules: unknown operation %q", args.Operation)
	}
}

func getAlertRuleDetail(ctx context.Context, uid string, limitAlerts int) (*alertRuleDetail, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	alertRule, err := c.Provisioning.GetAlertRuleWithParams(
		provisioning.NewGetAlertRuleParamsWithContext(ctx).WithUID(uid),
	)
	if err != nil {
		return nil, fmt.Errorf("get alert rule %s: %w", uid, err)
	}

	ac, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating alerting client for rule %s: %w", uid, err)
	}

	opts := &GetRulesOpts{LimitAlerts: limitAlerts}
	if alertRule.Payload.FolderUID != nil {
		opts.FolderUID = *alertRule.Payload.FolderUID
	}
	if alertRule.Payload.RuleGroup != nil {
		opts.RuleGroup = *alertRule.Payload.RuleGroup
	}

	rulesResp, err := ac.GetRules(ctx, opts)
	if err != nil {
		mcpgrafana.LoggerFromContext(ctx).WarnContext(ctx, "failed to fetch runtime state for alert rule",
			"uid", uid, "error", err)
		detail := mergeRuleDetail(alertRule.Payload, nil)
		return &detail, nil
	}

	runtime := findRuleInResponse(rulesResp, uid)
	detail := mergeRuleDetail(alertRule.Payload, runtime)
	return &detail, nil
}

func getAlertRuleVersions(ctx context.Context, uid string) (any, error) {
	ac, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating alerting client: %w", err)
	}

	versions, err := ac.GetRuleVersions(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("get rule versions for %s: %w", uid, err)
	}

	return versions, nil
}

// mergeRuleDetail combines a provisioned rule's config with runtime state
// from the Prometheus rules API response. If runtime is nil the state fields
// are left at their zero values.
func mergeRuleDetail(provisioned *models.ProvisionedAlertRule, runtime *alertingRule) alertRuleDetail {
	detail := alertRuleDetail{
		UID:                         provisioned.UID,
		Labels:                      provisioned.Labels,
		Annotations:                 provisioned.Annotations,
		KeepFiringFor:               provisioned.KeepFiringFor.String(),
		MissingSeriesEvalsToResolve: provisioned.MissingSeriesEvalsToResolve,
	}

	if provisioned.Title != nil {
		detail.Title = *provisioned.Title
	}
	if provisioned.FolderUID != nil {
		detail.FolderUID = *provisioned.FolderUID
	}
	if provisioned.RuleGroup != nil {
		detail.RuleGroup = *provisioned.RuleGroup
	}
	if provisioned.Condition != nil {
		detail.Condition = *provisioned.Condition
	}
	// NoDataState, ExecErrState are set empty by grafana HTTP API for a recording rule
	// non-nil checks are applied as api expects to have a defined value for these fields
	if provisioned.NoDataState != nil {
		detail.NoDataState = *provisioned.NoDataState
	}
	if provisioned.ExecErrState != nil {
		detail.ExecErrState = *provisioned.ExecErrState
	}
	if provisioned.For != nil {
		detail.For = provisioned.For.String()
	}
	if provisioned.Record != nil {
		detail.Record = (*Record)(provisioned.Record)
	}

	detail.IsPaused = provisioned.IsPaused
	detail.NotificationSettings = provisioned.NotificationSettings
	detail.Data = provisioned.Data

	if runtime != nil {
		detail.State = normalizeState(runtime.State)
		detail.Health = runtime.Health
		detail.Type = runtime.Type
		detail.LastEvaluation = runtime.LastEvaluation.Format(time.RFC3339)
		detail.LastError = runtime.LastError
		detail.Alerts = runtime.Alerts
	}

	return detail
}

func normalizeState(state string) string {
	if state == "inactive" {
		return "normal"
	}
	return state
}

func findRuleInResponse(resp *rulesResponse, uid string) *alertingRule {
	for _, group := range resp.Data.RuleGroups {
		for i, rule := range group.Rules {
			if rule.UID == uid {
				return &group.Rules[i]
			}
		}
	}
	return nil
}

func listGrafanaRules(ctx context.Context, opts *GetRulesOpts, labelSelectors []Selector) ([]alertRuleSummary, error) {
	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating alerting client: %w", err)
	}

	resp, err := client.GetRules(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing alert rules: %w", err)
	}

	summaries := convertRulesResponseToSummary(resp)

	if len(labelSelectors) > 0 {
		summaries, err = filterSummaryByLabels(summaries, labelSelectors)
		if err != nil {
			return nil, fmt.Errorf("filtering alert rules: %w", err)
		}
	}

	// Server-side rule_limit returns complete groups, so it may exceed the
	// requested limit. Enforce the limit client-side as well.
	if opts != nil {
		summaries = applyRuleLimit(summaries, opts.RuleLimit)
	}

	return summaries, nil
}

func filterSummaryByRuleType(summaries []alertRuleSummary, ruleType string) []alertRuleSummary {
	filtered := make([]alertRuleSummary, 0, len(summaries))
	for _, s := range summaries {
		if s.Type == ruleType {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func applyRuleLimit(summaries []alertRuleSummary, ruleLimit int) []alertRuleSummary {
	limit := ruleLimit
	if limit == 0 {
		limit = DefaultListAlertRulesLimit
	}
	if limit > maxRulesLimit {
		limit = maxRulesLimit
	}
	if len(summaries) > limit {
		return summaries[:limit]
	}
	return summaries
}

func convertRulesResponseToSummary(resp *rulesResponse) []alertRuleSummary {
	var result []alertRuleSummary
	for _, group := range resp.Data.RuleGroups {
		for _, rule := range group.Rules {
			summary := alertRuleSummary{
				UID:       rule.UID,
				Title:     rule.Name,
				State:     normalizeState(rule.State),
				Health:    rule.Health,
				FolderUID: group.FolderUID,
				RuleGroup: group.Name,
			}
			if rule.Duration > 0 {
				summary.For = formatDuration(rule.Duration)
			}
			if !rule.LastEvaluation.IsZero() {
				summary.LastEvaluation = rule.LastEvaluation.Format(time.RFC3339)
			}
			if !rule.Labels.IsEmpty() {
				summary.Labels = rule.Labels.Map()
			}
			if !rule.Annotations.IsEmpty() {
				summary.Annotations = rule.Annotations.Map()
			}
			result = append(result, summary)
		}
	}
	return result
}

func filterSummaryByLabels(summaries []alertRuleSummary, selectors []Selector) ([]alertRuleSummary, error) {
	var filtered []alertRuleSummary
	for _, s := range summaries {
		lbls := s.Labels
		if lbls == nil {
			lbls = make(map[string]string)
		}
		labelsForSelector := labels.FromMap(lbls)
		match := true
		for _, sel := range selectors {
			m, err := sel.Matches(labelsForSelector)
			if err != nil {
				return nil, err
			}
			if !m {
				match = false
				break
			}
		}
		if match {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}

func createAlertRule(ctx context.Context, args CreateAlertRuleParams) (*models.ProvisionedAlertRule, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("create alert rule: %w", err)
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)

	duration, err := time.ParseDuration(args.For)
	if err != nil {
		return nil, fmt.Errorf("create alert rule: invalid duration format for parameter for: %q: %w", args.For, err)
	}

	convertedData, err := convertAlertQueries(args.Data)
	if err != nil {
		return nil, fmt.Errorf("create alert rule: %w", err)
	}
	keepFiringFor := time.Duration(0)
	if args.KeepFiringFor != "" {
		keepFiringFor, err = time.ParseDuration(args.KeepFiringFor)
		if err != nil {
			return nil, fmt.Errorf("create alert rule: invalid duration format for parameter keepFiringFor: %q: %w", args.KeepFiringFor, err)
		}
	}

	notificationSettings := convertNotificationSettings(args.NotificationSettings)
	record := convertRecord(args.Record)

	rule := &models.ProvisionedAlertRule{
		Title:                       &args.Title,
		RuleGroup:                   &args.RuleGroup,
		FolderUID:                   &args.FolderUID,
		Condition:                   &args.Condition,
		Data:                        convertedData,
		NoDataState:                 &args.NoDataState,
		ExecErrState:                &args.ExecErrState,
		For:                         func() *strfmt.Duration { d := strfmt.Duration(duration); return &d }(),
		Annotations:                 args.Annotations,
		Labels:                      args.Labels,
		OrgID:                       &args.OrgID,
		IsPaused:                    args.IsPaused,
		KeepFiringFor:               func() strfmt.Duration { d := strfmt.Duration(keepFiringFor); return d }(),
		MissingSeriesEvalsToResolve: args.MissingSeriesEvalsToResolve,
		NotificationSettings:        notificationSettings,
		Record:                      record,
	}

	if args.UID != nil {
		rule.UID = *args.UID
	}

	if err := rule.Validate(strfmt.Default); err != nil {
		return nil, fmt.Errorf("create alert rule: invalid rule configuration: %w", err)
	}

	params := provisioning.NewPostAlertRuleParams().WithContext(ctx).WithBody(rule)

	disableProvenance := true
	if args.DisableProvenance != nil {
		disableProvenance = *args.DisableProvenance
	}
	if disableProvenance {
		header := "true"
		params = params.WithXDisableProvenance(&header)
	}
	response, err := c.Provisioning.PostAlertRule(params)
	if err != nil {
		return nil, fmt.Errorf("create alert rule: %w", err)
	}

	return response.Payload, nil
}

func updateAlertRule(ctx context.Context, args UpdateAlertRuleParams) (*models.ProvisionedAlertRule, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("update alert rule: %w", err)
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)

	duration, err := time.ParseDuration(args.For)
	if err != nil {
		return nil, fmt.Errorf("update alert rule: invalid duration format for parameter for: %q: %w", args.For, err)
	}

	convertedData, err := convertAlertQueries(args.Data)
	if err != nil {
		return nil, fmt.Errorf("update alert rule: %w", err)
	}

	keepFiringFor := time.Duration(0)
	if args.KeepFiringFor != "" {
		keepFiringFor, err = time.ParseDuration(args.KeepFiringFor)
		if err != nil {
			return nil, fmt.Errorf("update alert rule: invalid duration format for parameter keepFiringFor: %q: %w", args.KeepFiringFor, err)
		}
	}

	notificationSettings := convertNotificationSettings(args.NotificationSettings)

	record := convertRecord(args.Record)

	// MissingSeriesEvalsToResolve, Provenance are set to defaults
	rule := &models.ProvisionedAlertRule{
		UID:                         args.UID,
		Title:                       &args.Title,
		RuleGroup:                   &args.RuleGroup,
		FolderUID:                   &args.FolderUID,
		Condition:                   &args.Condition,
		Data:                        convertedData,
		NoDataState:                 &args.NoDataState,
		ExecErrState:                &args.ExecErrState,
		For:                         func() *strfmt.Duration { d := strfmt.Duration(duration); return &d }(),
		Annotations:                 args.Annotations,
		Labels:                      args.Labels,
		OrgID:                       &args.OrgID,
		IsPaused:                    args.IsPaused,
		MissingSeriesEvalsToResolve: args.MissingSeriesEvalsToResolve,
		KeepFiringFor:               func() strfmt.Duration { d := strfmt.Duration(keepFiringFor); return d }(),
		NotificationSettings:        notificationSettings,
		Record:                      record,
	}

	if err := rule.Validate(strfmt.Default); err != nil {
		return nil, fmt.Errorf("update alert rule: invalid rule configuration: %w", err)
	}

	params := provisioning.NewPutAlertRuleParams().WithContext(ctx).WithUID(args.UID).WithBody(rule)

	disableProvenance := true
	if args.DisableProvenance != nil {
		disableProvenance = *args.DisableProvenance
	}
	if disableProvenance {
		header := "true"
		params = params.WithXDisableProvenance(&header)
	}

	response, err := c.Provisioning.PutAlertRule(params)
	if err != nil {
		return nil, fmt.Errorf("update alert rule %s: %w", args.UID, err)
	}

	return response.Payload, nil
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
