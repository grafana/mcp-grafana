package tools

import "fmt"

// preparePanelQueries reuses execution's saved values, formatting and frontend
// time macros. Inspection never fetches datasource metadata or variable options.
func preparePanelQueries(db map[string]interface{}, args DashboardPanelQueriesParams, queries []panelQuery, definitions map[string]VariableInfo) ([]panelQuery, error) {
	if args.Variables == nil && args.Start == "" && args.End == "" {
		return queries, nil
	}
	start, end := args.Start, args.End
	if start == "" {
		start = "now-1h"
	}
	if end == "" {
		end = "now"
	}
	from, err := parseTime(start)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}
	to, err := parseTime(end)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}
	values := extractTemplateVariableValues(db)
	for name, value := range args.Variables {
		values[name] = []string{value}
	}
	allSelections := make(map[string]bool)
	for _, item := range safeArray(safeObject(db, "templating"), "list") {
		variable, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name := safeString(variable, "name")
		current := safeObject(variable, "current")
		selection, hasValue := current["value"]
		if override, ok := args.Variables[name]; ok {
			selection = override
			hasValue = true
		}
		_, resolved := values[name]
		allSelections[name] = isAllTemplateSelection(selection) || (!hasValue && !resolved && safeString(current, "text") == "All")
	}
	for i := range queries {
		query := &queries[i]
		references := substituteGrafanaMacros(variableSearchText(*query), from, to)
		query.RequiredVariables = findVariablesInQuery(references+" "+query.Datasource.UID, definitions, args.Variables)
		for _, variable := range query.RequiredVariables {
			if allSelections[variable.Name] || args.Variables[variable.Name] == "$__all" {
				query.Warnings = append(query.Warnings, fmt.Sprintf("Variable %q is set to All. Supply an explicit value in variables.", variable.Name))
			}
		}
		if len(query.Warnings) > 0 {
			continue
		}
		query.ProcessedQuery = substituteGrafanaMacros(substituteTemplateVariableValues(query.Query, values), from, to)
		query.Datasource.UID = substituteTemplateVariableValues(query.Datasource.UID, values)
	}
	return queries, nil
}

func isAllTemplateSelection(value interface{}) bool {
	switch selected := value.(type) {
	case string:
		return selected == "$__all"
	case []interface{}:
		return len(selected) > 0 && selected[0] == "$__all"
	case []string:
		return len(selected) > 0 && selected[0] == "$__all"
	default:
		return false
	}
}
