package tools

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// The chart registry of the viz contract (grafana/viz-contract): registry/chart-types.json and registry/pick.json.
// The Source reads them and no other part does (tool.18). The files are copies. viz_contract/CONTRACT pins the commit
// and the version, and scripts/sync-viz-contract.sh changes them. The summary of the frames and the matcher follow
// the condition rule language of schemas/registry.schema.json.

//go:embed viz_contract/chart-types.json
var chartTypesJSON []byte

//go:embed viz_contract/pick.json
var pickJSON []byte

type vizChart struct {
	Name        string           `json:"name"`
	Panel       string           `json:"panel"`
	Options     map[string]any   `json:"options"`
	Fits        []map[string]any `json:"fits"`
	Description string           `json:"description"`
	DataShape   string           `json:"dataShape"`
}

type vizPickRule struct {
	ID    string         `json:"id"`
	When  map[string]any `json:"when"`
	Chart string         `json:"chart"`
}

var vizCharts, vizPickRules = mustLoadVizRegistry()

func mustLoadVizRegistry() ([]vizChart, []vizPickRule) {
	var charts struct {
		Charts []vizChart `json:"charts"`
	}
	var pick struct {
		Rules []vizPickRule `json:"rules"`
	}
	if err := json.Unmarshal(chartTypesJSON, &charts); err != nil {
		panic(fmt.Sprintf("chart-types.json: %v", err))
	}
	if err := json.Unmarshal(pickJSON, &pick); err != nil {
		panic(fmt.Sprintf("pick.json: %v", err))
	}
	for _, rule := range pick.Rules {
		if vizChartByName(charts.Charts, rule.Chart) == nil {
			panic(fmt.Sprintf("pick.json: rule %s has the unknown chart %s", rule.ID, rule.Chart))
		}
	}
	return charts.Charts, pick.Rules
}

func vizChartByName(charts []vizChart, name string) *vizChart {
	for i := range charts {
		if charts[i].Name == name {
			return &charts[i]
		}
	}
	return nil
}

func vizChartNames() []string {
	names := make([]string, len(vizCharts))
	for i, c := range vizCharts {
		names[i] = c.Name
	}
	return names
}

// vizChartDescription lists what each chart needs, as the type argument of the contract does.
func vizChartDescription() string {
	parts := make([]string, len(vizCharts))
	for i, c := range vizCharts {
		parts[i] = fmt.Sprintf("%s (%s)", c.Name, c.DataShape)
	}
	return "Optional chart to draw. With no type, the chart comes from the data. Each chart needs: " + strings.Join(parts, ", ") + "."
}

var vizFieldTypes = []string{"time", "number", "string", "boolean", "trace", "geo", "enum", "other", "frame", "nestedFrames"}

// vizSummary is the summary of the frames of a results object (the response of /api/ds/query, keyed by refId).
// It skips the frames that have schema.meta.dataTopic annotations.
func vizSummary(results map[string]vizResult) map[string]any {
	s := map[string]any{
		"frameCount": 0, "rowCountTotal": 0, "rowCountMax": 0, "fieldCount": 0, "fieldCountMax": 0,
	}
	counts := map[string]int{}
	hasTime, instant := false, true
	var first *float64
	// The order of the entries does not change the summary, but keep it stable.
	refIDs := make([]string, 0, len(results))
	for refID := range results {
		refIDs = append(refIDs, refID)
	}
	slices.Sort(refIDs)
	for _, refID := range refIDs {
		for _, f := range results[refID].Frames {
			if f.Schema.Meta.DataTopic == "annotations" {
				continue
			}
			s["frameCount"] = s["frameCount"].(int) + 1
			rows := 0
			if len(f.Data.Values) > 0 {
				rows = len(f.Data.Values[0])
			}
			s["rowCountTotal"] = s["rowCountTotal"].(int) + rows
			s["rowCountMax"] = max(s["rowCountMax"].(int), rows)
			s["fieldCount"] = s["fieldCount"].(int) + len(f.Schema.Fields)
			s["fieldCountMax"] = max(s["fieldCountMax"].(int), len(f.Schema.Fields))
			if t := f.Schema.Meta.Type; t != "" {
				s["hasDataFrameType."+t] = true
			}
			if t := f.Schema.Meta.PreferredVisualisationType; t != "" {
				s["hasPreferredVisualisationType."+t] = true
			}
			for i, field := range f.Schema.Fields {
				typ := field.Type
				if typ == "" {
					typ = "other"
				}
				if i < len(f.Data.Enums) && f.Data.Enums[i] != nil {
					typ = "string"
				}
				counts[typ]++
				s["hasFieldName."+strings.ToLower(field.Name)] = true
				if typ != "time" {
					continue
				}
				hasTime = true
				if i >= len(f.Data.Values) {
					continue
				}
				for _, v := range f.Data.Values[i] {
					n, ok := v.(float64)
					if !ok {
						continue
					}
					if first == nil {
						first = &n
					} else if n != *first {
						instant = false
					}
				}
			}
		}
	}
	s["isInstant"] = hasTime && first != nil && instant
	for _, t := range vizFieldTypes {
		s["fieldCountByType."+t] = counts[t]
		s["hasFieldType."+t] = counts[t] > 0
	}
	return s
}

// vizResult is the part of an entry of the results object that the summary reads.
type vizResult struct {
	Frames []struct {
		Schema struct {
			Meta struct {
				Type                       string `json:"type"`
				PreferredVisualisationType string `json:"preferredVisualisationType"`
				DataTopic                  string `json:"dataTopic"`
			} `json:"meta"`
			Fields []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"fields"`
		} `json:"schema"`
		Data struct {
			Values [][]any `json:"values"`
			Enums  []any   `json:"enums"`
		} `json:"data"`
	} `json:"frames"`
}

// vizMatches reports whether each key of the condition matches the summary. A list matches when the value is a
// member. An object matches a number from min to max. Any other value matches when it is equal.
func vizMatches(summary map[string]any, condition map[string]any) bool {
	for key, want := range condition {
		value, ok := summary[key]
		if !ok {
			value = false
		}
		switch w := want.(type) {
		case []any:
			if !slices.ContainsFunc(w, func(x any) bool { return vizEqual(x, value) }) {
				return false
			}
		case map[string]any:
			n, isNumber := vizNumber(value)
			if !isNumber {
				return false
			}
			if lo, ok := vizNumber(w["min"]); ok && n < lo {
				return false
			}
			if hi, ok := vizNumber(w["max"]); ok && n > hi {
				return false
			}
		default:
			if !vizEqual(want, value) {
				return false
			}
		}
	}
	return true
}

func vizNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func vizEqual(a, b any) bool {
	if x, ok := vizNumber(a); ok {
		y, ok := vizNumber(b)
		return ok && x == y
	}
	return a == b
}

func vizFits(summary map[string]any, chart vizChart) bool {
	return slices.ContainsFunc(chart.Fits, func(c map[string]any) bool { return vizMatches(summary, c) })
}

// vizMatchingRule gives the first rule that matches the summary (tool.18). The last rule has an empty condition.
func vizMatchingRule(summary map[string]any) *vizPickRule {
	for i := range vizPickRules {
		if vizMatches(summary, vizPickRules[i].When) {
			return &vizPickRules[i]
		}
	}
	return nil
}

// vizPick gives the chart of the first rule that matches the summary.
func vizPick(summary map[string]any) *vizChart {
	if rule := vizMatchingRule(summary); rule != nil {
		return vizChartByName(vizCharts, rule.Chart)
	}
	return nil
}
