package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

var promqlParser = parser.NewParser(parser.Options{})

var validAlertStates = map[string]bool{
	"firing": true, "pending": true, "normal": true,
	"recovering": true, "nodata": true, "error": true,
}

// unquotedLabelValueRe matches either a quoted string, which is left untouched so
// operators inside quoted values are never rewritten, or a label matcher operator
// followed by an unquoted value.
var unquotedLabelValueRe = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|(!=|!~|=~|=)\s*([^"'}\s,~][^},]*)`)

// quoteUnquotedLabelValues adds double quotes around unquoted label values in
// Prometheus-style selectors. LLMs frequently omit the required quotes, e.g.
// {severity=critical} instead of {severity="critical"}.
func quoteUnquotedLabelValues(s string) string {
	return unquotedLabelValueRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := unquotedLabelValueRe.FindStringSubmatch(match)
		if groups[1] == "" {
			return match
		}
		return groups[1] + `"` + strings.TrimSpace(groups[2]) + `"`
	})
}

type alertRuleSummary struct {
	UID            string            `json:"uid,omitempty"`
	Title          string            `json:"title"`
	Type           string            `json:"type,omitempty"`
	State          string            `json:"state,omitempty"`
	Health         string            `json:"health,omitempty"`
	FolderUID      string            `json:"folder_uid,omitempty"`
	RuleGroup      string            `json:"rule_group,omitempty"`
	For            string            `json:"for,omitempty"`
	LastEvaluation string            `json:"last_evaluation,omitempty"`
	LastError      string            `json:"last_error,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
	// Query is the rule expression. Populated for datasource-managed rules
	// (Prometheus / Mimir / Loki ruler responses); empty for Grafana-managed
	// rules where the expression is split across multiple AlertQuery nodes
	// and exposed via the 'get' operation instead.
	Query string `json:"query,omitempty"`
	// Namespace is the ruler namespace of a datasource-managed rule (the
	// rules API "file" field). Mimir and Loki identify a rule group by
	// namespace + group name, so group names alone can collide.
	Namespace string `json:"namespace,omitempty"`

	// Enrichment from the provisioning API for Grafana-managed rules, so a
	// list exposes paused state, the notification target and the queried
	// datasources without a per-rule 'get'. Left empty when provisioning config
	// is unavailable (e.g. the token lacks provisioning read) or for
	// datasource-managed rules.
	IsPaused             *bool                                 `json:"is_paused,omitempty"`
	NotificationSettings *models.AlertRuleNotificationSettings `json:"notification_settings,omitempty"`
	DatasourceUIDs       []string                              `json:"datasource_uids,omitempty"`
}

// alertRuleDetail is the enriched response for a single rule, combining
// full configuration from the Provisioning API with runtime state from the
// Prometheus rules API.
type alertRuleDetail struct {
	OrgID        int64             `json:"org_id,omitempty"`
	UID          string            `json:"uid"`
	Title        string            `json:"title"`
	FolderUID    string            `json:"folder_uid"`
	RuleGroup    string            `json:"rule_group"`
	Condition    string            `json:"condition,omitempty"`
	NoDataState  string            `json:"no_data_state,omitempty"`
	ExecErrState string            `json:"exec_err_state,omitempty"`
	For          string            `json:"for,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`

	IsPaused             bool                                  `json:"is_paused"`
	NotificationSettings *models.AlertRuleNotificationSettings `json:"notification_settings,omitempty"`
	Data                 []*models.AlertQuery                  `json:"data,omitempty"`

	KeepFiringFor               string  `json:"keep_firing_for,omitempty"`
	Record                      *Record `json:"record,omitempty" `
	MissingSeriesEvalsToResolve int64   `json:"missing_series_evals_to_resolve,omitempty"`

	State          string  `json:"state"`
	Health         string  `json:"health"`
	Type           string  `json:"type,omitempty"`
	LastEvaluation string  `json:"last_evaluation,omitempty"`
	LastError      string  `json:"last_error,omitempty"`
	Alerts         []alert `json:"alerts,omitempty"`
}

type RelativeTimeRange struct {
	From int64 `json:"from" jsonschema:"description=Seconds before eval time (e.g. 600 = 10m ago)"`
	To   int64 `json:"to" jsonschema:"description=Seconds before eval time (0 = now)"`
}

type AlertQuery struct {
	RefID             string             `json:"refId,omitempty" jsonschema:"description=Query identifier (e.g. 'A'). Auto-assigned from position if omitted."`
	DatasourceUID     string             `json:"datasourceUid" jsonschema:"required,description=Datasource UID (e.g. 'grafanacloud-prom'\\, 'grafanacloud-logs') for data queries\\, or '__expr__' for expressions that transform other queries (reduce\\, threshold\\, math)."`
	RelativeTimeRange *RelativeTimeRange `json:"relativeTimeRange,omitempty" jsonschema:"description=Time range relative to eval time. Defaults to {from:600\\,to:0} for non-expression queries."`
	QueryType         string             `json:"queryType,omitempty" jsonschema:"description=Optional datasource-specific query type (e.g. 'instant'\\, 'range'). Usually left empty."`
	Model             AlertQueryModel    `json:"model" jsonschema:"required,description=Query model. For data sources: set expr. For expressions: set type and expression."`
}

