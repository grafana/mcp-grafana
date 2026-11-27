package tools

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
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
	interp := newDashboardInterpolator(db, args.Variables)
	for i := range queries {
		query := &queries[i]
		// Frontend macros are not dashboard variables, even when the executor
		// leaves them intact in the submitted expression.
		options := query.interval.withLowerLimit(interp, query.Datasource.Type, nil)
		references := substituteFrontendMacros(interp.applyConditionalAll(variableSearchText(*query), query.Datasource.Type), query.Datasource.Type, from, to, options)
		references, err := substituteBackendMacros(references, query.Datasource.Type, from, to)
		if err != nil {
			return nil, err
		}
		references = substituteGrafanaMacros(references, from, to, options)
		query.RequiredVariables = findPanelVariablesInQuery(references+" "+query.Datasource.UID, query.Datasource.Type, definitions, args.Variables)
		for _, variable := range query.RequiredVariables {
			if warning := interp.allSelectionWarning(variable.Name); warning != "" {
				query.Warnings = append(query.Warnings, warning)
			}
		}
		if len(query.Warnings) > 0 {
			continue
		}
		query.ProcessedQuery, err = substituteBackendMacros(preparePanelExpression(interp, query.Query, query.Datasource.Type, from, to, options), query.Datasource.Type, from, to)
		if err != nil {
			return nil, err
		}
		query.Datasource.UID = interp.interpolate(query.Datasource.UID)
	}
	return queries, nil
}

// findPanelVariablesInQuery excludes whole macro names owned by the datasource.
// The old substring expansion hid names such as $__interval_s by changing them
// to 36s_s. Whole-name expansion leaves plugin macros intact for Grafana.
func findPanelVariablesInQuery(query, datasourceType string, definitions map[string]VariableInfo, overrides map[string]string) []VariableInfo {
	var variables []VariableInfo
	for _, variable := range findVariablesInQuery(query, definitions, overrides) {
		_, defined := definitions[variable.Name]
		_, overridden := overrides[variable.Name]
		if !defined && !overridden && isPanelQueryMacro(variable.Name, datasourceType) {
			continue
		}
		variables = append(variables, variable)
	}
	return variables
}

func isPanelQueryMacro(name, datasourceType string) bool {
	if !strings.HasPrefix(name, "__") {
		return false
	}
	switch name {
	case "__interval", "__interval_ms", "__range", "__range_s", "__range_ms", "__rate_interval", "__rate_interval_ms", "__auto", "__from", "__to":
		return true
	}
	switch normalizeDatasourceType(datasourceType) {
	case "postgres", "mysql", "mssql":
		// Built-in SQL datasource macros are expanded by Grafana's backend.
		switch name {
		case "__time", "__timeEpoch", "__timeFilter", "__timeFrom", "__timeTo", "__timeGroup", "__timeGroupAlias",
			"__unixEpochFrom", "__unixEpochTo", "__unixEpochFilter", "__unixEpochNanoFilter", "__unixEpochNanoFrom", "__unixEpochNanoTo", "__unixEpochGroup", "__unixEpochGroupAlias":
			return true
		}
	case "clickhouse":
		// ClickHouse's documented plugin macro set, including its frontend macros.
		switch name {
		case "__timeFilter", "__timeFilter_ms", "__dateFilter", "__dateTimeFilter", "__dt",
			"__fromTime", "__fromTime_ms", "__toTime", "__toTime_ms", "__timeFrom", "__timeTo", "__timeGroup",
			"__interval_s", "__timeInterval", "__timeInterval_ms", "__fromGrafanaInterval", "__conditionalAll",
			"__columns", "__rateColumns", "__perSecondColumns", "__increaseColumns", "__lttb":
			return true
		}
	}
	return false
}

// preparePanelExpression expands frontend macros before inserting variable
// values, so a value is never rescanned, as in Grafana's single-pass replace.
func preparePanelExpression(interp *variableInterpolator, query, datasourceType string, from, to time.Time, options intervalOptions) string {
	query = interp.applyConditionalAll(query, datasourceType)
	return interp.interpolateQuery(substituteFrontendMacros(query, datasourceType, from, to, options), datasourceType)
}

const (
	clickHouseDatasourceType = "grafana-clickhouse-datasource"
	conditionalAllMacro      = "$__conditionalAll("
)

// conditionalAllVariable is the first $name or ${name} reference in the
// macro's second argument, as the ClickHouse plugin matches it.
var conditionalAllVariable = regexp.MustCompile(`\$\{(\w+)\}|\$(\w+)`)

// applyConditionalAll expands $__conditionalAll(condition, $variable) as the
// ClickHouse plugin's frontend does before inserting variable values: the
// macro becomes 1=1 when the variable is set to All or is empty, and the
// condition otherwise. Like the plugin, it works from the last macro to the
// first, and leaves the query unchanged when a macro doesn't have exactly two
// arguments.
func (v *variableInterpolator) applyConditionalAll(query, datasourceType string) string {
	if !strings.EqualFold(datasourceType, clickHouseDatasourceType) {
		return query
	}
	for i := strings.LastIndex(query, conditionalAllMacro); i >= 0; i = strings.LastIndex(query, conditionalAllMacro) {
		args := macroArguments(query[i+len(conditionalAllMacro)-1:])
		if len(args) != 2 {
			return query
		}
		phrase := args[0]
		if m := conditionalAllVariable.FindStringSubmatch(strings.TrimSpace(args[1])); m != nil {
			name := m[1] + m[2]
			values, known := v.values[name]
			if v.all[name] || (known && strings.Join(values, ",") == "") {
				phrase = "1=1"
			}
		}
		query = strings.Replace(query, conditionalAllMacro+args[0]+","+args[1]+")", phrase, 1)
	}
	return query
}

// checkAllSelections returns the warning for the first variable that the
// references, with $__conditionalAll expanded, use while it is set to All or
// has no value.
func (v *variableInterpolator) checkAllSelections(references, datasourceType string) error {
	for _, variable := range findVariablesInQuery(v.applyConditionalAll(references, datasourceType), nil, nil) {
		if warning := v.allSelectionWarning(variable.Name); warning != "" {
			return errors.New(warning)
		}
	}
	return nil
}

// macroArguments splits the parenthesized arguments at the start of s on
// top-level commas, as the ClickHouse plugin's getMacroArgs does. It returns
// nil when the parentheses are not closed.
func macroArguments(s string) []string {
	var args []string
	depth, start := 0, 1
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 1 {
				args = append(args, s[start:i])
				start = i + 1
			}
		}
		if depth == 0 {
			return append(args, s[start:i])
		}
	}
	return nil
}

// substituteFrontendMacros mirrors the executors. InfluxDB forwards macros
// intact, and ClickHouse expands them in its SQL dialect instead.
func substituteFrontendMacros(query, datasourceType string, from, to time.Time, options intervalOptions) string {
	switch normalizeDatasourceType(datasourceType) {
	case "influxdb", "clickhouse":
		return query
	default:
		return substituteGrafanaMacros(query, from, to, options)
	}
}

// substituteBackendMacros applies the ClickHouse dialect, which query_sql runs
// after variables, like the plugin backend does.
func substituteBackendMacros(query, datasourceType string, from, to time.Time) (string, error) {
	if normalizeDatasourceType(datasourceType) != "clickhouse" {
		return query, nil
	}
	dialect, err := sqldialect.DialectFor(sqldialect.ClickHouseDatasourceType)
	if err != nil {
		return "", err
	}
	return dialect.SubstituteMacros(query, from, to), nil
}
