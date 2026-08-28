package tools

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client/datasources"
	mcpgrafana "github.com/grafana/mcp-grafana"
	sqldialect "github.com/grafana/mcp-grafana/tools/sql"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/common/model"
)

// RunPanelQueryParams defines parameters for running panel queries
type RunPanelQueryParams struct {
	DashboardUID   string            `json:"dashboardUid" jsonschema:"required,description=Dashboard UID"`
	PanelIDs       []int             `json:"panelIds" jsonschema:"required,description=Panel IDs to execute (one or more)"`
	QueryIndex     *int              `json:"queryIndex,omitempty" jsonschema:"description=Index of the query to execute per panel (0-based\\, defaults to 0). Use get_dashboard_panel_queries to see all queries."`
	Start          string            `json:"start" jsonschema:"description=Override start time (e.g. 'now-1h'\\, RFC3339\\, Unix ms)"`
	End            string            `json:"end" jsonschema:"description=Override end time (e.g. 'now'\\, RFC3339\\, Unix ms)"`
	Variables      map[string]string `json:"variables" jsonschema:"description=Override dashboard variables (e.g. {\"job\": \"api-server\"})"`
	DatasourceUID  string            `json:"datasourceUid,omitempty" jsonschema:"description=Override datasource UID"`
	DatasourceType string            `json:"datasourceType,omitempty" jsonschema:"description=Override datasource type (prometheus\\, loki\\, grafana-clickhouse-datasource\\, cloudwatch\\, influxdb\\, grafana-bigquery-datasource\\, mssql\\, grafana-postgresql-datasource\\, postgres)"`
}

// QueryTimeRange represents the actual time range used for a panel query
type QueryTimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// PanelQueryResult contains the result of a single panel query
type PanelQueryResult struct {
	PanelID        int         `json:"panelId"`
	PanelTitle     string      `json:"panelTitle"`
	DatasourceType string      `json:"datasourceType"`
	DatasourceUID  string      `json:"datasourceUid"`
	Query          string      `json:"query"`
	Results        interface{} `json:"results"`
	Hints          []string    `json:"hints,omitempty"`
}

// RunPanelQueryResult contains the result of running panel queries
type RunPanelQueryResult struct {
	DashboardUID string                    `json:"dashboardUid"`
	Results      map[int]*PanelQueryResult `json:"results"`
	Errors       map[int]string            `json:"errors,omitempty"`
	TimeRange    QueryTimeRange            `json:"timeRange"`
}

// singlePanelQueryParams holds the parameters for running a single panel query.
type singlePanelQueryParams struct {
	DB         map[string]interface{}
	PanelID    int
	QueryIndex int
	Start      string
	End        string
	Variables  map[string]string
	DsUID      string
	DsType     string
}

// panelInfo contains extracted information about a panel
type panelInfo struct {
	ID             int
	Title          string
	DatasourceUID  string
	DatasourceType string
	Query          string
	RawTarget      map[string]interface{} // For CloudWatch and other complex query types
}

// runPanelQuery executes one or more dashboard panel queries with optional time range and variable overrides
func runPanelQuery(ctx context.Context, args RunPanelQueryParams) (*RunPanelQueryResult, error) {
	if len(args.PanelIDs) == 0 {
		return nil, fmt.Errorf("panelIds is required and must not be empty")
	}

	// Determine time range defaults
	start := args.Start
	end := args.End
	if start == "" {
		start = "now-1h"
	}
	if end == "" {
		end = "now"
	}

	// Fetch the dashboard once
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: args.DashboardUID})
	if err != nil {
		return nil, fmt.Errorf("fetching dashboard: %w", err)
	}

	db, ok := dashboard.Dashboard.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}

	queryIndex := 0
	if args.QueryIndex != nil {
		queryIndex = *args.QueryIndex
	}

	results := make(map[int]*PanelQueryResult)
	errs := make(map[int]string)

	// Execute each panel query
	for _, panelID := range args.PanelIDs {
		result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
			DB:         db,
			PanelID:    panelID,
			QueryIndex: queryIndex,
			Start:      start,
			End:        end,
			Variables:  args.Variables,
			DsUID:      args.DatasourceUID,
			DsType:     args.DatasourceType,
		})
		if err != nil {
			errs[panelID] = err.Error()
		} else {
			results[panelID] = result
		}
	}

	return &RunPanelQueryResult{
		DashboardUID: args.DashboardUID,
		Results:      results,
		Errors:       errs,
		TimeRange: QueryTimeRange{
			Start: start,
			End:   end,
		},
	}, nil
}