// AlertQueryModel represents the query model for an alert query.
// It has typed fields for common PromQL/expression properties, plus an Extra
// map that preserves all additional datasource-specific fields (e.g. Graphite
// "target", "datasource", "textEditor") that would otherwise be silently
// dropped during JSON round-tripping.
type AlertQueryModel struct {
	Expr string `json:"expr,omitempty" jsonschema:"description=Query expression (PromQL\\, LogQL\\, etc.)"`

	// Server-side expressions only (when datasourceUid is __expr__).
	Type       string           `json:"type,omitempty" jsonschema:"description=Expression type (only for __expr__ datasource): math\\, reduce\\, threshold\\, classic_conditions"`
	Expression string           `json:"expression,omitempty" jsonschema:"description=Expression ref or formula. For reduce/threshold: ref like 'A'. For math: '$A > 0'."`
	Reducer    string           `json:"reducer,omitempty" jsonschema:"description=Reducer for reduce expressions: last\\, mean\\, min\\, max\\, sum\\, count"`
	Conditions []AlertCondition `json:"conditions,omitempty" jsonschema:"description=Conditions for threshold or classic_conditions expressions"`

	// Extra captures all additional model fields not explicitly typed above
	// (e.g. Graphite "target", "datasource", "textEditor", "intervalMs",
	// "maxDataPoints", "refId", etc.). This ensures datasource-specific
	// fields survive JSON round-tripping through convertAlertQueries.
	Extra map[string]any `json:"-"`
}

// MarshalJSON implements custom JSON marshaling that merges typed fields with
// Extra overflow fields, ensuring datasource-specific properties are preserved.
func (m AlertQueryModel) MarshalJSON() ([]byte, error) {
	result := make(map[string]any)

	// Start with extra fields as the base
	for k, v := range m.Extra {
		result[k] = v
	}

	// Overlay typed fields (they take precedence over Extra)
	if m.Expr != "" {
		result["expr"] = m.Expr
	}
	if m.Type != "" {
		result["type"] = m.Type
	}
	if m.Expression != "" {
		result["expression"] = m.Expression
	}
	if m.Reducer != "" {
		result["reducer"] = m.Reducer
	}
	if len(m.Conditions) > 0 {
		result["conditions"] = m.Conditions
	}

	return json.Marshal(result)
}

// UnmarshalJSON implements custom JSON unmarshaling that populates typed fields
// and stores all remaining fields in Extra.
func (m *AlertQueryModel) UnmarshalJSON(data []byte) error {
	// Unmarshal everything into a generic map first
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Extract known typed fields. If a known key exists but has an unexpected
	// type (e.g. null, number, object instead of string), fall through to
	// Extra so the value is preserved in the round-trip rather than silently
	// dropped.
	knownConsumed := make(map[string]bool)

	if v, ok := raw["expr"].(string); ok {
		m.Expr = v
		knownConsumed["expr"] = true
	}
	if v, ok := raw["type"].(string); ok {
		m.Type = v
		knownConsumed["type"] = true
	}
	if v, ok := raw["expression"].(string); ok {
		m.Expression = v
		knownConsumed["expression"] = true
	}
	if v, ok := raw["reducer"].(string); ok {
		m.Reducer = v
		knownConsumed["reducer"] = true
	}
	if v, ok := raw["conditions"]; ok {
		condBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal conditions: %w", err)
		}
		if err := json.Unmarshal(condBytes, &m.Conditions); err != nil {
			return fmt.Errorf("unmarshal conditions: %w", err)
		}
		knownConsumed["conditions"] = true
	}

	// Store all remaining fields in Extra — including known keys whose
	// type assertion failed above, so no value is silently lost.
	m.Extra = make(map[string]any)
	for k, v := range raw {
		if !knownConsumed[k] {
			m.Extra[k] = v
		}
	}

	return nil
}

// AlertCondition represents a condition within a classic_conditions or threshold expression.
// It includes the full set of fields used by classic_conditions (evaluator, operator, query, reducer).
type AlertCondition struct {
	Evaluator ConditionEvaluator `json:"evaluator" jsonschema:"description=Threshold evaluator"`
	Operator  *ConditionOperator `json:"operator,omitempty" jsonschema:"description=Logical operator (and/or) for combining conditions"`
	Query     *ConditionQuery    `json:"query,omitempty" jsonschema:"description=Query reference for classic_conditions (contains params with RefID)"`
	Reducer   *ConditionReducer  `json:"reducer,omitempty" jsonschema:"description=Reducer for classic_conditions (avg\\, sum\\, min\\, max\\, count\\, last\\, median)"`
}

// ConditionOperator represents the logical operator in a classic_conditions condition.
type ConditionOperator struct {
	Type string `json:"type" jsonschema:"description=Logical operator type: and\\, or"`
}

// ConditionQuery represents the query reference in a classic_conditions condition.
type ConditionQuery struct {
	Params []string `json:"params" jsonschema:"description=Query parameters\\, typically contains the RefID of the referenced query (e.g. ['C'])"`
}

// ConditionReducer represents the reducer in a classic_conditions condition.
type ConditionReducer struct {
	Type   string   `json:"type" jsonschema:"description=Reducer type: avg\\, sum\\, min\\, max\\, count\\, last\\, median"`
	Params []string `json:"params,omitempty" jsonschema:"description=Optional reducer parameters"`
}

