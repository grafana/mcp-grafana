package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	vizcontract "github.com/grafana/viz-contract"
)

// The chart registry of the viz contract (github.com/grafana/viz-contract, at the version in go.mod):
// registry/chart-types.json and registry/pick.json. The Source reads them and no other part does (tool.18). The summary
// of the frames and the matcher follow the condition rule language of schemas/registry.schema.json.

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
	mustReadVizRegistry("chart-types.json", &charts)
	mustReadVizRegistry("pick.json", &pick)
	for _, rule := range pick.Rules {
		if vizChartByName(charts.Charts, rule.Chart) == nil {
			panic(fmt.Sprintf("pick.json: rule %s has the unknown chart %s", rule.ID, rule.Chart))
		}
	}
	if len(pick.Rules) == 0 || len(pick.Rules[len(pick.Rules)-1].When) != 0 {
		panic("pick.json: the last rule must have an empty condition")
	}
	return charts.Charts, pick.Rules
}

func mustReadVizRegistry(name string, into any) {
	data, err := fs.ReadFile(vizcontract.Registry(), name)
	if err == nil {
		err = json.Unmarshal(data, into)
	}
	if err != nil {
		panic(fmt.Sprintf("%s: %v", name, err))
	}
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
				rows = jsonArrayEach(f.Data.Values[0], nil)
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
				if i < len(f.Data.Enums) && !isJSONNull(f.Data.Enums[i]) {
					typ = "string"
				}
				counts[typ]++
				s["hasFieldName."+strings.ToLower(field.Name)] = true
				if typ != "time" {
					continue
				}
				hasTime = true
				// isInstant needs only the first time and whether another time differs from it, so the scan stops at
				// the first difference.
				if i >= len(f.Data.Values) || !instant {
					continue
				}
				jsonArrayEach(f.Data.Values[i], func(elem []byte) bool {
					n, err := strconv.ParseFloat(string(elem), 64)
					if err != nil {
						return true // null, or not a number
					}
					if first == nil {
						first = &n
					} else if n != *first {
						instant = false
					}
					return instant
				})
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

// vizResult is the part of an entry of the results object that the summary and the notes read. The columns stay raw,
// so a large response is not decoded value by value.
type vizResult struct {
	Error  string `json:"error"`
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
			Values []json.RawMessage `json:"values"`
			Enums  []json.RawMessage `json:"enums"`
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

func isJSONNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// jsonArrayEach gives each element of a JSON array to fn, without decoding them, until fn returns false. It returns the
// number of elements it gave, so with a nil fn the length. raw is valid JSON (json.Unmarshal checked it); a value that
// is not an array has no elements.
func jsonArrayEach(raw json.RawMessage, fn func(elem []byte) bool) int {
	n, depth, start := 0, 0, -1
	inString, escaped := false, false
	emit := func(end int) bool {
		elem := bytes.TrimSpace(raw[start:end])
		start = -1
		if len(elem) == 0 {
			return true
		}
		n++
		return fn == nil || fn(elem)
	}
	for i, c := range raw {
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '[', '{':
			depth++
			if depth == 1 {
				if c != '[' {
					return 0
				}
				start = i + 1
			}
		case ']', '}':
			depth--
			if depth == 0 {
				emit(i)
				return n
			}
		case ',':
			if depth == 1 {
				if !emit(i) {
					return n
				}
				start = i + 1
			}
		}
	}
	return n
}

func vizFits(summary map[string]any, chart vizChart) bool {
	return slices.ContainsFunc(chart.Fits, func(c map[string]any) bool { return vizMatches(summary, c) })
}

// vizMatchingRule gives the first rule that matches the summary (tool.18). The last rule has an empty condition, so
// one always matches (mustLoadVizRegistry checks it).
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
