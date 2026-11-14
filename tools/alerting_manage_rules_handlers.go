package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
			return listDatasourceAlertRules(ctx, *args.DatasourceUID, opts, selectors, args.Cursor)
		}
		return listGrafanaRules(ctx, opts, selectors, args.Receiver, args.Cursor)
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

func listGrafanaRules(ctx context.Context, opts *GetRulesOpts, labelSelectors []Selector, receiver *string, cursor string) (*alertRuleList, error) {
	// Bound the entire scan, rather than giving every page a fresh timeout.
	ctx, cancel := context.WithTimeout(ctx, mcpgrafana.GrafanaConfigFromContext(ctx).TimeoutOr(mcpgrafana.DefaultGrafanaClientTimeout))
	defer cancel()
	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating alerting client: %w", err)
	}
	if opts == nil {
		opts = &GetRulesOpts{}
	}
	limit := ruleLimitOrDefault(opts)
	pageOpts := *opts
	pageOpts.IncludeInternalLabels = true
	// Grafana counts rule_limit before our label filter, so it only sizes a
	// page; collectRules keeps paging until the limit is met after filtering.
	pageOpts.RuleLimit = limit

	fetch := func(ctx context.Context, token string) ([]alertRuleSummary, string, error) {
		pageOpts.GroupNextToken = token
		resp, err := client.GetRules(ctx, &pageOpts)
		if err != nil {
			return nil, "", fmt.Errorf("listing alert rules: %w", err)
		}
		page := convertRulesResponseToSummary(resp)
		if len(labelSelectors) > 0 {
			page, err = filterSummaryByLabels(page, labelSelectors)
			if err != nil {
				return nil, "", fmt.Errorf("filtering alert rules: %w", err)
			}
		}
		return page, resp.Data.NextToken, nil
	}
	list, err := collectRules(ctx, fetch, limit, cursor)
	if err != nil {
		return nil, err
	}

	// Enrich each rule with its provisioning config (paused state,
	// notification settings, queried datasources) using a single bulk
	// provisioning call, so callers no longer issue one 'get' per rule.
	// Enrichment is best-effort for a plain list: the core list already
	// succeeded, so it is returned unenriched when provisioning is unavailable.
	var enrichErr error
	list.Rules, enrichErr = enrichGrafanaManagedSummaries(ctx, list.Rules)
	if enrichErr != nil {
		mcpgrafana.LoggerFromContext(ctx).WarnContext(ctx,
			"listing alert rules: could not enrich with provisioning config", "error", enrichErr)
	}

	// Receiver filter applies after enrichment (notification_settings is only
	// populated there). It cannot be answered without the provisioning config:
	// filtering unenriched rules would match nothing and read as "no rule uses
	// this receiver".
	if receiver != nil && *receiver != "" {
		if enrichErr != nil {
			return nil, fmt.Errorf("listing alert rules: cannot filter by receiver %q because the rules' notification settings could not be read: %w", *receiver, enrichErr)
		}
		list.Rules = filterSummariesByReceiver(list.Rules, *receiver)
	}

	return list, nil
}

// rulePageFetcher returns one filtered page of rules starting at token, and
// the token of the next page ("" on the last page).
type rulePageFetcher func(ctx context.Context, token string) ([]alertRuleSummary, string, error)

// ruleCursor is the decoded next_cursor: a page token plus how many filtered
// rules of that page were already returned.
type ruleCursor struct {
	Token string `json:"t,omitempty"`
	Skip  int    `json:"s,omitempty"`
}