type ConditionEvaluator struct {
	Type   string    `json:"type" jsonschema:"required,description=Evaluator: gt\\, lt\\, within_range\\, outside_range\\, no_value"`
	Params []float64 `json:"params" jsonschema:"required,description=Threshold value(s)"`
}

// NotificationSettings defines how notifications for an alert should be handled and grouped.
type NotificationSettings struct {
	ActiveTimeIntervals []string `json:"activeTimeIntervals,omitempty" jsonschema:"description=Override active (non-muted) time intervals by name."`
	GroupBy             []string `json:"groupBy,omitempty" jsonschema:"description=Labels used to group alerts for notification batching."`
	GroupInterval       string   `json:"groupInterval,omitempty" jsonschema:"description=Wait time before sending notifications for updates to an existing group."`
	GroupWait           string   `json:"groupWait,omitempty" jsonschema:"description=Initial wait time before sending the first notification for a group."`
	MuteTimeIntervals   []string `json:"muteTimeIntervals,omitempty" jsonschema:"description=Time intervals during which notifications are muted."`
	Receiver            *string  `json:"receiver" jsonschema:"required,description=Receiver name for sending notifications."`
	RepeatInterval      string   `json:"repeatInterval,omitempty" jsonschema:"description=Interval before resending a notification for an ongoing alert."`
}

// Record contains the configuration for a recording rule.
type Record struct {
	From                *string `json:"from" jsonschema:"required,description=Reference ID of the query or expression used as input. Ex: A"`
	Metric              *string `json:"metric" jsonschema:"required,description=Name of the recorded metric to be created."`
	TargetDatasourceUID string  `json:"targetDatasourceUid,omitempty" jsonschema:"description=Datasource UID where the recorded metric will be written."`
}

func (r *Record) validate() error {
	if r.From == nil || *r.From == "" {
		return fmt.Errorf("record.from is required")
	}
	if r.Metric == nil || *r.Metric == "" {
		return fmt.Errorf("record.metric is required")
	}
	return nil
}

// indexToRefID converts a zero-based index to a letter-based representation,
// matching the Grafana UI logic.
// 0 -> "A", 1 -> "B", ..., 25 -> "Z", 26 -> "AA", 27 -> "AB", ...
func indexToRefID(i int) string {
	result := ""
	for {
		result = string(rune('A'+i%26)) + result
		i = i/26 - 1
		if i < 0 {
			break
		}
	}
	return result
}

// convertAlertQueries converts typed AlertQuery slice to the models.AlertQuery
// slice expected by the Grafana API. It auto-assigns RefIDs and default RelativeTimeRange.
func convertAlertQueries(queries []*AlertQuery) ([]*models.AlertQuery, error) {
	result := make([]*models.AlertQuery, 0, len(queries))
	for i, q := range queries {
		if q == nil {
			return nil, fmt.Errorf("query at index %d is nil", i)
		}
		refID := q.RefID
		if refID == "" {
			refID = indexToRefID(i)
		}

		var rtr *models.RelativeTimeRange
		if q.RelativeTimeRange != nil {
			rtr = &models.RelativeTimeRange{
				From: models.Duration(q.RelativeTimeRange.From),
				To:   models.Duration(q.RelativeTimeRange.To),
			}
		} else if q.DatasourceUID != "__expr__" {
			rtr = &models.RelativeTimeRange{
				From: 600,
				To:   0,
			}
		}

		modelBytes, err := json.Marshal(q.Model)
		if err != nil {
			return nil, fmt.Errorf("marshal model for query %s: %w", refID, err)
		}
		modelMap := make(map[string]any)
		if err := json.Unmarshal(modelBytes, &modelMap); err != nil {
			return nil, fmt.Errorf("unmarshal model for query %s: %w", refID, err)
		}

		result = append(result, &models.AlertQuery{
			RefID:             refID,
			DatasourceUID:     q.DatasourceUID,
			RelativeTimeRange: rtr,
			QueryType:         q.QueryType,
			Model:             modelMap,
		})
	}
	return result, nil
}

// convertNotificationSettings converts user input type NotificationSettings to grafana http Api expected models.AlertRuleNotificationSettings type
func convertNotificationSettings(settings *NotificationSettings) *models.AlertRuleNotificationSettings {
	if settings == nil {
		return nil
	}
	return &models.AlertRuleNotificationSettings{
		ActiveTimeIntervals: settings.ActiveTimeIntervals,
		GroupBy:             settings.GroupBy,
		GroupInterval:       settings.GroupInterval,
		GroupWait:           settings.GroupWait,
		MuteTimeIntervals:   settings.MuteTimeIntervals,
		Receiver:            settings.Receiver,
		RepeatInterval:      settings.RepeatInterval,
	}
}

// convertRecord converts the input Record to the models.Record type compatible with the Grafana HTTP API.
func convertRecord(record *Record) *models.Record {
	if record == nil {
		return nil
	}
	return &models.Record{
		From:                record.From,
		Metric:              record.Metric,
		TargetDatasourceUID: record.TargetDatasourceUID,
	}
}

