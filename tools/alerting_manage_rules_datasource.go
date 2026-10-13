package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

func listDatasourceAlertRules(ctx context.Context, dsUID string, opts *GetRulesOpts, labelSelectors []Selector) ([]alertRuleSummary, error) {
	// Bound the entire scan, rather than giving every page a fresh timeout.
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: dsUID})
	if err != nil {
		return nil, fmt.Errorf("datasource %s: %w", dsUID, err)
	}

	if !isRulerDatasource(ds.Type) {
		return nil, fmt.Errorf("datasource %s (type: %s) does not support ruler API. Supported types: prometheus, loki", dsUID, ds.Type)
	}

	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating alerting client: %w", err)
	}

	limit := DefaultListAlertRulesLimit
	if opts != nil && opts.RuleLimit > 0 {
		limit = min(opts.RuleLimit, maxRulesLimit)
	}
	var summaries []alertRuleSummary
	nextToken := ""
	seenTokens := make(map[string]struct{})
	for {
		rulesResp, err := client.GetDatasourceRules(ctx, dsUID, opts, nextToken)
		if err != nil {
			return nil, fmt.Errorf("querying datasource %s rules: %w", dsUID, err)
		}
		page := convertPrometheusRulesToSummary(&rulesResp.RulesResult)
		// Keep local filtering for backends that ignore type, and preserve
		// substring name matching and the existing label selector semantics.
		if opts != nil && opts.RuleType != "" {
			page = filterSummaryByRuleType(page, opts.RuleType)
		}
		if opts != nil && opts.RuleName != "" {
			page = filterSummaryByRuleName(page, opts.RuleName)
		}
		if len(labelSelectors) > 0 {
			page, err = filterSummaryByLabels(page, labelSelectors)
			if err != nil {
				return nil, fmt.Errorf("filtering rules: %w", err)
			}
		}
		remaining := limit - len(summaries)
		summaries = append(summaries, page[:min(len(page), remaining)]...)
		if len(summaries) == limit || rulesResp.NextToken == "" {
			return summaries, nil
		}
		if _, seen := seenTokens[rulesResp.NextToken]; seen {
			return nil, fmt.Errorf("datasource %s rules API repeated a pagination token", dsUID)
		}
		seenTokens[rulesResp.NextToken] = struct{}{}
		nextToken = rulesResp.NextToken
	}
}

func isRulerDatasource(dsType string) bool {
	dsType = strings.ToLower(dsType)
	return strings.Contains(dsType, "prometheus") ||
		strings.Contains(dsType, "loki")
}

// convertPrometheusRulesToSummary flattens a ruler rules response into
// alertRuleSummary entries. Both alerting and recording rules are emitted —
// recording rules populate Query/Labels/Health/LastEvaluation but leave the
// alerting-only fields (state, for, annotations) empty.
func convertPrometheusRulesToSummary(result *v1.RulesResult) []alertRuleSummary {
	if result == nil {
		return nil
	}
	var rules []alertRuleSummary

	for _, group := range result.Groups {
		for _, rule := range group.Rules {
			switch r := rule.(type) {
			case v1.AlertingRule:
				rules = append(rules, alertRuleSummary{
					Title:          r.Name,
					Type:           "alerting",
					RuleGroup:      group.Name,
					Query:          r.Query,
					Labels:         labelMap(r.Labels),
					Annotations:    labelMap(r.Annotations),
					State:          normalizeState(string(r.State)),
					Health:         string(r.Health),
					LastEvaluation: formatRuleEvalTime(r.LastEvaluation),
					For:            formatDuration(r.Duration),
				})
			case v1.RecordingRule:
				rules = append(rules, alertRuleSummary{
					Title:          r.Name,
					Type:           "recording",
					RuleGroup:      group.Name,
					Query:          r.Query,
					Labels:         labelMap(r.Labels),
					Health:         string(r.Health),
					LastEvaluation: formatRuleEvalTime(r.LastEvaluation),
				})
			}
		}
	}

	return rules
}

// labelMap copies a v1 label set (model.LabelSet) into a plain string map.
// Returns nil for empty input so the JSON output omits the field.
func labelMap[K ~string, V ~string](in map[K]V) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[string(k)] = string(v)
	}
	return out
}

func formatRuleEvalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func formatDuration(seconds float64) string {
	if seconds == 0 {
		return ""
	}
	d := time.Duration(seconds * float64(time.Second))
	return d.String()
}
