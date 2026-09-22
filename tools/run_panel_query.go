package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

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
	QueryIndex     *int              `json:"queryIndex,omitempty" jsonschema:"description=Index of a single query to execute per panel (0-based). Omit this to run every query the panel has\\, which is what reproduces the panel: a panel's queries are layers of one picture. Use get_dashboard_panel_queries to see them."`
	Start          string            `json:"start" jsonschema:"description=Override start time (e.g. 'now-1h'\\, RFC3339\\, Unix ms)"`
	End            string            `json:"end" jsonschema:"description=Override end time (e.g. 'now'\\, RFC3339\\, Unix ms)"`
	Variables      map[string]string `json:"variables" jsonschema:"description=Override dashboard variables (e.g. {\"job\": \"api-server\"})"`
	DatasourceUID  string            `json:"datasourceUid,omitempty" jsonschema:"description=Override datasource UID"`
	MaxDataPoints  int               `json:"maxDataPoints,omitempty" jsonschema:"description=Prometheus panels only: maximum points per series (default 500). The query step is derived from this and the time range\\, the way a Grafana panel derives its step from its pixel width. Raise it only if you need finer resolution than a chart can show. Other datasources bound their own result size (Loki by entry limit\\, SQL and InfluxDB via the $__interval macro) and ignore this."`
	DatasourceType string            `json:"datasourceType,omitempty" jsonschema:"description=Fallback datasource type used only when the datasource cannot be read (prometheus\\, loki\\, grafana-clickhouse-datasource\\, cloudwatch\\, influxdb\\, grafana-bigquery-datasource\\, mssql\\, grafana-postgresql-datasource\\, postgres). When the datasource is readable its real type is used and this is ignored."`
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

	// Queries holds every query that ran, present only when the panel has more than
	// one. A panel's queries are layers of one picture - max and average of the same
	// metric, say - so returning one of them returns a different panel. Omitting this
	// for single-query panels keeps the common result the shape it has always been.
	Queries []PanelQueryExecution `json:"queries,omitempty"`

	// PanelSpec is how the panel draws, as the classic v1 panel JSON the
	// dashboard already stores: type, title, options and fieldConfig. A viewer
	// that renders these results can only guess at units, thresholds, axis
	// placement and per-series overrides without it, so a panel reconstructed
	// from the query alone looks like a different panel.
	//
	// Queries are left out because they are already above, and transformations
	// because nothing downstream applies them - a panel that has any gets a hint
	// instead of a spec that would quietly overstate the fidelity.
	PanelSpec map[string]interface{} `json:"panelSpec,omitempty"`

	// How many queries the panel declares, so the caller loop knows whether it has
	// them all. Unexported: it is scaffolding for assembling the result, not part of
	// it.
	targetCount int
}

// PanelQueryExecution is one of a panel's queries and what it returned.
type PanelQueryExecution struct {
	RefID   string      `json:"refId,omitempty"`
	Query   string      `json:"query"`
	Results interface{} `json:"results"`
	Hints   []string    `json:"hints,omitempty"`
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
	MaxPoints  int
}