type CreateAlertRuleParams struct {
	Title                       string                `json:"title" jsonschema:"required,description=The title of the alert rule"`
	RuleGroup                   string                `json:"ruleGroup" jsonschema:"required,description=The rule group name"`
	FolderUID                   string                `json:"folderUID" jsonschema:"required,description=The folder UID where the rule will be created"`
	Condition                   string                `json:"condition" jsonschema:"required,description=The query condition identifier (e.g. 'A'\\, 'B')"`
	Data                        []*AlertQuery         `json:"data" jsonschema:"required,description=Array of alert query objects. Example: [{datasourceUid: 'prometheus'\\, model: {expr: 'vector(1)'}}\\, {datasourceUid: '__expr__'\\, model: {type: 'threshold'\\, expression: 'A'\\, conditions: [{evaluator: {type: 'gt'\\, params: [1]}}]}}]. RefID and relativeTimeRange are auto-assigned if omitted."`
	NoDataState                 string                `json:"noDataState" jsonschema:"required,description=State when no data (NoData\\, Alerting\\, OK)"`
	ExecErrState                string                `json:"execErrState" jsonschema:"required,description=State on execution error (NoData\\, Alerting\\, OK)"`
	For                         string                `json:"for" jsonschema:"required,description=Duration before alert fires (e.g. '5m')"`
	KeepFiringFor               string                `json:"keepFiringFor,omitempty" jsonschema:"description=Enables continous firing of alert for specified time even when condition is no longer met. Default is 0 (resolves immediately)"`
	IsPaused                    bool                  `json:"isPaused,omitempty" jsonschema:"description=If true\\, the alert rule remains inactive\\, Default is false"`
	NotificationSettings        *NotificationSettings `json:"notificationSettings,omitempty" jsonschema:"description=Alert rule notification settings"`
	Record                      *Record               `json:"record,omitempty" jsonschema:"description=Settings for a recording type alert rule"`
	MissingSeriesEvalsToResolve int64                 `json:"missingSeriesEvalsToResolve,omitempty" jsonschema:"description=Consecutive evaluation intervals with no data required to mark the alert as resolved. Default is 2."`
	Annotations                 map[string]string     `json:"annotations,omitempty" jsonschema:"description=Optional annotations"`
	Labels                      map[string]string     `json:"labels,omitempty" jsonschema:"description=Optional labels"`
	UID                         *string               `json:"uid,omitempty" jsonschema:"description=Optional UID for the alert rule"`
	OrgID                       int64                 `json:"orgID" jsonschema:"required,description=The organization ID"`
	DisableProvenance           *bool                 `json:"disableProvenance,omitempty" jsonschema:"description=If true\\, the alert will remain editable in the Grafana UI (sets X-Disable-Provenance header). If false\\, the alert will be marked with provenance 'api' and locked from UI editing. Defaults to true."`
}

func (p CreateAlertRuleParams) validate() error {
	if p.Title == "" {
		return fmt.Errorf("title is required")
	}
	if p.RuleGroup == "" {
		return fmt.Errorf("rule_group is required")
	}
	if p.FolderUID == "" {
		return fmt.Errorf("folder_uid is required")
	}
	if p.Condition == "" {
		return fmt.Errorf("condition is required")
	}
	if p.Data == nil {
		return fmt.Errorf("data is required")
	}
	if p.NoDataState == "" {
		return fmt.Errorf("no_data_state is required")
	}
	if p.ExecErrState == "" {
		return fmt.Errorf("exec_err_state is required")
	}

	if p.Record != nil {
		if err := p.Record.validate(); err != nil {
			return err
		}
	}

	if p.For == "" {
		return fmt.Errorf("for duration is required")
	}
	if p.OrgID <= 0 {
		return fmt.Errorf("org_id is required and must be greater than 0")
	}
	return nil
}

// UpdateAlertRuleParams describes a partial update: only UID is required. Every
// other field left at its zero value is taken from the existing rule.
type UpdateAlertRuleParams struct {
	UID                         string                `json:"uid" jsonschema:"required,description=The UID of the alert rule to update"`
	Title                       string                `json:"title,omitempty" jsonschema:"description=The title of the alert rule. Omit to keep the current value"`
	RuleGroup                   string                `json:"ruleGroup,omitempty" jsonschema:"description=The rule group name. Omit to keep the current value"`
	FolderUID                   string                `json:"folderUID,omitempty" jsonschema:"description=The folder UID. Omit to keep the current value"`
	Condition                   string                `json:"condition,omitempty" jsonschema:"description=The query condition identifier (e.g. 'A'\\, 'B'). Omit to keep the current value"`
	Data                        []*AlertQuery         `json:"data,omitempty" jsonschema:"description=Array of alert query objects. RefID and relativeTimeRange are auto-assigned if omitted. Omit to keep the current queries"`
	NoDataState                 string                `json:"noDataState,omitempty" jsonschema:"description=State when no data (NoData\\, Alerting\\, OK). Omit to keep the current value"`
	ExecErrState                string                `json:"execErrState,omitempty" jsonschema:"description=State on execution error (NoData\\, Alerting\\, OK). Omit to keep the current value"`
	For                         string                `json:"for,omitempty" jsonschema:"description=Duration before alert fires (e.g. '5m'). Omit to keep the current value"`
	KeepFiringFor               string                `json:"keepFiringFor,omitempty" jsonschema:"description=Enables continous firing of alert for specified time even when condition is no longer met. Omit to keep the current value"`
	IsPaused                    *bool                 `json:"isPaused,omitempty" jsonschema:"description=If true\\, the alert rule remains inactive. Omit to keep the current state"`
	NotificationSettings        *NotificationSettings `json:"notificationSettings,omitempty" jsonschema:"description=Alert rule notification settings. Omit to keep the current settings"`
	Record                      *Record               `json:"record,omitempty" jsonschema:"description=Settings for a recording type alert rule. Omit to keep the current settings"`
	MissingSeriesEvalsToResolve *int64                `json:"missingSeriesEvalsToResolve,omitempty" jsonschema:"description=Consecutive evaluation intervals with no data required to mark the alert as resolved. Omit to keep the current value"`
	Annotations                 map[string]string     `json:"annotations,omitempty" jsonschema:"description=Optional annotations. Omit to keep the current annotations"`
	Labels                      map[string]string     `json:"labels,omitempty" jsonschema:"description=Optional labels. Omit to keep the current labels"`
	OrgID                       int64                 `json:"orgID,omitempty" jsonschema:"description=The organization ID. Omit to keep the current value"`
	DisableProvenance           *bool                 `json:"disableProvenance,omitempty" jsonschema:"description=If true\\, the alert will remain editable in the Grafana UI (sets X-Disable-Provenance header). If false\\, the alert will be marked with provenance 'api' and locked from UI editing. Defaults to true."`
}

