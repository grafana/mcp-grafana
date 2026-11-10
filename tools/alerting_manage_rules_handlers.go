package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/prometheus/prometheus/model/labels"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

func manageRulesRead(ctx context.Context, args ManageRulesReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("alerting_rules_read: %w", err)
	}

	switch args.Operation {
	case "list":
		opts, err := args.toGetRulesOpts()
		if err != nil {
			return nil, fmt.Errorf("alerting_rules_read: %w", err)
		}
		selectors, err := args.parseLabelSelectors()
		if err != nil {
			return nil, fmt.Errorf("alerting_rules_read: %w", err)
		}
		if args.DatasourceUID != nil && *args.DatasourceUID != "" {
			return listDatasourceAlertRules(ctx, *args.DatasourceUID, opts, selectors)
		}
		return listGrafanaRules(ctx, opts, selectors, args.Receiver)
	case "get":
		return getAlertRuleDetail(ctx, args.RuleUID, args.LimitAlerts)
	case "versions":
		return getAlertRuleVersions(ctx, args.RuleUID)
	default:
		return nil, fmt.Errorf("alerting_rules_read: unknown operation %q", args.Operation)
	}
}

func manageRulesWrite(ctx context.Context, args ManageRulesWriteParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("alerting_rules_write: %w", err)
	}

	switch args.Operation {
	case "create":
		if args.ManagedBy == "datasource" {
			return createDatasourceAlertRule(ctx, args)
		}
		cp, err := args.toCreateParams()
		if err != nil {
			return nil, fmt.Errorf("alerting_rules_write: %w", err)
		}
		return createAlertRule(ctx, cp)
	case "update":
		up, err := args.toUpdateParams()
		if err != nil {
			return nil, fmt.Errorf("alerting_rules_write: %w", err)
		}
		return updateAlertRule(ctx, up)
	case "delete":
		return deleteAlertRule(ctx, DeleteAlertRuleParams{
			UID: args.RuleUID,
		})
	default:
		return nil, fmt.Errorf("alerting_rules_write: unknown operation %q", args.Operation)
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

	opts := &GetRulesOpts{LimitAlerts: limitAlerts, IncludeInternalLabels: true}
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

	if provisioned.OrgID != nil {
		detail.OrgID = *provisioned.OrgID
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
			if rule.UID == uid || rule.Labels.Get(alertRuleUIDLabel) == uid {
				return &group.Rules[i]
			}
		}
	}
	return nil
}

func listGrafanaRules(ctx context.Context, opts *GetRulesOpts, labelSelectors []Selector, receiver *string) ([]alertRuleSummary, error) {
	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating alerting client: %w", err)
	}
	if opts == nil {
		opts = &GetRulesOpts{}
	}
	opts.IncludeInternalLabels = true

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

	// A1: enrich each rule with its provisioning config (paused state,
	// notification settings, queried datasources) using a single bulk
	// provisioning call, so callers no longer issue one 'get' per rule.
	summaries = enrichGrafanaManagedSummaries(ctx, summaries)

	// Receiver filter applies after enrichment (notification_settings is only
	// populated there) and before the limit so it is not truncated first.
	if receiver != nil && *receiver != "" {
		summaries = filterSummariesByReceiver(summaries, *receiver)
	}

	// Server-side rule_limit returns complete groups, so it may exceed the
	// requested limit. Enforce the limit client-side as well.
	summaries = applyRuleLimit(summaries, opts.RuleLimit)

	return summaries, nil
}

// enrichGrafanaManagedSummaries fills each summary's provisioning-derived
// fields (is_paused, notification_settings, datasource_uids) from a single bulk
// GET of all provisioned rules, joined by UID. Best-effort: on error (e.g. the
// token lacks provisioning read) the list is returned unenriched rather than
// failing, since the core list already succeeded.
func enrichGrafanaManagedSummaries(ctx context.Context, summaries []alertRuleSummary) []alertRuleSummary {
	if len(summaries) == 0 {
		return summaries
	}
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	if c == nil || c.Provisioning == nil {
		// No Grafana OpenAPI client in context (provisioning unavailable); the
		// core list already succeeded, so return it unenriched.
		return summaries
	}
	resp, err := c.Provisioning.GetAlertRulesWithParams(
		provisioning.NewGetAlertRulesParamsWithContext(ctx),
	)
	if err != nil {
		mcpgrafana.LoggerFromContext(ctx).WarnContext(ctx,
			"listing alert rules: could not enrich with provisioning config", "error", err)
		return summaries
	}
	byUID := make(map[string]*models.ProvisionedAlertRule, len(resp.Payload))
	for _, p := range resp.Payload {
		if p != nil && p.UID != "" {
			byUID[p.UID] = p
		}
	}
	for i := range summaries {
		p, ok := byUID[summaries[i].UID]
		if !ok {
			continue
		}
		paused := p.IsPaused
		summaries[i].IsPaused = &paused
		summaries[i].NotificationSettings = p.NotificationSettings
		summaries[i].DatasourceUIDs = datasourceUIDsFromQueries(p.Data)
	}
	return summaries
}

