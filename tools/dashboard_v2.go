package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/grafana/grafana-openapi-client-go/models"
)

// This file holds helpers for the dashboard schema v2 (dashboard.grafana.app
// v2beta1), whose spec differs fundamentally from classic v1:
//   - panels live under `elements` (a map keyed by element name), each
//     {kind: "Panel"|"LibraryPanel", spec: {...}}; `layout` only references them.
//   - a panel's queries are at panel.spec.data.spec.queries[].spec.query, where
//     the datasource type is `group` and the uid is `datasource.name`.
//   - template variables live under `variables` (an array of {kind, spec}).
//   - the time range lives under `timeSettings` (from/to).

// v2Element is a single entry from a v2 dashboard's `elements` map.
type v2Element struct {
	Name string
	Kind string
	Spec map[string]interface{}
}

// collectElementsV2 returns all entries from a v2 dashboard's `elements` map,
// sorted by panel id (then name) for stable output.
func collectElementsV2(spec map[string]interface{}) []v2Element {
	elements := safeObject(spec, "elements")
	if elements == nil {
		return nil
	}

	out := make([]v2Element, 0, len(elements))
	for name, raw := range elements {
		el, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		inner := safeObject(el, "spec")
		if inner == nil {
			continue
		}
		out = append(out, v2Element{
			Name: name,
			Kind: safeString(el, "kind"),
			Spec: inner,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		idi, idj := safeInt(out[i].Spec, "id"), safeInt(out[j].Spec, "id")
		if idi != idj {
			return idi < idj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// collectAllPanelsV2 returns the inner spec of every Panel element (excluding
// library panels, which carry no inline queries), for query extraction.
func collectAllPanelsV2(spec map[string]interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	for _, el := range collectElementsV2(spec) {
		if el.Kind == "Panel" {
			out = append(out, el.Spec)
		}
	}
	return out
}

// findPanelByIDV2 returns the spec of the Panel element with the given id.
func findPanelByIDV2(spec map[string]interface{}, panelID int) (map[string]interface{}, error) {
	for _, el := range collectElementsV2(spec) {
		if el.Kind == "Panel" && safeInt(el.Spec, "id") == panelID {
			return el.Spec, nil
		}
	}
	return nil, fmt.Errorf("panel with ID %d not found", panelID)
}

// getPanelQueriesV2 implements get_dashboard_panel_queries for v2 dashboards.
func getPanelQueriesV2(spec map[string]interface{}, args DashboardPanelQueriesParams) ([]panelQuery, error) {
	var dashboardVars map[string]VariableInfo
	if args.Variables != nil {
		dashboardVars = extractDashboardVariablesV2(spec)
	}

	var panels []map[string]interface{}
	if args.PanelID != nil {
		panel, err := findPanelByIDV2(spec, *args.PanelID)
		if err != nil {
			return nil, err
		}
		panels = []map[string]interface{}{panel}
	} else {
		panels = collectAllPanelsV2(spec)
	}

	result := make([]panelQuery, 0)
	for _, panel := range panels {
		result = append(result, extractPanelQueriesV2(panel, dashboardVars, args.Variables)...)
	}
	return result, nil
}

// extractPanelQueriesV2 extracts all queries from a v2 panel spec. The query
// string is read from the free-form per-query body (query.spec), reusing the
// same field-probe as v1; the datasource type is the query `group` and the uid
// is `datasource.name`.
func extractPanelQueriesV2(panel map[string]interface{}, dashboardVars map[string]VariableInfo, overrides map[string]string) []panelQuery {
	var queries []panelQuery

	title := safeString(panel, "title")
	data := safeObject(panel, "data")
	if data == nil {
		return queries
	}
	dataSpec := safeObject(data, "spec")
	if dataSpec == nil {
		return queries
	}

	for _, q := range safeArray(dataSpec, "queries") {
		pq, ok := q.(map[string]interface{})
		if !ok {
			continue
		}
		pqSpec := safeObject(pq, "spec")
		if pqSpec == nil {
			continue
		}

		query := safeObject(pqSpec, "query")
		if query == nil {
			continue
		}

		dsInfo := datasourceInfo{Type: safeString(query, "group")}
		if dsRef := safeObject(query, "datasource"); dsRef != nil {
			dsInfo.UID = safeString(dsRef, "name")
		}

		rawQuery := ""
		if body := safeObject(query, "spec"); body != nil {
			rawQuery = extractQueryExpression(body)
		}

		result := panelQuery{
			Title:      title,
			Query:      rawQuery,
			Datasource: dsInfo,
			RefID:      safeString(pqSpec, "refId"),
		}

		if dashboardVars != nil {
			result.RequiredVariables = findVariablesInQuery(rawQuery, dashboardVars, overrides)
			effectiveVars := buildEffectiveVariables(dashboardVars, overrides)
			result.ProcessedQuery = substituteVariables(rawQuery, effectiveVars)
			dsInfo.UID = substituteVariables(dsInfo.UID, effectiveVars)
			result.Datasource = dsInfo
		}

		if rawQuery != "" {
			queries = append(queries, result)
		}
	}

	return queries
}

// extractDashboardVariablesV2 extracts variable definitions from a v2
// dashboard's `variables` array.
func extractDashboardVariablesV2(spec map[string]interface{}) map[string]VariableInfo {
	variables := make(map[string]VariableInfo)

	for _, v := range safeArray(spec, "variables") {
		vk, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		vspec := safeObject(vk, "spec")
		if vspec == nil {
			continue
		}
		name := safeString(vspec, "name")
		if name == "" {
			continue
		}

		info := VariableInfo{Name: name}

		if current := safeObject(vspec, "current"); current != nil {
			if val, ok := current["value"]; ok {
				switch t := val.(type) {
				case string:
					info.CurrentValue = t
				case []interface{}:
					strs := make([]string, 0, len(t))
					for _, item := range t {
						if s, ok := item.(string); ok {
							strs = append(strs, s)
						}
					}
					info.CurrentValue = strings.Join(strs, ",")
				}
			}
		}

		// Default value: first option, or the query for constant variables.
		if options := safeArray(vspec, "options"); len(options) > 0 {
			if first, ok := options[0].(map[string]interface{}); ok {
				if val, ok := first["value"].(string); ok {
					info.DefaultValue = val
				}
			}
		}
		if info.DefaultValue == "" && strings.TrimSuffix(safeString(vk, "kind"), "Variable") == "Constant" {
			info.DefaultValue = safeString(vspec, "query")
		}

		variables[name] = info
	}

	return variables
}

// dashboardSummaryV2 implements get_dashboard_summary for v2 dashboards.
func dashboardSummaryV2(spec map[string]interface{}, uid string, meta *models.DashboardMeta) (*DashboardSummary, error) {
	summary := &DashboardSummary{
		UID:         uid,
		Meta:        meta,
		Title:       safeString(spec, "title"),
		Description: safeString(spec, "description"),
		Tags:        safeStringSlice(spec, "tags"),
	}

	if ts := safeObject(spec, "timeSettings"); ts != nil {
		summary.TimeRange = TimeRangeSummary{
			From: safeString(ts, "from"),
			To:   safeString(ts, "to"),
		}
		summary.Refresh = safeString(ts, "autoRefresh")
	}

	for _, el := range collectElementsV2(spec) {
		summary.Panels = append(summary.Panels, extractPanelSummaryV2(el))
	}
	summary.PanelCount = len(summary.Panels)

	for _, v := range safeArray(spec, "variables") {
		if vk, ok := v.(map[string]interface{}); ok {
			summary.Variables = append(summary.Variables, extractVariableSummaryV2(vk))
		}
	}

	return summary, nil
}

// extractPanelSummaryV2 builds a PanelSummary from a v2 element. The panel type
// is the visualization plugin id (vizConfig.group); for library panels it is
// the element kind.
func extractPanelSummaryV2(el v2Element) PanelSummary {
	summary := PanelSummary{
		ID:          safeInt(el.Spec, "id"),
		Title:       safeString(el.Spec, "title"),
		Description: safeString(el.Spec, "description"),
	}

	if el.Kind == "Panel" {
		if viz := safeObject(el.Spec, "vizConfig"); viz != nil {
			summary.Type = safeString(viz, "group")
		}
		if data := safeObject(el.Spec, "data"); data != nil {
			if dataSpec := safeObject(data, "spec"); dataSpec != nil {
				summary.QueryCount = len(safeArray(dataSpec, "queries"))
			}
		}
	} else {
		summary.Type = el.Kind
	}

	return summary
}

// extractVariableSummaryV2 builds a VariableSummary from a v2 variable entry.
// The type is derived from the discriminator kind (e.g. "QueryVariable" -> "query").
func extractVariableSummaryV2(vk map[string]interface{}) VariableSummary {
	summary := VariableSummary{
		Type: strings.ToLower(strings.TrimSuffix(safeString(vk, "kind"), "Variable")),
	}
	if spec := safeObject(vk, "spec"); spec != nil {
		summary.Name = safeString(spec, "name")
		summary.Label = safeString(spec, "label")
	}
	return summary
}