// validate checks only what an update needs up front: the rule UID, plus the
// shape of any field that was supplied. Fields that were not supplied are taken
// from the existing rule in updateAlertRule, and the merged rule is validated
// there before it is sent.
func (p UpdateAlertRuleParams) validate() error {
	if p.UID == "" {
		return fmt.Errorf("rule_uid is required")
	}
	if p.Data != nil && len(p.Data) == 0 {
		return fmt.Errorf("data must contain at least one query when supplied; omit it to keep the current queries")
	}
	if p.Record != nil {
		if err := p.Record.validate(); err != nil {
			return err
		}
	}
	if p.OrgID < 0 {
		return fmt.Errorf("org_id must be greater than 0 when supplied")
	}
	return nil
}

type DeleteAlertRuleParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the alert rule to delete"`
}

func (p DeleteAlertRuleParams) validate() error {
	if p.UID == "" {
		return fmt.Errorf("uid is required")
	}
	return nil
}

// parseMatcherStrings parses Prometheus-style matcher strings (e.g. 'severity="critical"')
// into label matchers. Each string should be a single matcher like 'name="value"',
// 'name!="value"', 'name=~"regex"', or 'name!~"regex"'. Strings may optionally be
// wrapped in braces (e.g. '{severity="critical"}') — braces are stripped if present.
func parseMatcherStrings(strs []string) ([]*labels.Matcher, error) {
	if len(strs) == 0 {
		return nil, nil
	}
	var result []*labels.Matcher
	for _, s := range strs {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "{") {
			s = "{" + s + "}"
		}
		s = quoteUnquotedLabelValues(s)
		parsed, err := promqlParser.ParseMetricSelector(s)
		if err != nil {
			return nil, fmt.Errorf("invalid matcher %q: %w", s, err)
		}
		result = append(result, parsed...)
	}
	return result, nil
}

// parseSelectorStrings parses Prometheus-style selector strings (e.g. '{severity="critical", env!="dev"}')
// into Selector structs for client-side label filtering.
func parseSelectorStrings(strs []string) ([]Selector, error) {
	if len(strs) == 0 {
		return nil, nil
	}
	var result []Selector
	for _, s := range strs {
		s = strings.TrimSpace(s)
		parsed, err := parseSelector(s)
		if err != nil {
			return nil, fmt.Errorf("invalid label selector %q: %w", s, err)
		}
		var filters []LabelMatcher
		for _, m := range parsed {
			filters = append(filters, LabelMatcher{
				Name:  m.Name,
				Type:  m.Type.String(),
				Value: m.Value,
			})
		}
		result = append(result, Selector{Filters: filters})
	}
	return result, nil
}

func parseSelector(s string) ([]*labels.Matcher, error) {
	matchers, err := promqlParser.ParseMetricSelector(s)
	if err == nil {
		return matchers, nil
	}
	for _, candidate := range []string{s, "{" + s + "}"} {
		if repaired, repairErr := promqlParser.ParseMetricSelector(quoteUnquotedLabelValues(candidate)); repairErr == nil {
			return repaired, nil
		}
	}
	return nil, err
}

// buildGetRulesOpts validates shared list filter fields and constructs GetRulesOpts.
func buildGetRulesOpts(f listFilterParams, folderUID, ruleGroup string) (*GetRulesOpts, error) {
	if f.RuleLimit < 0 {
		return nil, fmt.Errorf("invalid rule_limit: %d, must be >= 0", f.RuleLimit)
	}
	if folderUID != "" && f.SearchFolder != "" {
		return nil, fmt.Errorf("folder_uid and search_folder are mutually exclusive")
	}
	for _, s := range f.States {
		if !validAlertStates[s] {
			return nil, fmt.Errorf("invalid state %q, must be one of: firing, pending, normal, recovering, nodata, error", s)
		}
	}
	matchers, err := parseMatcherStrings(f.Matchers)
	if err != nil {
		return nil, err
	}
	return &GetRulesOpts{
		FolderUID:    folderUID,
		SearchFolder: f.SearchFolder,
		RuleGroup:    ruleGroup,
		RuleName:     f.SearchRuleName,
		RuleType:     f.RuleType,
		States:       f.States,
		RuleLimit:    f.RuleLimit,
		LimitAlerts:  f.LimitAlerts,
		Matchers:     matchers,
	}, nil
}

