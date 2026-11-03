package tools

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The panel type of a query_datasource view comes from the shape of the
// returned frames, never from the model. app_datasource_pick.json is the rule
// table that ships with the vendored Grafana ECharts renderer
// (ui/mcp-apps/vendor). The first rule whose `when` matches the frame summary
// wins; the last rule has an empty `when` and always matches.

//go:embed app_datasource_pick.json
var vizPickJSON []byte

var vizPickTable = mustParseVizPickTable(vizPickJSON)

// vizPickFieldTypes are the field types of @grafana/data.
var vizPickFieldTypes = []string{"time", "number", "string", "boolean", "trace", "geo", "enum", "other", "frame", "nestedFrames"}

type vizPickRule struct {
	ID          string                     `json:"id"`
	Reason      string                     `json:"reason"`
	When        map[string]json.RawMessage `json:"when"`
	Type        string                     `json:"type"`
	Options     map[string]any             `json:"options"`
	FieldConfig map[string]any             `json:"fieldConfig"`
}

type vizPickRules struct {
	PickVersion int           `json:"pickVersion"`
	Rules       []vizPickRule `json:"rules"`
}

// vizPick names the rule that chose the panel, for the view and for tests.
type vizPick struct {
	Rule string `json:"rule"`
	Type string `json:"type"`
}

func mustParseVizPickTable(b []byte) *vizPickRules {
	table, err := parseVizPickTable(b)
	if err != nil {
		panic(err)
	}
	return table
}

func parseVizPickTable(b []byte) (*vizPickRules, error) {
	var table vizPickRules
	if err := json.Unmarshal(b, &table); err != nil {
		return nil, fmt.Errorf("panel pick table: %w", err)
	}
	if table.PickVersion != 1 {
		return nil, fmt.Errorf("panel pick table has pickVersion %d; only version 1 is supported", table.PickVersion)
	}
	if len(table.Rules) == 0 || len(table.Rules[len(table.Rules)-1].When) != 0 {
		return nil, errors.New("panel pick table: the last rule must have an empty when")
	}
	for _, r := range table.Rules {
		for key := range r.When {
			if err := validateVizPickKey(key); err != nil {
				return nil, fmt.Errorf("panel pick rule %s: %w", r.ID, err)
			}
		}
	}
	return &table, nil
}

func validateVizPickKey(key string) error {
	switch key {
	case "frameCount", "rowCountTotal", "rowCountMax", "fieldCount", "fieldCountMax", "isInstant":
		return nil
	}
	name, arg, ok := strings.Cut(key, ".")
	if ok && arg != "" {
		switch name {
		case "fieldCountByType", "hasFieldType":
			if slices.Contains(vizPickFieldTypes, arg) {
				return nil
			}
		case "hasDataFrameType", "hasPreferredVisualisationType":
			return nil
		}
	}
	return fmt.Errorf("the when key %q is not part of the frame summary", key)
}

type vizPickFrame struct {
	Schema struct {
		Fields []struct {
			Type string `json:"type"`
		} `json:"fields"`
		Meta struct {
			DataTopic                  string `json:"dataTopic"`
			Type                       string `json:"type"`
			PreferredVisualisationType string `json:"preferredVisualisationType"`
		} `json:"meta"`
	} `json:"schema"`
	Data struct {
		Values [][]any `json:"values"`
		Enums  []any   `json:"enums"`
	} `json:"data"`
}

// vizFrameSummary summarizes the frames of each query, in refIDs order.
// Annotation frames are skipped.
func vizFrameSummary(results json.RawMessage, refIDs []string) (map[string]any, error) {
	var byRef map[string]struct {
		Frames []vizPickFrame `json:"frames"`
	}
	if err := json.Unmarshal(results, &byRef); err != nil {
		return nil, fmt.Errorf("reading the query results for the panel pick: %w", err)
	}
	var frames []vizPickFrame
	for _, refID := range refIDs {
		for _, f := range byRef[refID].Frames {
			if f.Schema.Meta.DataTopic != "annotations" {
				frames = append(frames, f)
			}
		}
	}

	counts := map[string]int{}
	frameTypes := map[string]bool{}
	preferred := map[string]bool{}
	rowTotal, rowMax, fieldTotal, fieldMax := 0, 0, 0, 0
	hasTime, instant := false, true
	var first *float64
	for _, f := range frames {
		rows := 0
		if len(f.Data.Values) > 0 {
			rows = len(f.Data.Values[0])
		}
		rowTotal += rows
		rowMax = max(rowMax, rows)
		fieldTotal += len(f.Schema.Fields)
		fieldMax = max(fieldMax, len(f.Schema.Fields))
		if t := f.Schema.Meta.Type; t != "" {
			frameTypes[t] = true
		}
		if p := f.Schema.Meta.PreferredVisualisationType; p != "" {
			preferred[p] = true
		}
		for i, field := range f.Schema.Fields {
			typ := field.Type
			if i < len(f.Data.Enums) && f.Data.Enums[i] != nil {
				typ = "string"
			} else if typ == "" {
				typ = "other"
			}
			counts[typ]++
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

	summary := map[string]any{
		"frameCount":    float64(len(frames)),
		"rowCountTotal": float64(rowTotal),
		"rowCountMax":   float64(rowMax),
		"fieldCount":    float64(fieldTotal),
		"fieldCountMax": float64(fieldMax),
		"isInstant":     hasTime && instant,
	}
	for _, t := range vizPickFieldTypes {
		summary["fieldCountByType."+t] = float64(counts[t])
		summary["hasFieldType."+t] = counts[t] > 0
	}
	for t := range frameTypes {
		summary["hasDataFrameType."+t] = true
	}
	for t := range preferred {
		summary["hasPreferredVisualisationType."+t] = true
	}
	return summary, nil
}

// vizSummaryValue is the value of a summary key. A hasDataFrameType or
// hasPreferredVisualisationType key that no frame has is false.
func vizSummaryValue(summary map[string]any, key string) any {
	if v, ok := summary[key]; ok {
		return v
	}
	return false
}

// vizPickMatches matches one summary value: a list matches any member, an
// object is a {min, max} range, anything else must be equal.
func vizPickMatches(value any, raw json.RawMessage) (bool, error) {
	var want any
	if err := json.Unmarshal(raw, &want); err != nil {
		return false, err
	}
	switch w := want.(type) {
	case []any:
		return slices.Contains(w, value), nil
	case map[string]any:
		n, ok := value.(float64)
		if !ok {
			return false, nil
		}
		if lo, ok := w["min"].(float64); ok && n < lo {
			return false, nil
		}
		if hi, ok := w["max"].(float64); ok && n > hi {
			return false, nil
		}
		return true, nil
	default:
		return want == value, nil
	}
}

// runVizPick returns the first rule of the table that matches the frames.
func runVizPick(table *vizPickRules, results json.RawMessage, refIDs []string) (*vizPickRule, error) {
	summary, err := vizFrameSummary(results, refIDs)
	if err != nil {
		return nil, err
	}
	for i := range table.Rules {
		rule := &table.Rules[i]
		match := true
		for key, raw := range rule.When {
			ok, err := vizPickMatches(vizSummaryValue(summary, key), raw)
			if err != nil {
				return nil, fmt.Errorf("panel pick rule %s, key %s: %w", rule.ID, key, err)
			}
			if !ok {
				match = false
				break
			}
		}
		if match {
			return rule, nil
		}
	}
	return nil, errors.New("panel pick table: no rule matches")
}
