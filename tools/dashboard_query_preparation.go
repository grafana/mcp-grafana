package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type variableQueriesKey struct{}

// Register a per-server handler, without changing the shared tool definition.
// SQL option queries have the same permissions and side effects as raw SQL.
func registerDashboardQueryTool(s *mcp.Server, definition mcpgrafana.Tool, allowQueries bool) {
	tool := definition
	metadata := *definition.Tool
	annotations := *metadata.Annotations
	metadata.Annotations = &annotations
	metadata.Meta = maps.Clone(metadata.Meta)
	tool.Tool = &metadata
	if allowQueries {
		mcpgrafana.WithReadOnlyHintAnnotation(false)(tool.Tool)
		mcpgrafana.WithDestructiveHintAnnotation(true)(tool.Tool)
		mcpgrafana.WithIdempotentHintAnnotation(false)(tool.Tool)
		mcpgrafana.RequiresPermissions(datasourceQuery...)(tool.Tool)
	}
	tool.Handler = func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return definition.Handler(context.WithValue(ctx, variableQueriesKey{}, allowQueries), req)
	}
	tool.Register(s)
}

type preparedDashboardQuery struct {
	Query      string
	Target     map[string]interface{}
	Datasource datasourceInfo
	Variables  templateVariableValues
	Warnings   []string
	Start      string
	End        string
}

type variableOptionQueryKey struct {
	datasourceUID string
	query         string
	target        string
	start         string
	end           string
}

type variableOptionQueryResult struct {
	values []string
	err    error
}

// A cache belongs to one tool call and is shared by its sequential panel
// preparations. Keep failures too, so each panel reports the same resolution
// error without retrying the datasource within that call.
type variableOptionsCache map[variableOptionQueryKey]variableOptionQueryResult

func (cache variableOptionsCache) query(ctx context.Context, uid, query string, target map[string]interface{}, start, end string) ([]string, error) {
	targetJSON, err := json.Marshal(target)
	if err != nil {
		return nil, fmt.Errorf("encoding variable query target: %w", err)
	}
	key := variableOptionQueryKey{datasourceUID: uid, query: query, target: string(targetJSON), start: start, end: end}
	if result, ok := cache[key]; ok {
		return result.values, result.err
	}
	values, err := querySQLVariableOptions(ctx, uid, query, target, start, end)
	if cache != nil {
		cache[key] = variableOptionQueryResult{values: values, err: err}
	}
	return values, err
}

func dashboardQueryRange(start, end string) (time.Time, time.Time, error) {
	if start == "" {
		start = "now-1h"
	}
	if end == "" {
		end = "now"
	}
	r := gtime.TimeRange{From: start, To: end, Now: time.Now()}
	from, err := r.ParseFrom()
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parsing start time: %w", err)
	}
	to, err := r.ParseTo()
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parsing end time: %w", err)
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("end must not precede start")
	}
	return from, to, nil
}