// runSinglePanelQuery executes a single panel's query within a dashboard
func runSinglePanelQuery(ctx context.Context, params singlePanelQueryParams) (*PanelQueryResult, error) {
	// Find the panel by ID
	panel, err := findPanelByID(params.DB, params.PanelID)
	if err != nil {
		return nil, fmt.Errorf("finding panel: %w", err)
	}

	// Extract query and datasource info from the panel
	panelData, err := extractPanelInfo(panel, params.QueryIndex)
	if err != nil {
		return nil, fmt.Errorf("extracting panel info: %w", err)
	}

	// Extract template variables from the dashboard. Keep both the first value
	// (used for datasource references) and the complete value list (used for
	// formatted query interpolation).
	templateVariables := extractTemplateVariableValues(params.DB)
	vars := firstTemplateVariableValues(templateVariables)

	// Apply variable overrides from user
	for name, value := range params.Variables {
		vars[name] = value
		templateVariables[name] = []string{value}
	}

	// Resolve datasource UID and type
	datasourceUID := panelData.DatasourceUID
	datasourceType := panelData.DatasourceType

	// Apply explicit datasource overrides (highest priority)
	if params.DsUID != "" {
		datasourceUID = params.DsUID
		if params.DsType != "" {
			datasourceType = params.DsType
		}
	} else if isVariableReference(datasourceUID) {
		// Resolve variable reference only if no explicit override
		varName := extractVariableName(datasourceUID)
		if resolvedUID, ok := vars[varName]; ok {
			datasourceUID = resolvedUID
			// Reset type so it gets looked up from the resolved datasource
			datasourceType = ""
		} else {
			availableDS := getAvailableDatasourceUIDs(ctx, panelData.DatasourceType)
			return nil, fmt.Errorf("datasource variable '%s' not found. Hint: Use 'datasourceUid' and 'datasourceType' to override. Available %s datasources: %v", datasourceUID, panelData.DatasourceType, availableDS)
		}
	}

	// If we still need the datasource type, look it up
	if datasourceType == "" && datasourceUID != "" {
		ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: datasourceUID})
		if err != nil {
			var forbiddenErr *datasources.GetDataSourceByUIDForbidden
			var notFoundErr *datasources.GetDataSourceByUIDNotFound

			switch {
			case errors.As(err, &forbiddenErr):
				availableDS := getAvailableDatasourceUIDs(ctx, "")
				return nil, fmt.Errorf("permission denied for datasource '%s'. Hint: Provide both 'datasourceUid' and 'datasourceType' to override. Available datasources: %v", datasourceUID, availableDS)
			case errors.As(err, &notFoundErr):
				availableDS := getAvailableDatasourceUIDs(ctx, "")
				return nil, fmt.Errorf("datasource '%s' not found. Available datasources: %v", datasourceUID, availableDS)
			default:
				return nil, fmt.Errorf("fetching datasource info: %w", err)
			}
		}
		datasourceType = ds.Type
	}

	// Substitute variables in the query
	query := substituteTemplateVariableValues(panelData.Query, templateVariables)

	// Route to appropriate datasource and execute query
	var results interface{}

	switch normalizeDatasourceType(datasourceType) {
	case "prometheus":
		results, err = executePrometheusQuery(ctx, datasourceUID, query, params.Start, params.End)
	case "loki":
		results, err = executeLokiQuery(ctx, datasourceUID, query, params.Start, params.End)
	case "clickhouse":
		results, err = executeClickHouseQuery(ctx, datasourceUID, query, params.Start, params.End)
	case "cloudwatch":
		results, err = executeCloudWatchPanelQuery(ctx, datasourceUID, panelData, params.Start, params.End, templateVariables)
	case "influxdb":
		results, err = executeInfluxDBQuery(ctx, datasourceUID, panelData, query, params.Start, params.End)
	case "bigquery":
		results, err = executeSQLPanelQuery(ctx, datasourceUID, panelData, query, params.Start, params.End, templateVariables, sqldialect.BigQueryDatasourceType)
	case "mysql":
		results, err = executeSQLPanelQuery(ctx, datasourceUID, panelData, query, params.Start, params.End, templateVariables, sqldialect.MySQLDatasourceType)
	case "mssql":
		results, err = executeSQLPanelQuery(ctx, datasourceUID, panelData, query, params.Start, params.End, templateVariables, sqldialect.MSSQLDatasourceType)
	case "postgres":
		// PostgreSQL exposes two datasource identifiers (grafana-postgresql-datasource
		// and the legacy postgres); pass the resolved type through so the datasource
		// object sent to Grafana matches what the panel actually declared.
		results, err = executeSQLPanelQuery(ctx, datasourceUID, panelData, query, params.Start, params.End, templateVariables, datasourceType)
	default:
		return nil, fmt.Errorf("datasource type '%s' is not supported by run_panel_query; use the native query tool (e.g. query_prometheus\\, query_loki_logs\\, query_sql\\, query_cloudwatch\\, query_influxdb) directly", datasourceType)
	}

	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}

	// Check for empty results and generate hints
	var hints []string
	if isEmptyPanelResult(results) {
		hints = generatePanelQueryHints(datasourceType, query)
	}

	return &PanelQueryResult{
		PanelID:        params.PanelID,
		PanelTitle:     panelData.Title,
		DatasourceType: datasourceType,
		DatasourceUID:  datasourceUID,
		Query:          query,
		Results:        results,
		Hints:          hints,
	}, nil
}