// parseLabelSelectors converts string-based label selectors to typed Selectors.
func (f listFilterParams) parseLabelSelectors() ([]Selector, error) {
	return parseSelectorStrings(f.LabelSelectors)
}

// listFilterParams contains list operation filter fields shared between read and read-write param structs.
type listFilterParams struct {
	RuleLimit      int      `json:"rule_limit,omitempty" jsonschema:"default=200,description=Maximum number of rules to return (default 200\\, max 200). Requires Grafana 12.4+ (for 'list' operation)"`
	LabelSelectors []string `json:"label_selectors,omitempty" jsonschema:"description=Prometheus-style selectors to filter alert rules by labels. Each string is a selector e.g. '{severity=\"critical\"\\, team=~\"backend.*\"}'. All selectors must match (AND)."`
	LimitAlerts    int      `json:"limit_alerts,omitempty" jsonschema:"description=Number of alert instances to include per rule (0 or omitted = none\\, max 200)."`
	SearchFolder   string   `json:"search_folder,omitempty" jsonschema:"description=Search folders by path using partial matching (for 'list' operation). Requires Grafana 12.4+. Mutually exclusive with folder_uid."`
	SearchRuleName string   `json:"search_rule_name,omitempty" jsonschema:"description=Search alert rule names/titles using partial matching. Requires Grafana 12.4+ (for 'list' operation)"`
	States         []string `json:"states,omitempty" jsonschema:"description=Filter by alert state: firing\\, pending\\, normal\\, recovering\\, nodata\\, error (for 'list' operation)"`
	RuleType       string   `json:"rule_type,omitempty" jsonschema:"enum=alerting,enum=recording,description=Filter by rule type (for 'list' operation)"`
	Matchers       []string `json:"matchers,omitempty" jsonschema:"description=Label matchers to filter alert instances. Each string is a Prometheus-style matcher e.g. 'severity=\"critical\"'\\, 'env!=\"dev\"'\\, 'team=~\"backend.*\"'. Requires Grafana 12.4+."`
	Receiver       *string  `json:"receiver,omitempty" jsonschema:"description=Filter Grafana-managed rules by notification receiver/contact point name (for 'list' operation). Matches the per-rule notification_settings.receiver now included in list output."`
}

// ManageRulesReadParams is the param struct for alerting_rules_read.
type ManageRulesReadParams struct {
	listFilterParams

	Operation     string  `json:"operation" jsonschema:"required,enum=list,enum=get,enum=versions,description=The operation to perform: 'list' to search/filter rules\\, 'get' to retrieve full rule details (state + configuration) by UID\\, or 'versions' to get the version history of a rule"`
	RuleUID       string  `json:"rule_uid,omitempty" jsonschema:"description=The UID of the alert rule (required for 'get' and 'versions' operations)"`
	DatasourceUID *string `json:"datasource_uid,omitempty" jsonschema:"description=Optional: UID of a Prometheus or Loki datasource to query for datasource-managed alert rules. If omitted\\, returns Grafana-managed rules."`
	FolderUID     string  `json:"folder_uid,omitempty" jsonschema:"description=Filter by exact folder UID (for 'list' operation). Mutually exclusive with search_folder."`
	RuleGroup     string  `json:"rule_group,omitempty" jsonschema:"description=Filter by exact rule group name (for 'list' operation)"`
}