// datasourceUIDsFromQueries returns the distinct datasource UIDs a rule queries,
// skipping the server-side expression node (__expr__ / -100).
func datasourceUIDsFromQueries(queries []*models.AlertQuery) []string {
	seen := make(map[string]struct{})
	var uids []string
	for _, q := range queries {
		if q == nil || q.DatasourceUID == "" {
			continue
		}
		if q.DatasourceUID == "__expr__" || q.DatasourceUID == "-100" {
			continue
		}
		if _, dup := seen[q.DatasourceUID]; dup {
			continue
		}
		seen[q.DatasourceUID] = struct{}{}
		uids = append(uids, q.DatasourceUID)
	}
	return uids
}

// filterSummariesByReceiver keeps rules whose notification receiver matches name.
func filterSummariesByReceiver(summaries []alertRuleSummary, name string) []alertRuleSummary {
	filtered := make([]alertRuleSummary, 0, len(summaries))
	for _, s := range summaries {
		if s.NotificationSettings != nil &&
			s.NotificationSettings.Receiver != nil &&
			*s.NotificationSettings.Receiver == name {
			filtered = append(filtered, s)
		}
	}
	return filtered
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

// filterSummaryByRuleName keeps summaries whose title contains name as a
// case-insensitive substring, matching the partial-match semantics of the
// search.rule_name parameter on the Grafana-managed rules API.
func filterSummaryByRuleName(summaries []alertRuleSummary, name string) []alertRuleSummary {
	needle := strings.ToLower(name)
	filtered := make([]alertRuleSummary, 0, len(summaries))
	for _, s := range summaries {
		if strings.Contains(strings.ToLower(s.Title), needle) {
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
			labels := publicRuleLabels(rule.Labels)
			uid := rule.UID
			if uid == "" {
				uid = rule.Labels.Get(alertRuleUIDLabel)
			}
			folderUID := group.FolderUID
			if folderUID == "" {
				folderUID = rule.FolderUID
			}
			if folderUID == "" {
				folderUID = rule.Labels.Get(alertRuleNamespaceUIDLabel)
			}
			summary := alertRuleSummary{
				UID:       uid,
				Title:     rule.Name,
				State:     normalizeState(rule.State),
				Health:    rule.Health,
				FolderUID: folderUID,
				RuleGroup: group.Name,
			}
			if rule.Duration > 0 {
				summary.For = formatDuration(rule.Duration)
			}
			if !rule.LastEvaluation.IsZero() {
				summary.LastEvaluation = rule.LastEvaluation.Format(time.RFC3339)
			}
			if len(labels) > 0 {
				summary.Labels = labels
			}
			if !rule.Annotations.IsEmpty() {
				summary.Annotations = rule.Annotations.Map()
			}
			result = append(result, summary)
		}
	}
	return result
}

func publicRuleLabels(lbls labels.Labels) map[string]string {
	if lbls.IsEmpty() {
		return nil
	}
	result := lbls.Map()
	delete(result, alertRuleUIDLabel)
	delete(result, alertRuleNamespaceUIDLabel)
	if len(result) == 0 {
		return nil
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

	// GF-03: the provisioning PUT replaces the whole rule, so any optional field
	// the caller omits would be reset. Fetch the existing rule first and keep the
	// optional fields that were not supplied, so a partial update (e.g. pausing a
	// rule or changing one label) does not silently clear labels, annotations,
	// notification settings, keepFiringFor or the paused state.
	existing, err := c.Provisioning.GetAlertRuleWithParams(
		provisioning.NewGetAlertRuleParamsWithContext(ctx).WithUID(args.UID),
	)
	if err != nil {
		return nil, fmt.Errorf("update alert rule: fetch existing %s: %w", args.UID, err)
	}
	rule := existing.Payload

	// Required fields always overlay.
	rule.UID = args.UID
	rule.Title = &args.Title
	rule.RuleGroup = &args.RuleGroup
	rule.FolderUID = &args.FolderUID
	rule.Condition = &args.Condition
	rule.Data = convertedData
	rule.NoDataState = &args.NoDataState
	rule.ExecErrState = &args.ExecErrState
	rule.For = func() *strfmt.Duration { d := strfmt.Duration(duration); return &d }()
	rule.OrgID = &args.OrgID

	// Optional fields overlay only when supplied; otherwise the stored value stays.
	if args.Labels != nil {
		rule.Labels = args.Labels
	}
	if args.Annotations != nil {
		rule.Annotations = args.Annotations
	}
	if args.IsPaused != nil {
		rule.IsPaused = *args.IsPaused
	}
	if args.KeepFiringFor != "" {
		keepFiringFor, err := time.ParseDuration(args.KeepFiringFor)
		if err != nil {
			return nil, fmt.Errorf("update alert rule: invalid duration format for parameter keepFiringFor: %q: %w", args.KeepFiringFor, err)
		}
		rule.KeepFiringFor = strfmt.Duration(keepFiringFor)
	}
	if args.NotificationSettings != nil {
		rule.NotificationSettings = convertNotificationSettings(args.NotificationSettings)
	}
	if args.Record != nil {
		rule.Record = convertRecord(args.Record)
	}
	if args.MissingSeriesEvalsToResolve != nil {
		rule.MissingSeriesEvalsToResolve = *args.MissingSeriesEvalsToResolve
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