type templateVariableValues map[string][]string

// substituteTemplateVariableValues interpolates variables while retaining
// multi-select values for formatters such as sqlstring. Explicitly supported
// formatters follow Grafana's formatting syntax; unknown formatters fall back
// to Grafana's glob representation.
func substituteTemplateVariableValues(query string, variables templateVariableValues) string {
	for name, values := range variables {
		variableRe := regexp.MustCompile(fmt.Sprintf(
			`\$\{%s(?::[^}]*)?\}|\[\[%s(?::[^\]]*)?\]\]|\$%s\b`,
			regexp.QuoteMeta(name), regexp.QuoteMeta(name), regexp.QuoteMeta(name),
		))
		query = variableRe.ReplaceAllStringFunc(query, func(match string) string {
			format, hasFormat := templateVariableFormat(match)
			if !hasFormat {
				return firstTemplateVariableValue(values)
			}
			return formatTemplateVariable(values, format)
		})
	}
	return query
}

func templateVariableFormat(match string) (string, bool) {
	var inner string
	switch {
	case strings.HasPrefix(match, "${") && strings.HasSuffix(match, "}"):
		inner = match[2 : len(match)-1]
	case strings.HasPrefix(match, "[[") && strings.HasSuffix(match, "]]"):
		inner = match[2 : len(match)-2]
	default:
		return "", false
	}

	separator := strings.IndexByte(inner, ':')
	if separator < 0 {
		return "", false
	}
	return inner[separator+1:], true
}

func firstTemplateVariableValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func formatTemplateVariable(values []string, format string) string {
	formatName := format
	if separator := strings.IndexByte(format, ':'); separator >= 0 {
		formatName = format[:separator]
	}

	if formatName == "sqlstring" {
		return formatSQLStringVariable(values)
	}

	// Grafana uses glob as the fallback for unknown formatting options.
	if len(values) > 1 {
		return "{" + strings.Join(values, ",") + "}"
	}
	return firstTemplateVariableValue(values)
}

func formatSQLStringVariable(values []string) string {
	if len(values) == 0 {
		return ""
	}

	formatted := make([]string, len(values))
	for i, value := range values {
		// Match Grafana's SQLString formatter: pair single quotes and escape
		// double quotes with a backslash before surrounding the value in quotes.
		value = strings.ReplaceAll(value, "'", "''")
		value = strings.ReplaceAll(value, `"`, `\"`)
		formatted[i] = "'" + value + "'"
	}
	return strings.Join(formatted, ",")
}

func substituteTemplateVariablesInMapWithValues(target map[string]interface{}, variables templateVariableValues) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range target {
		switch val := v.(type) {
		case string:
			result[k] = substituteTemplateVariableValues(val, variables)
		case map[string]interface{}:
			result[k] = substituteTemplateVariablesInMapWithValues(val, variables)
		case []interface{}:
			result[k] = substituteTemplateVariablesInSliceWithValues(val, variables)
		default:
			result[k] = v
		}
	}
	return result
}