func (p ManageRulesReadParams) validate() error {
	switch p.Operation {
	case "list":
		_, err := buildGetRulesOpts(p.listFilterParams, p.FolderUID, p.RuleGroup)
		return err
	case "get":
		if p.RuleUID == "" {
			return fmt.Errorf("rule_uid is required for 'get' operation")
		}
		return nil
	case "versions":
		if p.RuleUID == "" {
			return fmt.Errorf("rule_uid is required for 'versions' operation")
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q, must be one of: list, get, versions", p.Operation)
	}
}

func (p ManageRulesReadParams) toGetRulesOpts() (*GetRulesOpts, error) {
	return buildGetRulesOpts(p.listFilterParams, p.FolderUID, p.RuleGroup)
}

// ManageRulesWriteParams is the param struct for alerting_rules_write.
type ManageRulesWriteParams struct {
	ManagedBy          string `json:"managed_by,omitempty" jsonschema:"enum=grafana,enum=datasource,default=grafana,description=Creation target. Omission defaults to Grafana-managed. Select datasource when requested or clearly established by conversation or environment context. Other mutations remain Grafana-only."`
	Namespace          string `json:"namespace,omitempty" jsonschema:"description=Namespace for datasource-managed creation. Required with managed_by=datasource."`
	Expression         string `json:"expression,omitempty" jsonschema:"description=PromQL or LogQL expression for datasource-managed creation."`
	EvaluationInterval string `json:"evaluation_interval,omitempty" jsonschema:"description=Interval for a new datasource rule group (default 1m). Must match an existing group's interval if supplied."`

	Operation                   string            `json:"operation" jsonschema:"required,enum=create,enum=update,enum=delete,description=The operation to perform: 'create'\\, 'update'\\, or 'delete'. To create a rule\\, use operation 'create' and provide all required fields in a single call. To update a rule\\, use 'update' with rule_uid and only the fields to change; omitted fields keep their current values."`
	RuleUID                     string            `json:"rule_uid,omitempty" jsonschema:"description=The UID of the alert rule (required for 'update' and 'delete'; optional for 'create')"`
	DatasourceUID               *string           `json:"datasource_uid,omitempty" jsonschema:"description=Datasource UID for managed_by=datasource creation. Omit to select the sole compatible datasource. Ambiguity returns candidates for selection."`
	Title                       string            `json:"title,omitempty" jsonschema:"description=The title of the alert rule (required for 'create')"`
	RuleGroup                   string            `json:"rule_group,omitempty" jsonschema:"description=The rule group name (required for 'create')"`
	FolderUID                   string            `json:"folder_uid,omitempty" jsonschema:"description=The folder UID. Required destination folder for Grafana-managed 'create'."`
	Condition                   string            `json:"condition,omitempty" jsonschema:"description=Grafana-managed only. The query condition identifier\\, e.g. 'A'\\, 'B' (required for 'create')"`
	Data                        []map[string]any  `json:"data,omitempty" jsonschema:"description=Grafana-managed only. Array of alert query objects (required for 'create'). Each object has: datasourceUid (string\\, required)\\, model (object with expr for data queries or type/expression/conditions for expressions)\\, refId (string\\, auto-assigned if omitted)\\, relativeTimeRange ({from\\, to} in seconds\\, defaults to {from:600\\,to:0}). For server-side expressions use datasourceUid '__expr__'. Example: [{datasourceUid: 'prometheus'\\, model: {expr: 'up == 0'}}\\, {datasourceUid: '__expr__'\\, model: {type: 'threshold'\\, expression: 'A'\\, conditions: [{evaluator: {type: 'gt'\\, params: [0]}}]}}]"`
	NoDataState                 string            `json:"no_data_state,omitempty" jsonschema:"description=Grafana-managed only. State when no data: NoData\\, Alerting\\, OK (required for 'create')"`
	ExecErrState                string            `json:"exec_err_state,omitempty" jsonschema:"description=Grafana-managed only. State on execution error: NoData\\, Alerting\\, OK (required for 'create')"`
	For                         string            `json:"for,omitempty" jsonschema:"description=Duration before alert fires\\, e.g. '5m'. Required for Grafana-managed create. Optional for datasource-managed rules (default 0s)."`
	KeepFiringFor               string            `json:"keep_firing_for,omitempty" jsonschema:"description=Enables continous firing of alert for specified time even when condition is no longer met. Default is 0 (resolves immediately)"`
	IsPaused                    *bool             `json:"is_paused,omitempty" jsonschema:"description=If true\\, the alert rule remains inactive. Default false on create"`
	NotificationSettings        map[string]any    `json:"notification_settings,omitempty" jsonschema:"description=Notification settings object. Fields: receiver (string\\, required)\\, groupBy ([]string)\\, groupWait/groupInterval/repeatInterval (duration strings)\\, muteTimeIntervals/activeTimeIntervals ([]string). snake_case keys are also accepted."`
	Record                      map[string]any    `json:"record,omitempty" jsonschema:"description=Recording rule config. Fields: from (string\\, required - ref ID e.g. 'A')\\, metric (string\\, required - metric name)\\, targetDatasourceUid (string\\, optional)."`
	MissingSeriesEvalsToResolve *int64            `json:"missing_series_evals_to_resolve,omitempty" jsonschema:"description=Consecutive evaluation intervals with no data required to mark the alert as resolved. Default is 2."`
	Annotations                 map[string]string `json:"annotations,omitempty" jsonschema:"description=Optional annotations for the alert rule"`
	Labels                      map[string]string `json:"labels,omitempty" jsonschema:"description=Optional labels for the alert rule"`
	OrgID                       int64             `json:"org_id,omitempty" jsonschema:"description=Grafana-managed organization ID (required for 'create')"`
	DisableProvenance           *bool             `json:"disable_provenance,omitempty" jsonschema:"description=If true\\, the alert remains editable in the Grafana UI (sets X-Disable-Provenance header). Defaults to true."`
}

func (p ManageRulesWriteParams) validate() error {
	if err := p.validateCreationTarget(); err != nil {
		return err
	}
	switch p.Operation {
	case "create":
		if p.ManagedBy == "datasource" {
			return p.validateDatasourceRule()
		}
		cp, err := p.toCreateParams()
		if err != nil {
			return err
		}
		return cp.validate()
	case "update":
		up, err := p.toUpdateParams()
		if err != nil {
			return err
		}
		return up.validate()
	case "delete":
		if p.RuleUID == "" {
			return fmt.Errorf("rule_uid is required for 'delete' operation")
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q, must be one of: create, update, delete", p.Operation)
	}
}

// unmarshalVia converts a map[string]any to a typed struct by round-tripping through JSON.
func unmarshalVia[T any](m map[string]any) (*T, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal map: %w", err)
	}
	var t T
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("unmarshal to %T: %w", t, err)
	}
	return &t, nil
}

// notificationSettingsSnakeKeys maps the snake_case keys that
// alerting_rules_read returns (Grafana's provisioning format) to the camelCase
// fields of NotificationSettings, so a rule read and written back keeps its
// grouping, timings and mute intervals.
var notificationSettingsSnakeKeys = map[string]string{
	"group_by":              "groupBy",
	"group_wait":            "groupWait",
	"group_interval":        "groupInterval",
	"repeat_interval":       "repeatInterval",
	"mute_time_intervals":   "muteTimeIntervals",
	"active_time_intervals": "activeTimeIntervals",
}