// prepareDashboardQuery prepares frontend substitutions only. SQL plugin macros
// such as $__timeFilter remain in the query sent to Grafana's backend.
func prepareDashboardQuery(ctx context.Context, db map[string]interface{}, query string, target map[string]interface{}, source datasourceInfo, overrides map[string]string, start, end, overrideUID, overrideType string, optionsCache variableOptionsCache) (*preparedDashboardQuery, error) {
	from, to, err := dashboardQueryRange(start, end)
	if err != nil {
		return nil, err
	}
	prepared := &preparedDashboardQuery{
		Variables: extractTemplateVariableValues(db),
		Start:     strconv.FormatInt(from.UnixMilli(), 10),
		End:       strconv.FormatInt(to.UnixMilli(), 10),
	}
	definitions := make(map[string]map[string]interface{})
	for _, item := range safeArray(safeObject(db, "templating"), "list") {
		if variable, ok := item.(map[string]interface{}); ok {
			definitions[safeString(variable, "name")] = variable
		}
	}
	for name, value := range overrides {
		prepared.Variables[name] = []string{value}
		if multi, _ := definitions[name]["multi"].(bool); multi {
			if values, ok := parseVariableOverride(value); ok {
				prepared.Variables[name] = values
			}
		}
	}
	resolved, resolving := make(map[string]bool), make(map[string]bool)
	customAll := make(map[string]string)
	interpolate := func(text string) string {
		// Custom All values are literal Grafana expressions, not SQL string values.
		for name, value := range customAll {
			text = substituteVariables(text, map[string]string{name: value})
		}
		return substituteTemplateVariableValues(text, prepared.Variables)
	}
	var resolve func(string)
	resolve = func(name string) {
		if resolved[name] {
			return
		}
		if resolving[name] {
			prepared.Warnings = append(prepared.Warnings, fmt.Sprintf("Variable %q has a cyclic dependency. Supply an explicit variable value.", name))
			return
		}
		variable := definitions[name]
		if variable == nil {
			return
		}
		resolving[name] = true
		defer func() { delete(resolving, name); resolved[name] = true }()
		selected := safeObject(variable, "current")["value"]
		if override, ok := overrides[name]; ok {
			selected = override
		}
		all := selected == "$__all"
		switch values := selected.(type) {
		case []interface{}:
			all = len(values) > 0 && values[0] == "$__all"
		case []string:
			all = len(values) > 0 && values[0] == "$__all"
		}
		if !all {
			return
		}
		delete(prepared.Variables, name)
		if value := safeString(variable, "allValue"); value != "" {
			customAll[name] = value
			return
		}
		var values []string
		var optionErr error
		if safeString(variable, "type") == "query" {
			optionQuery := safeString(variable, "query")
			optionTarget := map[string]interface{}{"rawQuery": true}
			if object := safeObject(variable, "query"); object != nil {
				optionQuery = extractQueryExpression(object)
				optionTarget = maps.Clone(object)
				if _, ok := optionTarget["rawQuery"]; !ok {
					optionTarget["rawQuery"] = true
				}
			}
			ds := safeObject(variable, "datasource")
			dsUID := safeString(ds, "uid")
			for _, dependency := range findVariablesInQuery(optionQuery+" "+dsUID+" "+variableSearchText(panelQuery{Target: optionTarget}), nil, nil) {
				resolve(dependency.Name)
			}
			allow, _ := ctx.Value(variableQueriesKey{}).(bool)
			if !allow {
				optionErr = fmt.Errorf("variable option queries are disabled in this server mode")
			} else if safeString(variable, "regex") != "" {
				optionErr = fmt.Errorf("variable regex filtering is not supported")
			} else if len(prepared.Warnings) > 0 {
				optionErr = fmt.Errorf("a dependent variable could not be resolved")
			} else {
				optionQuery = interpolate(optionQuery)
				dsUID = interpolate(dsUID)
				optionTarget = substituteStringsInMap(optionTarget, interpolate)
				values, optionErr = optionsCache.query(ctx, dsUID, optionQuery, optionTarget, prepared.Start, prepared.End)
			}
		} else {
			for _, item := range safeArray(variable, "options") {
				if option, ok := item.(map[string]interface{}); ok && option["value"] != "$__all" && option["value"] != nil {
					values = append(values, fmt.Sprint(option["value"]))
				}
			}
			if len(values) == 0 {
				optionErr = fmt.Errorf("no saved options are available")
			}
		}
		if optionErr != nil {
			prepared.Warnings = append(prepared.Warnings, fmt.Sprintf("Could not resolve All for variable %q: %v. Check its datasource/query or supply explicit values.", name, optionErr))
			return
		}
		prepared.Variables[name] = values
	}
	// Only the effective datasource is a dependency. Query fields may still use
	// the original variable, so keep scanning those even when it is overridden.
	datasourceUID := source.UID
	if overrideUID != "" {
		datasourceUID = overrideUID
	}
	searchText := query + " " + datasourceUID + " " + variableSearchText(panelQuery{Target: target})
	for _, variable := range findVariablesInQuery(searchText, nil, nil) {
		resolve(variable.Name)
	}
	datasourceVariables := firstTemplateVariableValues(prepared.Variables)
	maps.Copy(datasourceVariables, customAll)
	source, err = resolvePanelDatasource(ctx, source, datasourceVariables, overrideUID, overrideType)
	if err != nil {
		return nil, err
	}
	prepared.Datasource = source
	prepared.Query = interpolate(query)
	if target != nil {
		prepared.Target = substituteStringsInMap(target, interpolate)
	}
	switch normalizeDatasourceType(source.Type) {
	case "prometheus", "loki", "postgres", "mysql", "mssql", "bigquery", "clickhouse":
		prepared.Query = substituteGrafanaMacros(prepared.Query, from, to)
	}
	return prepared, nil
}