func substituteTemplateVariablesInSliceWithValues(slice []interface{}, variables templateVariableValues) []interface{} {
	result := make([]interface{}, len(slice))
	for i, v := range slice {
		switch val := v.(type) {
		case string:
			result[i] = substituteTemplateVariableValues(val, variables)
		case map[string]interface{}:
			result[i] = substituteTemplateVariablesInMapWithValues(val, variables)
		case []interface{}:
			result[i] = substituteTemplateVariablesInSliceWithValues(val, variables)
		default:
			result[i] = v
		}
	}
	return result
}

// extractPanelInfo extracts query and datasource information from a panel
func extractPanelInfo(panel map[string]interface{}, queryIndex int) (*panelInfo, error) {
	info := &panelInfo{
		ID:    safeInt(panel, "id"),
		Title: safeString(panel, "title"),
	}

	// Extract query from targets
	targets := safeArray(panel, "targets")
	if len(targets) == 0 {
		return nil, fmt.Errorf("panel has no query targets")
	}

	// Bounds check for queryIndex
	if queryIndex < 0 || queryIndex >= len(targets) {
		return nil, fmt.Errorf("queryIndex %d out of range (panel has %d queries, valid range: 0-%d)", queryIndex, len(targets), len(targets)-1)
	}

	// Get the target at the specified index
	target, ok := targets[queryIndex].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid target format")
	}

	// Store raw target for CloudWatch and other complex query types
	info.RawTarget = target

	// Extract datasource - prefer target-level (more specific) over panel-level.
	// This handles "Mixed" datasource panels where each target specifies its own datasource.
	if targetDS := safeObject(target, "datasource"); targetDS != nil {
		info.DatasourceUID = safeString(targetDS, "uid")
		info.DatasourceType = safeString(targetDS, "type")
	}

	// Fall back to panel-level datasource
	if info.DatasourceUID == "" {
		if dsField := safeObject(panel, "datasource"); dsField != nil {
			info.DatasourceUID = safeString(dsField, "uid")
			info.DatasourceType = safeString(dsField, "type")
		}
	}

	if info.DatasourceUID == "" {
		return nil, fmt.Errorf("could not determine datasource for panel")
	}

	// Try to get query expression - different datasources use different field names
	query := extractQueryExpression(target)

	// CloudWatch panels use structured targets rather than string expressions
	if query == "" && normalizeDatasourceType(info.DatasourceType) != "cloudwatch" {
		return nil, fmt.Errorf("could not extract query from panel target (checked: expr, query, expression, rawSql, rawSQL, rawQuery, target)")
	}
	info.Query = query

	return info, nil
}

// extractTemplateVariableValues extracts all selected values of each dashboard
// template variable. Grafana stores multi-select values in current.value as an
// array, and formatted interpolation needs to retain that array.
func extractTemplateVariableValues(db map[string]interface{}) templateVariableValues {
	variables := make(templateVariableValues)

	templating := safeObject(db, "templating")
	if templating == nil {
		return variables
	}

	list := safeArray(templating, "list")
	for _, v := range list {
		variable, ok := v.(map[string]interface{})
		if !ok {
			continue
		}

		name := safeString(variable, "name")
		if name == "" {
			continue
		}

		// Get current value - can be in different formats
		current := safeObject(variable, "current")
		currentValueSet := false
		if current != nil {
			// Try "value" field first (can be string or array).
			if val, ok := current["value"]; ok {
				currentValueSet = true
				switch v := val.(type) {
				case string:
					if v != "$__all" {
						variables[name] = []string{v}
					}
				case nil:
					// Grafana formats a defined null/undefined value as an empty
					// string. Keep the empty slice distinguishable from a missing
					// variable so formatted expressions are still replaced.
					variables[name] = []string{}
				case []interface{}:
					if len(v) == 0 {
						variables[name] = []string{}
					} else if first, ok := v[0].(string); ok && first != "$__all" {
						values := make([]string, 0, len(v))
						for _, item := range v {
							if str, ok := item.(string); ok {
								values = append(values, str)
							}
						}
						variables[name] = values
					}
				case []string:
					if len(v) == 0 || v[0] != "$__all" {
						variables[name] = append([]string(nil), v...)
					}
				}
			}
			// Fall back to "text" field
			if !currentValueSet {
				if text, ok := current["text"].(string); ok && text != "" && text != "All" {
					variables[name] = []string{text}
				}
			}
		}

		// Constant and textbox variables hold their value in "query" and are
		// frequently saved without a "current" at all. Without this fallback
		// the raw $name survives substitution and reaches the datasource,
		// which for SQL datasources is a syntax error.
		if !currentValueSet {
			switch safeString(variable, "type") {
			case "constant", "textbox":
				if q := safeString(variable, "query"); q != "" {
					variables[name] = []string{q}
				}
			}
		}
	}

	return variables
}