func (c ruleCursor) encode() string {
	b, _ := json.Marshal(c) // a string and an int cannot fail to marshal
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeRuleCursor(s string) (ruleCursor, error) {
	var c ruleCursor
	if s == "" {
		return c, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if err != nil || c.Skip < 0 {
		return ruleCursor{}, fmt.Errorf("invalid cursor %q: pass next_cursor from a previous list response", s)
	}
	return c, nil
}

// collectRules pages through fetch until limit rules are collected or the
// pages run out. A page cut mid-way resumes at the same page with a skip.
func collectRules(ctx context.Context, fetch rulePageFetcher, limit int, cursor string) (*alertRuleList, error) {
	at, err := decodeRuleCursor(cursor)
	if err != nil {
		return nil, err
	}
	list := &alertRuleList{Rules: []alertRuleSummary{}}
	seenTokens := make(map[string]struct{})
	for {
		page, next, err := fetch(ctx, at.Token)
		if err != nil {
			return nil, err
		}
		page = page[min(at.Skip, len(page)):]
		room := limit - len(list.Rules)
		if len(page) > room {
			list.Rules = append(list.Rules, page[:room]...)
			list.Truncated = true
			list.NextCursor = ruleCursor{Token: at.Token, Skip: at.Skip + room}.encode()
			return list, nil
		}
		list.Rules = append(list.Rules, page...)
		if next == "" {
			return list, nil
		}
		if len(list.Rules) == limit {
			list.Truncated = true
			list.NextCursor = ruleCursor{Token: next}.encode()
			return list, nil
		}
		if _, seen := seenTokens[next]; seen {
			return nil, fmt.Errorf("rules API repeated a pagination token")
		}
		seenTokens[next] = struct{}{}
		at = ruleCursor{Token: next}
	}
}

// enrichGrafanaManagedSummaries fills each summary's provisioning-derived
// fields (is_paused, notification_settings, datasource_uids) from a single bulk
// GET of all provisioned rules, joined by UID. When the provisioning config
// cannot be read (e.g. the token lacks provisioning read) it returns the
// summaries unenriched together with the error, and the caller decides whether
// the unenriched list is still a valid answer.
func enrichGrafanaManagedSummaries(ctx context.Context, summaries []alertRuleSummary) ([]alertRuleSummary, error) {
	if len(summaries) == 0 {
		return summaries, nil
	}
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	if c == nil || c.GrafanaHTTPAPI == nil || c.Provisioning == nil {
		return summaries, fmt.Errorf("no Grafana provisioning client available")
	}
	resp, err := c.Provisioning.GetAlertRulesWithParams(
		provisioning.NewGetAlertRulesParamsWithContext(ctx),
	)
	if err != nil {
		return summaries, fmt.Errorf("get provisioned alert rules: %w", err)
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
	return summaries, nil
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

// ruleLimitOrDefault returns the requested rule limit, defaulted and capped.
func ruleLimitOrDefault(opts *GetRulesOpts) int {
	if opts == nil || opts.RuleLimit <= 0 {
		return DefaultListAlertRulesLimit
	}
	return min(opts.RuleLimit, maxRulesLimit)
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

	// The provisioning PUT replaces the whole rule, so any field the caller omits
	// would be reset. Fetch the existing rule first and overlay only the fields
	// that were supplied, so a partial update (e.g. pausing a rule or changing
	// one label) leaves everything else exactly as stored.
	existing, err := c.Provisioning.GetAlertRuleWithParams(
		provisioning.NewGetAlertRuleParamsWithContext(ctx).WithUID(args.UID),
	)
	if err != nil {
		return nil, fmt.Errorf("update alert rule: fetch existing %s: %w", args.UID, err)
	}
	rule := existing.Payload
	if rule == nil {
		return nil, fmt.Errorf("update alert rule: fetch existing %s: empty response", args.UID)
	}
	if err := applyAlertRuleUpdate(rule, args); err != nil {
		return nil, fmt.Errorf("update alert rule: %w", err)
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

// applyAlertRuleUpdate overlays the fields supplied in args onto rule, which is
// the rule as currently stored. A field left at its zero value in args was not
// supplied and keeps the stored value.
func applyAlertRuleUpdate(rule *models.ProvisionedAlertRule, args UpdateAlertRuleParams) error {
	rule.UID = args.UID
	if args.Title != "" {
		rule.Title = &args.Title
	}
	if args.RuleGroup != "" {
		rule.RuleGroup = &args.RuleGroup
	}
	if args.FolderUID != "" {
		rule.FolderUID = &args.FolderUID
	}
	if args.Condition != "" {
		rule.Condition = &args.Condition
	}
	if args.Data != nil {
		convertedData, err := convertAlertQueries(args.Data)
		if err != nil {
			return err
		}
		rule.Data = convertedData
	}
	if args.NoDataState != "" {
		rule.NoDataState = &args.NoDataState
	}
	if args.ExecErrState != "" {
		rule.ExecErrState = &args.ExecErrState
	}
	if args.For != "" {
		duration, err := time.ParseDuration(args.For)
		if err != nil {
			return fmt.Errorf("invalid duration format for parameter for: %q: %w", args.For, err)
		}
		d := strfmt.Duration(duration)
		rule.For = &d
	}
	if args.OrgID > 0 {
		rule.OrgID = &args.OrgID
	}
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
			return fmt.Errorf("invalid duration format for parameter keepFiringFor: %q: %w", args.KeepFiringFor, err)
		}
		rule.KeepFiringFor = strfmt.Duration(keepFiringFor)
	}
	if args.NotificationSettings != nil {
		rule.NotificationSettings = mergeNotificationSettings(rule.NotificationSettings, args.NotificationSettings)
	}
	if args.Record != nil {
		rule.Record = mergeRecord(rule.Record, args.Record)
	}
	if args.MissingSeriesEvalsToResolve != nil {
		rule.MissingSeriesEvalsToResolve = *args.MissingSeriesEvalsToResolve
	}

	// Grafana ignores noDataState and execErrState on recording rules and returns
	// them empty, which the client-side enum validation rejects. Send the same
	// placeholder a full update has to pass for these ignored fields.
	if rule.Record != nil {
		if rule.NoDataState == nil || *rule.NoDataState == "" {
			noData := "OK"
			rule.NoDataState = &noData
		}
		if rule.ExecErrState == nil || *rule.ExecErrState == "" {
			execErr := "OK"
			rule.ExecErrState = &execErr
		}
	}
	return nil
}

// mergeNotificationSettings overlays the supplied notification settings onto
// the stored ones by the same rule as the top level: a field left at its zero
// value was not supplied and keeps the stored value. An empty list is a
// supplied value and clears the stored list. Replacing the object as a whole
// would silently drop grouping, timing and mute intervals when a caller changes
// only the receiver.
func mergeNotificationSettings(stored *models.AlertRuleNotificationSettings, in *NotificationSettings) *models.AlertRuleNotificationSettings {
	if stored == nil {
		if in.Receiver == nil && in.GroupBy == nil && in.GroupWait == "" && in.GroupInterval == "" &&
			in.RepeatInterval == "" && in.MuteTimeIntervals == nil && in.ActiveTimeIntervals == nil {
			return nil
		}
		return convertNotificationSettings(in)
	}
	merged := *stored
	if in.Receiver != nil {
		merged.Receiver = in.Receiver
	}
	if in.GroupBy != nil {
		merged.GroupBy = in.GroupBy
	}
	if in.GroupWait != "" {
		merged.GroupWait = in.GroupWait
	}
	if in.GroupInterval != "" {
		merged.GroupInterval = in.GroupInterval
	}
	if in.RepeatInterval != "" {
		merged.RepeatInterval = in.RepeatInterval
	}
	if in.MuteTimeIntervals != nil {
		merged.MuteTimeIntervals = in.MuteTimeIntervals
	}
	if in.ActiveTimeIntervals != nil {
		merged.ActiveTimeIntervals = in.ActiveTimeIntervals
	}
	return &merged
}

// mergeRecord overlays the supplied recording-rule settings onto the stored
// ones; fields that were not supplied keep their stored values.
func mergeRecord(stored *models.Record, in *Record) *models.Record {
	if stored == nil {
		if in.From == nil && in.Metric == nil && in.TargetDatasourceUID == "" {
			return nil
		}
		return convertRecord(in)
	}
	merged := *stored
	if in.From != nil {
		merged.From = in.From
	}
	if in.Metric != nil {
		merged.Metric = in.Metric
	}
	if in.TargetDatasourceUID != "" {
		merged.TargetDatasourceUID = in.TargetDatasourceUID
	}
	return &merged
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