func prepareInspectedQueries(ctx context.Context, db map[string]interface{}, args DashboardPanelQueriesParams, queries []panelQuery) []panelQuery {
	// Preserve raw-only inspection unless the caller requests preparation.
	if args.Variables == nil && args.Start == "" && args.End == "" {
		return queries
	}
	from, to, rangeErr := dashboardQueryRange(args.Start, args.End)
	if rangeErr == nil {
		args.Start, args.End = strconv.FormatInt(from.UnixMilli(), 10), strconv.FormatInt(to.UnixMilli(), 10)
	}
	optionsCache := make(variableOptionsCache)
	for i := range queries {
		if rangeErr != nil {
			queries[i].ProcessedQuery = ""
			queries[i].ProcessedTarget = nil
			queries[i].Warnings = []string{rangeErr.Error()}
			continue
		}
		q := &queries[i]
		q.RequiredVariables = findVariablesInQuery(variableSearchText(*q), extractDashboardVariables(db), args.Variables)
		target := q.rawTarget
		if target == nil {
			target = q.Target
		}
		prepared, err := prepareDashboardQuery(ctx, db, q.Query, target, q.Datasource, args.Variables, args.Start, args.End, "", "", optionsCache)
		if err != nil {
			q.ProcessedQuery = ""
			q.ProcessedTarget = nil
			q.Warnings = []string{err.Error()}
			continue
		}
		q.Datasource = prepared.Datasource
		q.ProcessedQuery = prepared.Query
		if q.Target != nil {
			q.ProcessedTarget = queryTargetFields(prepared.Target)
		}
		q.Warnings = prepared.Warnings
		if len(q.Warnings) > 0 {
			q.ProcessedQuery = ""
			q.ProcessedTarget = nil
		}
	}
	return queries
}

// Multi-value overrides can be a JSON string array or a SQL-quoted list. Decode
// only complete lists, leaving commas and quotes in ordinary values untouched.
func parseVariableOverride(value string) ([]string, bool) {
	var values []string
	if strings.HasPrefix(strings.TrimSpace(value), "[") && json.Unmarshal([]byte(value), &values) == nil {
		return values, true
	}
	rest := strings.TrimSpace(value)
	for strings.HasPrefix(rest, "'") {
		rest = rest[1:]
		var item strings.Builder
		closed := false
		for len(rest) > 0 {
			if rest[0] == '\'' {
				if strings.HasPrefix(rest, "''") {
					item.WriteByte('\'')
					rest = rest[2:]
					continue
				}
				rest = strings.TrimSpace(rest[1:])
				closed = true
				break
			}
			item.WriteByte(rest[0])
			rest = rest[1:]
		}
		if !closed {
			return nil, false
		}
		values = append(values, item.String())
		if rest == "" {
			return values, true
		}
		if !strings.HasPrefix(rest, ",") {
			return nil, false
		}
		rest = strings.TrimSpace(rest[1:])
	}
	return nil, false
}

func querySQLVariableOptions(ctx context.Context, uid, query string, target map[string]interface{}, start, end string) ([]string, error) {
	if uid == "" || query == "" {
		return nil, fmt.Errorf("a SQL variable datasource UID and query are required")
	}
	// Built-in macros are resolved by the shared frontend helper or SQL plugin.
	for _, variable := range findVariablesInQuery(query+" "+variableSearchText(panelQuery{Target: target}), nil, nil) {
		if !strings.HasPrefix(variable.Name, "__") {
			return nil, fmt.Errorf("option query still contains variable %q", variable.Name)
		}
	}
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}
	switch normalizeDatasourceType(ds.Type) {
	case "postgres", "mysql", "mssql", "bigquery":
	default:
		return nil, fmt.Errorf("option queries for datasource type %q are not supported", ds.Type)
	}
	result, err := executeSQLPanelQuery(ctx, uid, &panelInfo{RawTarget: target}, query, start, end, nil, ds.Type)
	if err != nil {
		return nil, err
	}
	if len(result.Columns) == 0 {
		return nil, fmt.Errorf("option query returned no columns")
	}
	column := result.Columns[0]
	if slices.Contains(result.Columns, "__value") {
		column = "__value"
	}
	values := []string{}
	seen := map[string]bool{}
	for _, row := range result.Rows {
		value := reflect.ValueOf(row[column])
		// Grafana's SQL frames use nullable fields, decoded by the SDK as
		// pointers. Format the value, never its address, and omit null options.
		if value.Kind() == reflect.Pointer {
			value = value.Elem()
		}
		if !value.IsValid() {
			continue
		}
		text := fmt.Sprint(value.Interface())
		if !seen[text] {
			values = append(values, text)
			seen[text] = true
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("option query returned no usable values")
	}
	return values, nil
}

// Epoch macros are frontend built-ins, independent of SQL plugin macros.
func substituteEpochMacros(query string, from, to time.Time) string {
	for name, value := range map[string]int64{"__from": from.UnixMilli(), "__to": to.UnixMilli()} {
		text := strconv.FormatInt(value, 10)
		query = strings.ReplaceAll(query, "${"+name+"}", text)
		query = replaceSimpleDollarVar(query, name, text)
	}
	return query
}