func firstTemplateVariableValues(values templateVariableValues) map[string]string {
	variables := make(map[string]string, len(values))
	for name, value := range values {
		if len(value) > 0 {
			variables[name] = value[0]
		}
	}
	return variables
}

// executePrometheusQuery runs a Prometheus query using the existing queryPrometheus function
func executePrometheusQuery(ctx context.Context, datasourceUID, query, start, end string) (model.Value, error) {
	// Parse time range for macro substitution
	startTime, err := parseTime(start)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}
	endTime, err := parseTime(end)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}

	// Substitute Grafana temporal macros ($__range, $__rate_interval, $__interval)
	query = substituteGrafanaMacros(query, startTime, endTime)

	return queryPrometheus(ctx, QueryPrometheusParams{
		DatasourceUID: datasourceUID,
		Expr:          query,
		StartTime:     start,
		EndTime:       end,
		StepSeconds:   60, // Default 1-minute resolution
		QueryType:     "range",
	})
}

// executeLokiQuery runs a Loki query using the existing queryLokiLogs function
func executeLokiQuery(ctx context.Context, datasourceUID, query, start, end string) ([]LogEntry, error) {
	// Convert relative times to RFC3339 for Loki
	startTime, err := parseTime(start)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}
	endTime, err := parseTime(end)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}

	// Substitute Grafana temporal macros ($__range, $__rate_interval, $__interval)
	query = substituteGrafanaMacros(query, startTime, endTime)

	result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
		DatasourceUID: datasourceUID,
		LogQL:         query,
		StartRFC3339:  startTime.Format("2006-01-02T15:04:05Z07:00"),
		EndRFC3339:    endTime.Format("2006-01-02T15:04:05Z07:00"),
		Limit:         100,
		Direction:     "backward",
		QueryType:     "range",
	})
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}

func executeClickHouseQuery(ctx context.Context, datasourceUID, query, start, end string) (*sqldialect.SQLQueryResult, error) {
	return querySQLHandler(ctx, sqldialect.QuerySQLParams{
		DatasourceUID: datasourceUID,
		Query:         query,
		Start:         start,
		End:           end,
	})
}

// executeCloudWatchPanelQuery runs a CloudWatch query using Grafana's /api/ds/query endpoint
func executeCloudWatchPanelQuery(ctx context.Context, datasourceUID string, panelData *panelInfo, start, end string, variables templateVariableValues) (interface{}, error) {
	if panelData.RawTarget == nil {
		return nil, fmt.Errorf("CloudWatch panel target not available")
	}

	// Check for math expression panels
	if dsField := safeObject(panelData.RawTarget, "datasource"); dsField != nil {
		if dsType := safeString(dsField, "type"); dsType == "__expr__" || dsType == "expression" {
			return nil, fmt.Errorf("math expression panels require executing multiple queries; use query_cloudwatch directly for the underlying metrics")
		}
	}

	// Parse time range
	startTime, err := parseTime(start)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}
	endTime, err := parseTime(end)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}

	// Deep copy and substitute variables in target fields
	target := substituteTemplateVariablesInMapWithValues(panelData.RawTarget, variables)

	// Ensure datasource is set correctly
	target["datasource"] = map[string]interface{}{"uid": datasourceUID, "type": "cloudwatch"}

	// Ensure refId is set
	if safeString(target, "refId") == "" {
		target["refId"] = "A"
	}

	return executeGrafanaDSQuery(ctx, dsQueryPayload(startTime, endTime, target))
}

// executeInfluxDBQuery runs an InfluxDB panel query using queryInfluxDB. The
// panel's queryType field tells us which dialect to use (influxql vs flux);
// fall back to inference from the datasource if the panel doesn't carry one.
func executeInfluxDBQuery(ctx context.Context, datasourceUID string, panelData *panelInfo, query, start, end string) (*InfluxDBQueryResult, error) {
	dialect := ""
	if panelData != nil && panelData.RawTarget != nil {
		if qt := safeString(panelData.RawTarget, "queryType"); qt != "" {
			dialect = qt
		}
	}

	return queryInfluxDB(ctx, InfluxDBQueryParams{
		DatasourceUID: datasourceUID,
		Query:         query,
		Dialect:       dialect,
		Start:         start,
		End:           end,
	})
}