// notificationSettingsFromMap decodes notification_settings in either key
// style and rejects unknown keys, which plain json.Unmarshal would drop
// without a word. Two spellings of one key are compared case-insensitively,
// as encoding/json matches them, and a null counts as unset because
// alerting_rules_read returns null for empty lists.
func notificationSettingsFromMap(m map[string]any) (*NotificationSettings, error) {
	if m == nil {
		return nil, nil
	}
	type setting struct {
		sent  string
		key   string
		value any
	}
	byField := make(map[string]setting, len(m))
	for sent, value := range m {
		key := sent
		if camel, ok := notificationSettingsSnakeKeys[sent]; ok {
			key = camel
		}
		field := strings.ToLower(key)
		if prev, ok := byField[field]; ok {
			if prev.value != nil && value != nil {
				return nil, fmt.Errorf("set %s or %s, not both", min(prev.sent, sent), max(prev.sent, sent))
			}
			if value == nil {
				continue
			}
		}
		byField[field] = setting{sent: sent, key: key, value: value}
	}
	normalized := make(map[string]any, len(byField))
	for _, s := range byField {
		normalized[s.key] = s.value
	}
	b, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal map: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var ns NotificationSettings
	if err := dec.Decode(&ns); err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field") {
			return nil, fmt.Errorf("%w (valid keys: receiver, group_by, group_wait, group_interval, repeat_interval, mute_time_intervals, active_time_intervals, or their camelCase forms)", err)
		}
		return nil, err
	}
	return &ns, nil
}

// unmarshalDataToAlertQueries converts []map[string]any to []*AlertQuery.
func unmarshalDataToAlertQueries(data []map[string]any) ([]*AlertQuery, error) {
	if data == nil {
		return nil, nil
	}
	b, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}
	var queries []*AlertQuery
	if err := json.Unmarshal(b, &queries); err != nil {
		return nil, fmt.Errorf("unmarshal alert queries: %w", err)
	}
	return queries, nil
}

func (p ManageRulesWriteParams) toCreateParams() (CreateAlertRuleParams, error) {
	data, err := unmarshalDataToAlertQueries(p.Data)
	if err != nil {
		return CreateAlertRuleParams{}, fmt.Errorf("invalid data: %w", err)
	}
	ns, err := notificationSettingsFromMap(p.NotificationSettings)
	if err != nil {
		return CreateAlertRuleParams{}, fmt.Errorf("invalid notification_settings: %w", err)
	}
	rec, err := unmarshalVia[Record](p.Record)
	if err != nil {
		return CreateAlertRuleParams{}, fmt.Errorf("invalid record: %w", err)
	}
	isPaused := p.IsPaused != nil && *p.IsPaused
	missingEvals := int64(0)
	if p.MissingSeriesEvalsToResolve != nil {
		missingEvals = *p.MissingSeriesEvalsToResolve
	}
	params := CreateAlertRuleParams{
		Title:                       p.Title,
		RuleGroup:                   p.RuleGroup,
		FolderUID:                   p.FolderUID,
		Condition:                   p.Condition,
		Data:                        data,
		NoDataState:                 p.NoDataState,
		ExecErrState:                p.ExecErrState,
		For:                         p.For,
		Annotations:                 p.Annotations,
		Labels:                      p.Labels,
		OrgID:                       p.OrgID,
		DisableProvenance:           p.DisableProvenance,
		KeepFiringFor:               p.KeepFiringFor,
		IsPaused:                    isPaused,
		NotificationSettings:        ns,
		Record:                      rec,
		MissingSeriesEvalsToResolve: missingEvals,
	}
	if p.RuleUID != "" {
		params.UID = &p.RuleUID
	}
	return params, nil
}

func (p ManageRulesWriteParams) toUpdateParams() (UpdateAlertRuleParams, error) {
	data, err := unmarshalDataToAlertQueries(p.Data)
	if err != nil {
		return UpdateAlertRuleParams{}, fmt.Errorf("invalid data: %w", err)
	}
	ns, err := notificationSettingsFromMap(p.NotificationSettings)
	if err != nil {
		return UpdateAlertRuleParams{}, fmt.Errorf("invalid notification_settings: %w", err)
	}
	rec, err := unmarshalVia[Record](p.Record)
	if err != nil {
		return UpdateAlertRuleParams{}, fmt.Errorf("invalid record: %w", err)
	}
	return UpdateAlertRuleParams{
		UID:                         p.RuleUID,
		Title:                       p.Title,
		RuleGroup:                   p.RuleGroup,
		FolderUID:                   p.FolderUID,
		Condition:                   p.Condition,
		Data:                        data,
		NoDataState:                 p.NoDataState,
		ExecErrState:                p.ExecErrState,
		For:                         p.For,
		Annotations:                 p.Annotations,
		Labels:                      p.Labels,
		OrgID:                       p.OrgID,
		DisableProvenance:           p.DisableProvenance,
		KeepFiringFor:               p.KeepFiringFor,
		IsPaused:                    p.IsPaused,
		NotificationSettings:        ns,
		Record:                      rec,
		MissingSeriesEvalsToResolve: p.MissingSeriesEvalsToResolve,
	}, nil
}