// panelInfo contains extracted information about a panel
type panelInfo struct {
	ID             int
	Title          string
	DatasourceUID  string
	DatasourceType string
	Query          string
	RawTarget      map[string]interface{} // For CloudWatch and other complex query types

	Spec               map[string]interface{} // How the panel draws; see PanelQueryResult.PanelSpec
	HasTransformations bool
	RefID              string // The target's refId, which names this layer of the panel
	TargetCount        int    // How many queries the panel has in total
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
		run := func(index int) (*PanelQueryResult, error) {
			return runSinglePanelQuery(ctx, singlePanelQueryParams{
				DB:         db,
				PanelID:    panelID,
				QueryIndex: index,
				Start:      start,
				End:        end,
				Variables:  args.Variables,
				DsUID:      args.DatasourceUID,
				DsType:     args.DatasourceType,
				MaxPoints:  args.MaxDataPoints,
			})
		}

		result, err := run(queryIndex)
		if err != nil {
			errs[panelID] = err.Error()
			continue
		}

		// Without an explicit queryIndex, run the panel's remaining queries too. A
		// panel's queries are layers of one picture - the max and the average of a
		// metric, say - so answering with the first alone answers with a different
		// panel, and a caller that notices goes looking for the rest.
		if args.QueryIndex == nil {
			for index := 1; index < result.targetCount; index++ {
				next, nextErr := run(index)
				if nextErr != nil {
					// One unrunnable layer does not invalidate the others; say so
					// against the query rather than dropping the whole panel.
					result.Queries = append(result.Queries, PanelQueryExecution{
						Query: fmt.Sprintf("query %d could not run: %s", index, nextErr.Error()),
					})
					continue
				}
				result.Queries = append(result.Queries, next.Queries...)
			}
		}

		// Only worth carrying when there is more than one layer to distinguish.
		if len(result.Queries) < 2 {
			result.Queries = nil
		}

		results[panelID] = result
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

	// Resolve the datasource type authoritatively from its UID whenever the
	// caller overrode the datasource, or when we don't yet have a type. The
	// datasource's real type — not a caller-supplied one — decides which
	// executor runs, because the executors are not equivalent: a Loki
	// datasource routed on a SQL/CloudWatch type would run through
	// executeSQLPanelQuery / executeCloudWatchPanelQuery, which query
	// /api/ds/query directly and so bypass the Loki label-matcher enforcement
	// that is applied only in the native Loki backend (loki_backend.go /
	// loki_enforce.go). A type declared in the panel JSON (no override) is
	// trusted as-is: it comes from the dashboard, not the caller.
	if datasourceUID != "" && (params.DsUID != "" || datasourceType == "") {
		ds, lookupErr := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: datasourceUID})
		switch {
		case lookupErr == nil:
			// The datasource's real type wins over any caller-supplied type.
			datasourceType = ds.Type
		case datasourceType == "":
			// Cannot resolve the type and the caller gave nothing to fall back
			// on.
			availableDS := getAvailableDatasourceUIDs(ctx, "")
			return nil, fmt.Errorf("could not resolve datasource '%s' (%v) and no datasourceType was provided. Hint: provide both 'datasourceUid' and 'datasourceType' to override. Available datasources: %v", datasourceUID, lookupErr, availableDS)
		case len(enforcedMatchers(ctx)) > 0 && normalizeDatasourceType(datasourceType) != "loki":
			// The datasource is unreadable, so the caller-supplied type is
			// unverified. With Loki label-matcher enforcement active, refuse
			// rather than route a possibly-Loki datasource onto the
			// /api/ds/query path, which bypasses enforcement. Fails closed,
			// mirroring the VictoriaLogs guard in lokiBackendForDatasource.
			return nil, fmt.Errorf("refusing to run panel query for datasource '%s': Loki label-matcher enforcement is enabled and the datasource type could not be verified because the datasource is not readable; query Loki via query_loki_logs, or supply an accessible datasource", datasourceUID)
		default:
			// Unreadable datasource, but the caller supplied a fallback type and
			// enforcement (if any) is satisfied; keep the caller-supplied type.
		}
	}

	// Substitute variables in the query
	query := substituteTemplateVariableValues(panelData.Query, templateVariables)

	// Route to appropriate datasource and execute query
	var results interface{}

	switch normalizeDatasourceType(datasourceType) {
	case "prometheus":
		results, err = executePrometheusQuery(ctx, datasourceUID, query, params.Start, params.End, params.MaxPoints)
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

	// Said out loud because the gap is invisible otherwise: the results below are
	// the panel's query output, and a transformation sits between that and what the
	// panel shows, so a value someone is looking for may not appear here at all.
	if panelData.HasTransformations {
		hints = append(hints, "this panel applies transformations, which are not applied to these results; what the panel shows in Grafana may differ")
	}

	return &PanelQueryResult{
		PanelID:        params.PanelID,
		PanelTitle:     panelData.Title,
		DatasourceType: datasourceType,
		DatasourceUID:  datasourceUID,
		Query:          query,
		Results:        results,
		Hints:          hints,
		PanelSpec:      panelData.Spec,
		Queries: []PanelQueryExecution{{
			RefID:   panelData.RefID,
			Query:   query,
			Results: results,
			Hints:   hints,
		}},
		targetCount: panelData.TargetCount,
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

	// Read before the target checks below, so a panel whose query cannot be
	// extracted still reports why rather than failing twice over.
	info.Spec = panelRenderSpec(panel)
	info.HasTransformations = len(safeArray(panel, "transformations")) > 0

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
	info.RefID = safeString(target, "refId")
	info.TargetCount = len(targets)

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

// panelRenderSpec copies the keys that decide how a panel draws into a standalone
// object. Classic v1 panel JSON rather than Dashboard v2 PanelKind, because that is
// what the dashboard stores and what a renderer accepts directly: converting here
// would be a lossy round trip for no reader's benefit.
//
// Returns nil when the panel declares no type. A spec without one cannot select a
// renderer, and a caller is better served by an absent field than by an object that
// looks usable and is not.
func panelRenderSpec(panel map[string]interface{}) map[string]interface{} {
	if safeString(panel, "type") == "" {
		return nil
	}

	spec := make(map[string]interface{}, 4)
	for _, key := range []string{"type", "title", "options", "fieldConfig"} {
		if value, ok := panel[key]; ok && value != nil {
			spec[key] = value
		}
	}

	return spec
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
// defaultMaxDataPoints is the per-series point budget when the caller gives none.
// A few hundred points is all a chart a few hundred pixels wide can show, and it is
// the same reasoning Grafana uses to turn a panel's width into maxDataPoints.
const defaultMaxDataPoints = 500

// minStepSeconds keeps short ranges at the resolution this tool has always used
// rather than making them finer than before.
const minStepSeconds = 60

// prometheusStepSeconds bounds the result size by range rather than leaving the step
// fixed. Prometheus is the only executor here that needs it: Loki caps entries with
// its own limit, and the SQL and InfluxDB paths take their granularity from the
// panel query's own $__interval macro (see substituteGrafanaMacros), so only this
// one chose a server-side step independent of the range. A fixed one-minute step is fine for an hour and ruinous for a week: seven
// days of it is 10,080 points per series, which pushed a six-series panel to 1.9 MB
// and past the tool-output limit of every host, so the caller got nothing at all.
func prometheusStepSeconds(startTime, endTime time.Time, maxPoints int) int {
	if maxPoints <= 0 {
		maxPoints = defaultMaxDataPoints
	}
	rangeSeconds := int(endTime.Sub(startTime).Seconds())
	if rangeSeconds <= 0 {
		return minStepSeconds
	}
	step := (rangeSeconds + maxPoints - 1) / maxPoints // ceil
	if step < minStepSeconds {
		step = minStepSeconds
	}
	return step
}

func executePrometheusQuery(ctx context.Context, datasourceUID, query, start, end string, maxPoints int) (model.Value, error) {
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
		StepSeconds:   prometheusStepSeconds(startTime, endTime, maxPoints),
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

// summarizePanelQueryResult describes a result in the few lines a model needs to
// reason about it: which panels ran, against what, how much came back, and what
// failed. The samples themselves are not summarized because a model cannot do
// anything with a truncated series that it could not do with this.
//
// This exists so the payload does not have to be the model's copy too. A single
// panel over a modest window is tens of thousands of tokens of timestamps, and it
// grows with every panel the caller asks for - the feature gets more expensive the
// better it works.
func summarizePanelQueryResult(result *RunPanelQueryResult) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Ran %d panel quer%s on dashboard %s over %s to %s.\n",
		len(result.Results)+len(result.Errors),
		map[bool]string{true: "y", false: "ies"}[len(result.Results)+len(result.Errors) == 1],
		result.DashboardUID, result.TimeRange.Start, result.TimeRange.End)

	// Sorted, because map iteration order would reshuffle the summary between two
	// identical calls and make a diff of two runs unreadable.
	ids := make([]int, 0, len(result.Results))
	for id := range result.Results {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	for _, id := range ids {
		panel := result.Results[id]
		fmt.Fprintf(&b, "\n- panel %d %q (%s)\n", id, panel.PanelTitle, panel.DatasourceType)

		// Every layer, named, because a panel drawn from one of two queries is a
		// different panel and the caller has to be able to tell that it got both.
		for _, execution := range panelExecutions(panel) {
			label := execution.RefID
			if label == "" {
				label = "query"
			}
			fmt.Fprintf(&b, "  %s: %s | %s\n", label, describePanelResults(execution.Results),
				truncateString(execution.Query, 240))
		}
		if panel.PanelSpec == nil {
			fmt.Fprint(&b, "  no panel spec: a viewer cannot reproduce this panel's appearance\n")
		}
		for _, hint := range panel.Hints {
			fmt.Fprintf(&b, "  hint: %s\n", hint)
		}
	}

	errIDs := make([]int, 0, len(result.Errors))
	for id := range result.Errors {
		errIDs = append(errIDs, id)
	}
	sort.Ints(errIDs)
	for _, id := range errIDs {
		fmt.Fprintf(&b, "\n- panel %d failed: %s\n", id, result.Errors[id])
	}

	fmt.Fprint(&b, "\nThe data itself is attached for the viewer to render; ask for a specific "+
		"panel or a narrower window if you need to read values yourself.\n")

	return b.String()
}

// panelExecutions is every query that ran for a panel, whether it carries the
// multi-query list or only the single-query fields.
func panelExecutions(panel *PanelQueryResult) []PanelQueryExecution {
	if len(panel.Queries) > 0 {
		return panel.Queries
	}

	return []PanelQueryExecution{{Query: panel.Query, Results: panel.Results, Hints: panel.Hints}}
}

// describePanelResults counts what a datasource returned, for the datasources whose
// result shape is known here. Anything else gets the one thing that is always true
// and still worth saying, which is whether it came back empty.
func describePanelResults(results interface{}) string {
	switch v := results.(type) {
	case model.Matrix:
		return fmt.Sprintf("%d series", len(v))
	case model.Vector:
		return fmt.Sprintf("%d samples", len(v))
	case []LogEntry:
		return fmt.Sprintf("%d log lines", len(v))
	case *sqldialect.SQLQueryResult:
		return fmt.Sprintf("%d rows", v.RowCount)
	case *InfluxDBQueryResult:
		return fmt.Sprintf("%d rows", v.RowCount)
	}

	if isEmptyPanelResult(results) {
		return "no data"
	}

	return "data returned"
}

// runPanelQueryApp wraps runPanelQuery so the tool *result* carries
// _meta.ui.resourceUri.
//
// WithUIResource on the tool definition is not sufficient: hosts key inline MCP
// App rendering off the result's metadata, and MustTool's default path marshals a
// struct return straight to a JSON text block with no _meta at all.
//
// Two content items rather than one. The first is a summary addressed to both
// audiences, the second the full payload addressed to the user, which is the
// standard MCP signal for "render this, do not read it into the conversation"
// (`annotations.audience`, base spec, not the Apps extension - the extension's
// `visibility` governs who may *call a tool*, not who reads a content item).
// Audience is advisory, so a host that ignores it still receives everything it
// received before, just preceded by a summary. The payload also rides
// structuredContent, and carries `_meta.ui.kind` so an app can find it without
// depending on content order.
func runPanelQueryApp(ctx context.Context, args RunPanelQueryParams) (*mcp.CallToolResult, error) {
	result, err := runPanelQuery(ctx, args)
	if err != nil {
		return nil, err
	}
	summary := summarizePanelQueryResult(result)
	content := []mcp.Content{
		mcp.TextContent{
			Annotated: mcp.Annotated{
				Annotations: &mcp.Annotations{Audience: []mcp.Role{mcp.RoleAssistant, mcp.RoleUser}},
			},
			Type: "text",
			Text: summary,
		},
	}

	// The samples ride along only where something can draw them. On a host that cannot
	// render the app they are unusable by definition - nothing will plot them, and a
	// model handed tens of thousands of timestamps does not learn anything the summary
	// did not already say. Claude Code's terminal is the case in point: it renders no
	// app, spills a large result to a file, and the agent ends up running jq over it.
	if mcpgrafana.HostRendersApps(ctx) {
		payload, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshal panel query result: %w", err)
		}
		content = append(content, mcp.TextContent{
			Annotated: mcp.Annotated{
				Annotations: &mcp.Annotations{Audience: []mcp.Role{mcp.RoleUser}},
			},
			Meta: mcpgrafana.NewUIContentMeta(mcpgrafana.UIContentKindPanelQuery),
			Type: "text",
			Text: string(payload),
		})
	} else {
		content = append(content, mcp.TextContent{
			Annotated: mcp.Annotated{
				Annotations: &mcp.Annotations{Audience: []mcp.Role{mcp.RoleAssistant, mcp.RoleUser}},
			},
			Type: "text",
			Text: renderUnavailableNote,
		})
	}

	return &mcp.CallToolResult{
		Result: mcp.Result{
			Meta: &mcp.Meta{
				AdditionalFields: map[string]any{
					"ui": map[string]any{"resourceUri": mcpgrafana.PanelEmbedResourceURI},
				},
			},
		},
		Content: content,
		// structuredContent stays populated either way: a host that renders reads the
		// payload from here when it drops content items, and one that does not ignores
		// it without putting it in front of the model.
		StructuredContent: result,
	}, nil
}

// renderUnavailableNote tells the model why it is holding a description rather than a
// chart, so it reports that instead of inventing one or digging for data that is not
// there.
const renderUnavailableNote = "This client does not render Grafana panels inline, so the samples were not sent. " +
	"Say so rather than describing the shape of data you cannot see. To see the panel itself, open it in a client " +
	"that supports MCP Apps, such as Cursor or the Claude Code desktop app."

var RunPanelQuery = mcpgrafana.MustTool(
	"run_panel_query",
	"Executes one or more dashboard panel queries with optional time range and variable overrides. Accepts an array of panel IDs to query in a single call. Fetches the dashboard\\, extracts queries from the specified panels\\, substitutes template variables and Grafana macros ($__range\\, $__rate_interval\\, $__interval)\\, and routes to the appropriate datasource (Prometheus\\, Loki\\, ClickHouse\\, CloudWatch\\, InfluxDB\\, BigQuery\\, MSSQL\\, or PostgreSQL). Returns results keyed by panel ID - partial failures are allowed (some panels can succeed while others fail). Use get_dashboard_summary first to find panel IDs. If a panel uses a template variable datasource you cannot access\\, provide datasourceUid and datasourceType to override.",
	runPanelQueryApp,
	mcp.WithTitleAnnotation("Run panel query"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
	// Hosts that support MCP Apps render the results as live Grafana panels. Hosts
	// that do not simply ignore the metadata and show the text result.
	mcpgrafana.WithUIResource(mcpgrafana.PanelEmbedResourceURI),
)

// AddRunPanelQueryTools registers run panel query tools with the MCP server.
// Every tool in this category executes a query, so nothing is registered when
// enableQueryTools is false.
func AddRunPanelQueryTools(mcp *server.MCPServer, enableQueryTools bool) {
	if enableQueryTools {
		RunPanelQuery.Register(mcp)
	}
}