// SQLFormatTable is the format value for table/tabular query results on sqlds-based
// datasources such as BigQuery, whose query model takes a numeric format enum.
const SQLFormatTable = 1

// MSSQLFormatTable is the format value for table/tabular query results on MSSQL, whose
// sqleng-based query model unmarshals format into a string and fails on a number.
const MSSQLFormatTable = "table"

// defaultSQLFormat returns the table format value understood by the given datasource.
func defaultSQLFormat(datasourceType string) interface{} {
	switch normalizeDatasourceType(datasourceType) {
	case "mssql", "postgres", "mysql":
		// MSSQL, PostgreSQL, and MySQL use Grafana's sqleng backend, whose query
		// model unmarshals format into a string and errors on a number.
		return MSSQLFormatTable
	default:
		return SQLFormatTable
	}
}

// executeSQLPanelQuery runs a panel query against a SQL datasource via Grafana's
// /api/ds/query endpoint. Those datasources resolve SQL macros such as
// $__timeFilter/$__timeFrom/$__timeGroup server-side in their backend plugin, so we
// only substitute the frontend-only macros ($__interval, $__range, etc.) here. The
// panel's raw target is preserved so datasource-specific fields (BigQuery's location,
// project and dataset, for example) reach the backend; only rawSql, datasource, refId
// and format are overridden.
func executeSQLPanelQuery(ctx context.Context, datasourceUID string, panelData *panelInfo, query, start, end string, variables templateVariableValues, datasourceType string) (*sqldialect.SQLQueryResult, error) {
	if panelData == nil || panelData.RawTarget == nil {
		return nil, fmt.Errorf("SQL panel target not available")
	}

	// Parse time range
	startTime, err := parseTime(start)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}
	endTime, err := parseTime(end)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}

	// Substitute Grafana frontend macros; leave SQL macros for the backend plugin.
	processedQuery := substituteGrafanaMacros(query, startTime, endTime)

	// Deep copy the raw target and substitute variables in its fields (e.g. location).
	target := substituteTemplateVariablesInMapWithValues(panelData.RawTarget, variables)

	// Override the SQL with the fully-processed query and ensure required fields are set.
	target["rawSql"] = processedQuery
	target["datasource"] = map[string]interface{}{"uid": datasourceUID, "type": datasourceType}
	if safeString(target, "refId") == "" {
		target["refId"] = "A"
	}
	if _, ok := target["format"]; !ok {
		target["format"] = defaultSQLFormat(datasourceType)
	}

	client, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := doDSQuery(ctx, client, baseURL, dsQueryPayload(startTime, endTime, target))
	if err != nil {
		return nil, err
	}

	columns, rows, err := framesToTabularRows(resp)
	if err != nil {
		return nil, err
	}

	return &sqldialect.SQLQueryResult{
		Columns:        columns,
		Rows:           rows,
		RowCount:       len(rows),
		ProcessedQuery: processedQuery,
	}, nil
}

// executeGrafanaDSQuery executes a query through Grafana's /api/ds/query endpoint.
func executeGrafanaDSQuery(ctx context.Context, payload map[string]interface{}) (interface{}, error) {
	client, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := doDSQuery(ctx, client, baseURL, payload)
	if err != nil {
		return nil, err
	}

	return resp.Responses, nil
}

