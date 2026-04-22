package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

func listDatasourceAlertRules(ctx context.Context, dsUID string, opts *GetRulesOpts, labelSelectors []Selector) ([]alertRuleSummary, error) {
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

	rulesResp, err := client.GetDatasourceRules(ctx, dsUID, opts)
	if err != nil {
		return nil, fmt.Errorf("querying datasource %s rules: %w", dsUID, err)
	}

	summaries := convertPrometheusRulesToSummary(rulesResp)

	if len(labelSelectors) > 0 {
		summaries, err = filterSummaryByLabels(summaries, labelSelectors)
		if err != nil {
			return nil, fmt.Errorf("filtering rules: %w", err)
		}
	}

	if opts != nil {
		summaries = applyRuleLimit(summaries, opts.RuleLimit)
	}

	return summaries, nil
}

func isRulerDatasource(dsType string) bool {
	dsType = strings.ToLower(dsType)
	return strings.Contains(dsType, "prometheus") ||
		strings.Contains(dsType, "loki")
}

func convertPrometheusRulesToSummary(result *v1.RulesResult) []alertRuleSummary {
	var rules []alertRuleSummary

	for _, group := range result.Groups {
		for _, rule := range group.Rules {
			switch r := rule.(type) {
			case v1.AlertingRule:
				lbls := make(map[string]string)
				for k, v := range r.Labels {
					lbls[string(k)] = string(v)
				}
				annots := make(map[string]string)
				for k, v := range r.Annotations {
					annots[string(k)] = string(v)
				}

				rules = append(rules, alertRuleSummary{
					Title:          r.Name,
					RuleGroup:      group.Name,
					Labels:         lbls,
					Annotations:    annots,
					State:          normalizeState(string(r.State)),
					Health:         string(r.Health),
					LastEvaluation: r.LastEvaluation.Format(time.RFC3339),
					For:            formatDuration(r.Duration),
				})
			case v1.RecordingRule:
				continue
			}
		}
	}

	return rules
}

func formatDuration(seconds float64) string {
	if seconds == 0 {
		return ""
	}
	d := time.Duration(seconds * float64(time.Second))
	return d.String()
}
