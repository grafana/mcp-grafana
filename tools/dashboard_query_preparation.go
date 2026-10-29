package tools

import (
	"fmt"
	"time"

	sqldialect "github.com/grafana/mcp-grafana/v2/tools/sql"
)

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
	allWarnings := allTemplateSelectionWarnings(db, args.Variables, values)
	for i := range queries {
		query := &queries[i]
		// Frontend macros are not dashboard variables, even when the executor
		// leaves them intact in the submitted expression.
		references, err := substituteInspectionMacros(variableSearchText(*query), query.Datasource.Type, from, to)
		if err != nil {
			return nil, err
		}
		references = substituteGrafanaMacros(references, from, to)
		query.RequiredVariables = findVariablesInQuery(references+" "+query.Datasource.UID, definitions, args.Variables)
		for _, variable := range query.RequiredVariables {
			if warning := allWarnings[variable.Name]; warning != "" {
				query.Warnings = append(query.Warnings, warning)
			}
		}
		if len(query.Warnings) > 0 {
			continue
		}
		query.ProcessedQuery, err = substituteInspectionMacros(substituteQueryTemplateVariableValues(query.Query, values, query.Datasource.Type), query.Datasource.Type, from, to)
		if err != nil {
			return nil, err
		}
		query.Datasource.UID = substituteTemplateVariableValues(query.Datasource.UID, values)
	}
	return queries, nil
}

// Inspection uses the executor's macro handling. InfluxDB forwards expressions
// intact, while ClickHouse uses its SQL dialect.
func substituteInspectionMacros(query, datasourceType string, from, to time.Time) (string, error) {
	switch normalizeDatasourceType(datasourceType) {
	case "influxdb":
		return query, nil
	case "clickhouse":
		dialect, err := sqldialect.DialectFor(sqldialect.ClickHouseDatasourceType)
		if err != nil {
			return "", err
		}
		return dialect.SubstituteMacros(query, from, to), nil
	default:
		return substituteGrafanaMacros(query, from, to), nil
	}
}

func allTemplateSelectionWarnings(db map[string]interface{}, overrides map[string]string, values templateVariableValues) map[string]string {
	warnings := make(map[string]string)
	const message = "Variable %q is set to All. Supply an explicit value in variables."
	for _, item := range safeArray(safeObject(db, "templating"), "list") {
		variable, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name := safeString(variable, "name")
		current := safeObject(variable, "current")
		selection, hasValue := current["value"]
		if override, ok := overrides[name]; ok {
			selection = override
			hasValue = true
		}
		_, resolved := values[name]
		if isAllTemplateSelection(selection) || (!hasValue && !resolved && safeString(current, "text") == "All") {
			warnings[name] = fmt.Sprintf(message, name)
		}
	}
	for name, value := range overrides {
		if isAllTemplateSelection(value) {
			warnings[name] = fmt.Sprintf(message, name)
		}
	}
	return warnings
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