// substituteGrafanaMacros substitutes Grafana temporal macros ($__range, $__rate_interval, $__interval)
// used across datasource types (Prometheus, Loki, etc.)
func substituteGrafanaMacros(query string, start, end time.Time) string {
	duration := end.Sub(start)

	// Substitute $__range_ms and $__range_s BEFORE $__range to avoid partial replacement
	rangeSec := int64(duration.Seconds())
	rangeMs := duration.Milliseconds()
	query = strings.ReplaceAll(query, "${__range_ms}", fmt.Sprintf("%d", rangeMs))
	query = strings.ReplaceAll(query, "$__range_ms", fmt.Sprintf("%d", rangeMs))
	query = strings.ReplaceAll(query, "${__range_s}", fmt.Sprintf("%d", rangeSec))
	query = strings.ReplaceAll(query, "$__range_s", fmt.Sprintf("%d", rangeSec))

	// $__range - total time range as duration string
	rangeStr := formatPrometheusDuration(duration)
	query = strings.ReplaceAll(query, "${__range}", rangeStr)
	query = strings.ReplaceAll(query, "$__range", rangeStr)

	// $__rate_interval - default to "1m"
	query = strings.ReplaceAll(query, "${__rate_interval}", "1m")
	query = strings.ReplaceAll(query, "$__rate_interval", "1m")

	// Calculate interval based on time range / max data points (~100 points)
	interval := duration / 100
	if interval < time.Second {
		interval = time.Second
	}

	// Substitute $__interval_ms BEFORE $__interval to avoid partial replacement
	intervalMs := int64(interval / time.Millisecond)
	query = strings.ReplaceAll(query, "${__interval_ms}", fmt.Sprintf("%d", intervalMs))
	query = strings.ReplaceAll(query, "$__interval_ms", fmt.Sprintf("%d", intervalMs))

	// $__interval - duration string
	intervalStr := formatPrometheusDuration(interval)
	query = strings.ReplaceAll(query, "${__interval}", intervalStr)
	query = strings.ReplaceAll(query, "$__interval", intervalStr)

	return query
}

// formatPrometheusDuration formats a duration for Prometheus (e.g., "14m", "1h30m", "36s")
func formatPrometheusDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if mins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, mins)
}

// isVariableReference checks if a string is a Grafana variable reference
func isVariableReference(s string) bool {
	return strings.HasPrefix(s, "$") || strings.HasPrefix(s, "[[")
}

// extractVariableName extracts the variable name from different reference formats
func extractVariableName(s string) string {
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		return s[2 : len(s)-1]
	}
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		return s[2 : len(s)-2]
	}
	if strings.HasPrefix(s, "$") {
		return strings.TrimPrefix(s, "$")
	}
	return s
}

// getAvailableDatasourceUIDs returns UIDs of datasources matching the given type
func getAvailableDatasourceUIDs(ctx context.Context, dsType string) []string {
	result, err := listDatasources(ctx, ListDatasourcesParams{Type: dsType})
	if err != nil {
		return nil
	}
	datasources := result.Datasources
	// Limit to first 10 to avoid very long error messages
	limit := 10
	if len(datasources) < limit {
		limit = len(datasources)
	}
	uids := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		ds := datasources[i]
		uids = append(uids, fmt.Sprintf("%s (%s)", ds.Name, ds.UID))
	}
	return uids
}

// normalizeDatasourceType maps a datasource API type to a canonical short name.
// Prometheus, Loki, and CloudWatch use exact (case-insensitive) matching;
// ClickHouse uses substring matching because the API type is "grafana-clickhouse-datasource".
func normalizeDatasourceType(dsType string) string {
	lower := strings.ToLower(dsType)
	switch {
	case lower == "prometheus" || lower == "stackdriver":
		return "prometheus"
	case lower == "loki":
		return "loki"
	case lower == "cloudwatch":
		return "cloudwatch"
	case lower == "influxdb":
		return "influxdb"
	case strings.Contains(lower, "clickhouse"):
		return "clickhouse"
	case strings.Contains(lower, "athena"):
		return "athena"
	case strings.Contains(lower, "snowflake"):
		return "snowflake"
	case lower == "mysql":
		return "mysql"
	case lower == "mssql":
		return "mssql"
	case lower == "postgres" || lower == "grafana-postgresql-datasource":
		return "postgres"
	case strings.Contains(lower, "bigquery"):
		return "bigquery"
	default:
		return lower
	}
}

// isEmptyPanelResult checks if the query result is empty
func isEmptyPanelResult(results interface{}) bool {
	if results == nil {
		return true
	}
	switch v := results.(type) {
	case []interface{}:
		return len(v) == 0
	case []LogEntry:
		return len(v) == 0
	case *InfluxDBQueryResult:
		return v == nil || len(v.Rows) == 0
	case *sqldialect.SQLQueryResult:
		return v == nil || len(v.Rows) == 0
	case model.Value:
		switch m := v.(type) {
		case model.Matrix:
			return len(m) == 0
		case model.Vector:
			return len(m) == 0
		}
	}
	return false
}

// generatePanelQueryHints generates helpful hints when panel query returns no data
func generatePanelQueryHints(datasourceType, query string) []string {
	hints := []string{"No data found for the panel query. Possible reasons:"}

	hints = append(hints, "- Time range may have no data - try extending with start='now-6h' or start='now-24h'")

	switch normalizeDatasourceType(datasourceType) {
	case "prometheus":
		hints = append(hints,
			"- Metric may not exist - use list_prometheus_metric_names to discover available metrics",
			"- Label selectors may be too restrictive - try removing some filters",
			"- Prometheus may not have scraped data for this time range",
		)
	case "loki":
		hints = append(hints,
			"- Log stream selectors may not match any streams - use list_loki_label_names to discover labels",
			"- Pipeline filters may be filtering out all logs - try simplifying the query",
			"- Use query_loki_stats to check if logs exist in this time range",
		)
	case "clickhouse":
		hints = append(hints,
			"- Table may be empty for this time range - use query_sql with a COUNT(*) to verify",
			"- Column names or WHERE clause may not match - use describe_sql_table to check schema",
			"- Time filter may not match the actual timestamp column format",
		)
	case "cloudwatch":
		hints = append(hints,
			"- Namespace or metric name may be incorrect - use list_cloudwatch_namespaces and list_cloudwatch_metrics to discover available options",
			"- Dimension filters may not match any resources - use list_cloudwatch_dimensions to check available dimensions",
			"- AWS region may be incorrect - verify the region setting in the datasource",
			"- CloudWatch metrics may have longer retention periods than the selected time range",
		)
	case "influxdb":
		hints = append(hints,
			"- Bucket (v2/Flux) or database (v1/InfluxQL) name in the query may be wrong",
			"- Measurement or field names may not exist - try a broader query like 'from(bucket: \"...\") |> range(start: -1h) |> limit(n: 5)' to inspect available data",
			"- Tag filters may be too restrictive - remove them to see if the measurement has any points",
			"- InfluxDB retention policy may have expired the data - check the bucket's retention settings",
		)
	case "bigquery":
		hints = append(hints,
			"- Table or dataset may be empty for this time range - try a COUNT(*) without the time filter to verify",
			"- Column names or WHERE clause may not match the schema - check the table definition in BigQuery",
			"- The $__timeFilter(column) macro may reference a column whose type or timezone doesn't match the time range",
			"- The datasource's processing location may not match the dataset's region",
		)
	case "mssql":
		hints = append(hints,
			"- Table may be empty for this time range - try a COUNT(*) without the time filter to verify",
			"- Column names or WHERE clause may not match the schema - check the table definition in SQL Server",
			"- The $__timeFilter(column) macro may reference a column whose type or timezone doesn't match the time range",
			"- The datasource login may lack SELECT permission on the referenced tables",
		)
	case "postgres":
		hints = append(hints,
			"- Table may be empty for this time range - try a COUNT(*) without the time filter to verify",
			"- Column names or WHERE clause may not match the schema - check the table definition in PostgreSQL",
			"- The $__timeFilter(column) macro may reference a column whose type or timezone doesn't match the time range",
			"- The datasource role may lack SELECT privilege on the referenced tables, or the schema may not be on the search_path",
		)
	}

	if query != "" {
		hints = append(hints, "- Query executed: "+truncateString(query, 100))
	}

	return hints
}

// truncateString truncates a string to maxLen and adds ellipsis if needed
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// RunPanelQuery is the tool definition for running panel queries
var RunPanelQuery = mcpgrafana.MustTool(
	"run_panel_query",
	"Executes one or more dashboard panel queries with optional time range and variable overrides. Accepts an array of panel IDs to query in a single call. Fetches the dashboard\\, extracts queries from the specified panels\\, substitutes template variables and Grafana macros ($__range\\, $__rate_interval\\, $__interval)\\, and routes to the appropriate datasource (Prometheus\\, Loki\\, ClickHouse\\, CloudWatch\\, InfluxDB\\, BigQuery\\, MSSQL\\, or PostgreSQL). Returns results keyed by panel ID - partial failures are allowed (some panels can succeed while others fail). Use get_dashboard_summary first to find panel IDs. If a panel uses a template variable datasource you cannot access\\, provide datasourceUid and datasourceType to override.",
	runPanelQuery,
	mcp.WithTitleAnnotation("Run panel query"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddRunPanelQueryTools registers run panel query tools with the MCP server.
// Every tool in this category executes a query, so nothing is registered when
// enableQueryTools is false.
func AddRunPanelQueryTools(mcp *server.MCPServer, enableQueryTools bool) {
	if enableQueryTools {
		RunPanelQuery.Register(mcp)
	}
}
