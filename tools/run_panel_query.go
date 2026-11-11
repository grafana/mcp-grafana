package tools

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/internal/datasource"
	sqldialect "github.com/grafana/mcp-grafana/v2/tools/sql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/common/model"
)

// RunPanelQueryParams defines parameters for running panel queries
type RunPanelQueryParams struct {
	DashboardUID   string            `json:"dashboardUid" jsonschema:"required,description=Dashboard UID"`
	PanelIDs       []int             `json:"panelIds" jsonschema:"required,description=Panel IDs to execute (one or more)"`
	QueryIndex     *int              `json:"queryIndex,omitempty" jsonschema:"description=Index of the query to execute per panel (0-based\\, defaults to 0)."`
	Start          string            `json:"start" jsonschema:"description=Override start time (e.g. 'now-1h'\\, RFC3339\\, Unix ms)"`
	End            string            `json:"end" jsonschema:"description=Override end time (e.g. 'now'\\, RFC3339\\, Unix ms)"`
	Variables      map[string]string `json:"variables" jsonschema:"description=Override dashboard variables (e.g. {\"job\": \"api-server\"})"`
	DatasourceUID  string            `json:"datasourceUid,omitempty" jsonschema:"description=Override datasource UID"`
	DatasourceType string            `json:"datasourceType,omitempty" jsonschema:"description=Fallback datasource type used only when the datasource cannot be read (prometheus\\, loki\\, grafana-clickhouse-datasource\\, cloudwatch\\, influxdb\\, grafana-bigquery-datasource\\, mssql\\, grafana-postgresql-datasource\\, postgres\\, elasticsearch). When the datasource is readable its real type is used and this is ignored."`
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
	IsV2       bool
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
	// Interval holds the panel's options; runSinglePanelQuery adds the datasource's min interval.
	Interval intervalOptions
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
			IsV2:       dashboard.IsV2,
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
	// Find the panel by ID and extract its query and datasource info. The rest
	// of this function reads v1 variables, so a v2 dashboard's are converted.
	db := params.DB
	var panelData *panelInfo
	var err error
	if params.IsV2 {
		panel, findErr := findPanelByIDV2(params.DB, params.PanelID)
		if findErr != nil {
			return nil, fmt.Errorf("finding panel: %w", findErr)
		}
		panelData, err = extractPanelInfoV2(panel, params.QueryIndex)
		db = templatingV1FromV2(params.DB)
	} else {
		panel, findErr := findPanelByID(params.DB, params.PanelID)
		if findErr != nil {
			return nil, fmt.Errorf("finding panel: %w", findErr)
		}
		panelData, err = extractPanelInfo(panel, params.QueryIndex)
	}
	if err != nil {
		return nil, fmt.Errorf("extracting panel info: %w", err)
	}

	// Resolve the dashboard's template variables, with the caller's overrides,
	// and the interval variables set to "auto" for the query's time range.
	interp := newDashboardInterpolator(db, params.Variables)
	interp.resolveAutoIntervals(db, params.Start, params.End)
	templateVariables := interp.values
	referenceDatasource := panelData.DatasourceUID
	if params.DsUID != "" {
		referenceDatasource = params.DsUID
	}
	// Executors replace the target datasource with the resolved UID below.
	referenceTarget := maps.Clone(panelData.RawTarget)
	delete(referenceTarget, "datasource")
	references := variableSearchText(panelQuery{Query: panelData.Query, Target: referenceTarget})
	for _, variable := range findVariablesInQuery(references+" "+referenceDatasource, nil, nil) {
		if warning := interp.allSelectionWarning(variable.Name); warning != "" {
			return nil, errors.New(warning)
		}
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
		if resolvedUID, ok := interp.firstValue(varName); ok && resolvedUID != "" {
			datasourceUID = resolvedUID
			// Reset type so it gets looked up from the resolved datasource
			datasourceType = ""
		} else if defaultUID, ok := defaultDatasourceVariableValue(ctx, db, varName); ok {
			datasourceUID = defaultUID
			datasourceType = ""
		} else {
			availableDS := getAvailableDatasourceUIDs(ctx, panelData.DatasourceType)
			return nil, fmt.Errorf("datasource variable '%s' not found. Hint: Use 'datasourceUid' and 'datasourceType' to override. Available %s datasources: %v", datasourceUID, panelData.DatasourceType, availableDS)
		}
	}

	// Grafana resolves the "default" datasource reference to the org's default
	// datasource; it is not a real UID the datasource API knows about.
	if datasourceUID == "default" {
		ds, err := datasource.Default(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolving default datasource: %w", err)
		}
		datasourceUID = ds.UID
		datasourceType = ""
	}

	var datasourceJSONData interface{}
	// Resolve the datasource type authoritatively from its UID whenever the
	// caller overrode the datasource, when we don't yet have a type, or when
	// Prometheus enforcement requires verifying the panel's declared type. The
	// datasource's real type — not a caller-supplied one — decides which
	// executor runs, because the executors build different queries. Loki
	// label-matcher enforcement does not depend on this routing: doDSQuery
	// resolves every target from its UID and enforces or refuses it there. A
	// type declared in panel JSON is trusted only without Prometheus enforcement.
	if datasourceUID != "" && (params.DsUID != "" || datasourceType == "" || len(prometheusEnforcedMatchers(ctx)) > 0) {
		ds, lookupErr := datasource.ByUID(ctx, datasourceUID)
		if lookupErr != nil && params.DsUID == "" {
			// Dashboards saved before schema version 33 reference a
			// datasource, or a datasource variable's value, by name.
			if byName, err := datasource.ByName(ctx, datasourceUID); err == nil {
				ds, lookupErr = byName, nil
				datasourceUID = byName.UID
			}
		}
		switch {
		case lookupErr == nil:
			// The datasource's real type wins over any caller-supplied type.
			datasourceType = ds.Type
			datasourceJSONData = ds.JSONData
		case datasourceType == "":
			// Cannot resolve the type and the caller gave nothing to fall back
			// on.
			availableDS := getAvailableDatasourceUIDs(ctx, "")
			return nil, fmt.Errorf("could not resolve datasource '%s' (%v) and no datasourceType was provided. Hint: provide both 'datasourceUid' and 'datasourceType' to override. Available datasources: %v", datasourceUID, lookupErr, availableDS)
		case len(prometheusEnforcedMatchers(ctx)) > 0 && normalizeDatasourceType(datasourceType) != "prometheus":
			// The datasource is unreadable, so the caller-supplied type is
			// unverified. With Prometheus label-matcher enforcement active, refuse
			// rather than route a possibly-Prometheus datasource onto the
			// /api/ds/query path, which bypasses enforcement.
			return nil, fmt.Errorf("refusing to run panel query for datasource '%s': Prometheus label-matcher enforcement is enabled and the datasource type could not be verified because the datasource is not readable; query Prometheus via query_prometheus, or supply an accessible datasource", datasourceUID)
		default:
			// Unreadable datasource, but the caller supplied a fallback type;
			// keep it. Under Loki label-matcher enforcement the executor's
			// query still fails closed in doDSQuery, which cannot verify the
			// type either.
		}
	}

	startTime, err := parseTime(params.Start)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}
	endTime, err := parseTime(params.End)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}
	panelData.Interval = panelData.Interval.withLowerLimit(interp, datasourceType, datasourceJSONData)
	// Executors receive the expression with frontend macros and variables substituted.
	query := preparePanelExpression(interp, panelData.Query, datasourceType, startTime, endTime, panelData.Interval)

	// Route to appropriate datasource and execute query
	var results interface{}
	// Structured targets (CloudWatch, Infinity) have no query expression; the executors
	// return the substituted target so the fetched URL can be reported as the query.
	var target map[string]interface{}

	switch normalizeDatasourceType(datasourceType) {
	case "prometheus":
		results, err = executePrometheusQuery(ctx, datasourceUID, query, params.Start, params.End)
	case "loki":
		results, err = executeLokiQuery(ctx, datasourceUID, query, params.Start, params.End, panelData.Interval)
	case "clickhouse":
		results, err = executeClickHouseQuery(ctx, datasourceUID, query, params.Start, params.End)
	case "cloudwatch":
		results, target, err = executeCloudWatchPanelQuery(ctx, datasourceUID, panelData, params.Start, params.End, templateVariables)
	case "infinity":
		// Pass the resolved type through, so a renamed or forked plugin ID reaches Grafana as declared.
		results, target, err = executeInfinityPanelQuery(ctx, datasourceUID, datasourceType, panelData, params.Start, params.End, templateVariables)
	case Neo4jDatasourceType:
		results, target, err = executeStructuredPanelQuery(ctx, datasourceUID, Neo4jDatasourceType, panelData, params.Start, params.End, templateVariables)
		if err == nil {
			// Report the Cypher that ran, with macros substituted as well as variables.
			query = safeString(target, "cypherQuery")
		}
	case elasticsearchDatasourceType:
		results, target, err = executeElasticsearchPanelQuery(ctx, datasourceUID, panelData, params.Start, params.End, interp)
		if err == nil {
			// Report the Lucene query that ran, with macros substituted as well as variables.
			query = safeString(target, "query")
		}
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
		return nil, fmt.Errorf("datasource type '%s' is not supported by run_panel_query; use the native query tool (e.g. query_prometheus, query_loki_logs, query_sql, query_cloudwatch, query_influxdb) directly", datasourceType)
	}

	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}
	if query == "" && target != nil {
		query = safeString(target, "url")
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

// panelDatasourceRef reads a panel or target datasource reference. Dashboards
// saved before schema version 33 store it as a plain string: a datasource name
// or a variable. runSinglePanelQuery resolves a name to its UID.
func panelDatasourceRef(ref interface{}) (uid, dsType string) {
	switch ref := ref.(type) {
	case map[string]interface{}:
		return safeString(ref, "uid"), safeString(ref, "type")
	case string:
		if ref != "-- Mixed --" {
			return ref, ""
		}
	}
	return "", ""
}

// extractPanelInfo extracts query and datasource information from a panel
func extractPanelInfo(panel map[string]interface{}, queryIndex int) (*panelInfo, error) {
	info := &panelInfo{
		ID:       safeInt(panel, "id"),
		Title:    safeString(panel, "title"),
		Interval: panelIntervalOptions(panel),
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
	info.DatasourceUID, info.DatasourceType = panelDatasourceRef(target["datasource"])

	// Fall back to panel-level datasource
	if info.DatasourceUID == "" {
		info.DatasourceUID, info.DatasourceType = panelDatasourceRef(panel["datasource"])
	}

	// Grafana runs a panel without any datasource reference on the default one.
	if info.DatasourceUID == "" && target["datasource"] == nil && panel["datasource"] == nil {
		info.DatasourceUID = "default"
	}

	if info.DatasourceUID == "" {
		return nil, fmt.Errorf("could not determine datasource for panel")
	}

	// Try to get query expression - different datasources use different field names
	query := panelQueryExpression(target, info.DatasourceType)

	// CloudWatch and Infinity panels use structured targets rather than string expressions
	if query == "" && !usesStructuredTarget(info.DatasourceType) {
		return nil, errNoQueryExpression
	}
	info.Query = query

	return info, nil
}

// resolveAutoIntervalVariables gives interval variables set to "auto" the value
// Grafana computes for the query's time range. Grafana stores the selection as
// "$__auto_interval_<name>" and fills it in from the range, the variable's step
// count and its minimum interval; left as is, the placeholder reaches the
// datasource as an invalid duration. The legacy $__auto_interval_<name> and
// $__auto_interval references resolve to the same value.
func resolveAutoIntervalVariables(db map[string]interface{}, values templateVariableValues, start, end string) {
	from, err := parseTime(start)
	if err != nil {
		return
	}
	to, err := parseTime(end)
	if err != nil {
		return
	}
	for _, item := range safeArray(safeObject(db, "templating"), "list") {
		variable, ok := item.(map[string]interface{})
		if !ok || safeString(variable, "type") != "interval" {
			continue
		}
		name := safeString(variable, "name")
		if auto, _ := variable["auto"].(bool); !auto || name == "" {
			continue
		}
		steps := 30.0
		if count, ok := variable["auto_count"].(float64); ok && count > 0 {
			steps = count
		}
		minimum := "10s"
		if m, ok := variable["auto_min"].(string); ok && m != "" {
			minimum = m
		}
		interval := grafanaCalculateInterval(to.Sub(from), steps, minimum)
		placeholder := "$__auto_interval_" + name
		if current := values[name]; len(current) == 1 && current[0] == placeholder {
			values[name] = []string{interval}
		}
		values["__auto_interval_"+name] = []string{interval}
		values["__auto_interval"] = []string{interval}
	}
}

// grafanaCalculateInterval mirrors rangeutil.calculateInterval in Grafana: the
// range divided by the step count, rounded to a "nice" interval, no lower than
// the minimum, and formatted with secondsToHms.
func grafanaCalculateInterval(span time.Duration, steps float64, minimum string) string {
	return grafanaSecondsToHms(grafanaIntervalMs(span, steps, minimum) / 1000)
}

// grafanaIntervalMs is the intervalMs of rangeutil.calculateInterval.
func grafanaIntervalMs(span time.Duration, steps float64, minimum string) float64 {
	ms := grafanaRoundInterval(float64(span.Milliseconds()) / steps)
	if low, err := gtime.ParseInterval(minimum); err == nil && float64(low.Milliseconds()) > ms {
		ms = float64(low.Milliseconds())
	}
	return ms
}

// grafanaRoundInterval is rangeutil.roundInterval: thresholds in milliseconds
// and the interval each one rounds to.
func grafanaRoundInterval(ms float64) float64 {
	steps := [][2]float64{
		{10, 1}, {15, 10}, {35, 20}, {75, 50}, {150, 100}, {350, 200}, {750, 500},
		{1500, 1000}, {3500, 2000}, {7500, 5000}, {12500, 10000}, {17500, 15000},
		{25000, 20000}, {45000, 30000}, {90000, 60000}, {210000, 120000},
		{450000, 300000}, {750000, 600000}, {1050000, 900000}, {1500000, 1200000},
		{2700000, 1800000}, {5400000, 3600000}, {9000000, 7200000},
		{16200000, 10800000}, {32400000, 21600000}, {86400000, 43200000},
		{604800000, 86400000}, {1814400000, 604800000}, {3628800000, 2592000000},
	}
	for _, s := range steps {
		if ms < s[0] {
			return s[1]
		}
	}
	return 31536000000
}

// grafanaSecondsToHms is rangeutil.secondsToHms: the largest whole unit only.
func grafanaSecondsToHms(seconds float64) string {
	s := int64(math.Floor(seconds))
	switch {
	case s/31536000 > 0:
		return fmt.Sprintf("%dy", s/31536000)
	case s%31536000/86400 > 0:
		return fmt.Sprintf("%dd", s%31536000/86400)
	case s%86400/3600 > 0:
		return fmt.Sprintf("%dh", s%86400/3600)
	case s%3600/60 > 0:
		return fmt.Sprintf("%dm", s%3600/60)
	case s%60 > 0:
		return fmt.Sprintf("%ds", s%60)
	}
	if ms := int64(math.Floor(seconds * 1000)); ms > 0 {
		return fmt.Sprintf("%dms", ms)
	}
	return "less than a millisecond"
}

// executePrometheusQuery runs a Prometheus query using the existing queryPrometheus function
func executePrometheusQuery(ctx context.Context, datasourceUID, query, start, end string) (model.Value, error) {
	// Parse time range for the step
	startTime, err := parseTime(start)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}
	endTime, err := parseTime(end)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}

	return queryPrometheus(ctx, QueryPrometheusParams{
		DatasourceUID: datasourceUID,
		Expr:          query,
		StartTime:     start,
		EndTime:       end,
		StepSeconds:   panelPrometheusStep(ctx, endTime.Sub(startTime)),
		QueryType:     "range",
	})
}

// panelPrometheusStep is a 1-minute step, widened for long ranges to stay
// within Prometheus' per-series limit, as Grafana would, or within a lower
// enforced guardrail points cap. The tool has no step parameter, so a points
// block would leave the caller nothing to change. Shadow mode only observes,
// so it keeps the step and records the would-be block.
func panelPrometheusStep(ctx context.Context, window time.Duration) int {
	limit := int64(prometheusMaxPointsPerSeries)
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	if mode := config.PrometheusGuardrailMode; mode != "" && mode != mcpgrafana.PrometheusGuardrailOff && mode != mcpgrafana.PrometheusGuardrailShadow && config.PrometheusGuardrailMaxPoints > 0 {
		limit = min(limit, config.PrometheusGuardrailMaxPoints)
	}
	return int(max(60, promMinStepSeconds(max(window, 0), limit)))
}

// executeLokiQuery runs a Loki query using the existing queryLokiLogs function
func executeLokiQuery(ctx context.Context, datasourceUID, query, start, end string, options intervalOptions) ([]LogEntry, error) {
	// Convert relative times to RFC3339 for Loki
	startTime, err := parseTime(start)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}
	endTime, err := parseTime(end)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}

	result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
		DatasourceUID: datasourceUID,
		LogQL:         query,
		StartRFC3339:  startTime.Format("2006-01-02T15:04:05Z07:00"),
		EndRFC3339:    endTime.Format("2006-01-02T15:04:05Z07:00"),
		Limit:         100,
		Direction:     "backward",
		QueryType:     "range",
		// $__auto is substituted with this interval, and Grafana defines it as the step.
		step: macroInterval(startTime, endTime, options),
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
func executeCloudWatchPanelQuery(ctx context.Context, datasourceUID string, panelData *panelInfo, start, end string, variables templateVariableValues) (interface{}, map[string]interface{}, error) {
	if panelData == nil || panelData.RawTarget == nil {
		return nil, nil, fmt.Errorf("cloudwatch panel target not available")
	}

	// Check for math expression panels
	if dsField := safeObject(panelData.RawTarget, "datasource"); dsField != nil {
		if dsType := safeString(dsField, "type"); dsType == "__expr__" || dsType == "expression" {
			return nil, nil, fmt.Errorf("math expression panels require executing multiple queries; use query_cloudwatch directly for the underlying metrics")
		}
	}

	return executeStructuredPanelQuery(ctx, datasourceUID, "cloudwatch", panelData, start, end, variables)
}

// executeStructuredPanelQuery runs a structured panel target (CloudWatch, Infinity) through
// Grafana's /api/ds/query endpoint. Template variables and Grafana macros are substituted in
// every string field of a deep copy of the target, the datasource is set to the resolved UID
// and type, and refId defaults to "A". The substituted target is returned with the responses.
func executeStructuredPanelQuery(ctx context.Context, datasourceUID, datasourceType string, panelData *panelInfo, start, end string, variables templateVariableValues) (interface{}, map[string]interface{}, error) {
	if panelData == nil || panelData.RawTarget == nil {
		return nil, nil, fmt.Errorf("panel target not available")
	}

	startTime, err := parseTime(start)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing start time: %w", err)
	}
	endTime, err := parseTime(end)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing end time: %w", err)
	}

	// Macros first, so a variable value that looks like a macro stays literal.
	interp := newVariableInterpolator(variables, nil)
	target := mapStrings(panelData.RawTarget, func(s string) string {
		return interp.interpolate(substituteStructuredTargetMacros(s, startTime, endTime, panelData.Interval))
	}).(map[string]interface{})
	return queryStructuredTarget(ctx, datasourceUID, datasourceType, target, startTime, endTime)
}

// queryStructuredTarget sets the datasource and default refId of target, a deep copy of a panel
// target with macros and variables substituted, and runs it through /api/ds/query.
func queryStructuredTarget(ctx context.Context, datasourceUID, datasourceType string, target map[string]interface{}, startTime, endTime time.Time) (interface{}, map[string]interface{}, error) {
	target["datasource"] = map[string]interface{}{"uid": datasourceUID, "type": datasourceType}
	if safeString(target, "refId") == "" {
		target["refId"] = "A"
	}

	results, err := executeGrafanaDSQuery(ctx, dsQueryPayload(startTime, endTime, target))
	return results, target, err
}

// timeRangeMacroRe matches $__from, $__to, ${__from} and ${__to} as whole tokens, so
// plugin-side macros such as $__fromTime and formatted forms such as ${__from:date} are kept.
var timeRangeMacroRe = regexp.MustCompile(`\$\{__(from|to)\}|\$__(from|to)\b`)

// substituteStructuredTargetMacros applies substituteGrafanaMacros and replaces $__from and
// $__to with the range bounds in epoch milliseconds, as Grafana's frontend does before it
// sends a structured target to /api/ds/query.
func substituteStructuredTargetMacros(s string, start, end time.Time, options intervalOptions) string {
	s = substituteGrafanaMacros(s, start, end, options)
	return timeRangeMacroRe.ReplaceAllStringFunc(s, func(match string) string {
		if strings.Contains(match, "from") {
			return strconv.FormatInt(start.UnixMilli(), 10)
		}
		return strconv.FormatInt(end.UnixMilli(), 10)
	})
}

// InfinityDatasourceType is the plugin ID of the Infinity datasource (yesoreyeram-infinity-datasource),
// which fetches JSON, CSV, XML, GraphQL or HTML from a URL or inline content.
const InfinityDatasourceType = "yesoreyeram-infinity-datasource"

// panelQueryExpression returns the query string of a panel target. Neo4j targets are read
// from cypherQuery directly, so a leftover field such as query (copied or migrated panels)
// cannot stand in for the Cypher that runs.
func panelQueryExpression(target map[string]interface{}, datasourceType string) string {
	switch normalizeDatasourceType(datasourceType) {
	case Neo4jDatasourceType:
		return safeString(target, "cypherQuery")
	case elasticsearchDatasourceType:
		return safeString(target, "query")
	}
	return extractQueryExpression(target)
}

// usesStructuredTarget reports whether a datasource keeps its query in structured
// target fields rather than in a string expression such as expr or rawSql.
func usesStructuredTarget(datasourceType string) bool {
	switch normalizeDatasourceType(datasourceType) {
	// An Elasticsearch target with an empty Lucene query matches all documents.
	case "cloudwatch", "infinity", elasticsearchDatasourceType:
		return true
	default:
		return false
	}
}

// Infinity query types the plugin backend parses with the "backend" parser (the plugin's
// isBackendQuerySupported), and the subset it also parses with "jq-backend". A missing type
// is "json", the plugin default.
var (
	infinityBackendTypes   = map[string]bool{"json": true, "csv": true, "tsv": true, "xml": true, "graphql": true, "html": true}
	infinityJQBackendTypes = map[string]bool{"json": true, "xml": true, "graphql": true, "html": true}
)

// infinityBackendQueryError reports why the Infinity plugin backend would not execute the
// target, mirroring the plugin's isBackendQuery: data types need the backend or jq-backend
// parser; google-sheets always runs in the backend; transformations run in the backend but
// only over the panel's other queries; series, global, uql and groq run in the browser, where
// Grafana's API would return the response untransformed.
func infinityBackendQueryError(target map[string]interface{}) error {
	typ := safeString(target, "type")
	if typ == "" {
		typ = "json"
	}
	switch typ {
	case "google-sheets":
		return nil
	case "transformations":
		return fmt.Errorf("infinity transformations query combines the panel's other queries inside the plugin and cannot run on its own; run those queries instead")
	case "series":
		return fmt.Errorf("infinity series query generates random-walk data in the browser; there is nothing to execute through the API")
	case "global":
		return fmt.Errorf("infinity global query is resolved from the datasource settings in the browser and cannot be executed through the API")
	case "uql", "groq":
		return fmt.Errorf("infinity %q query is transformed in the browser; only the backend and jq-backend parsers can be executed through the API", typ)
	}
	if !infinityBackendTypes[typ] {
		return fmt.Errorf("unsupported infinity query type %q", typ)
	}

	parser := safeString(target, "parser")
	if parser == "" {
		parser = "simple"
	}
	switch parser {
	case "backend":
		return nil
	case "jq-backend":
		if infinityJQBackendTypes[typ] {
			return nil
		}
		return fmt.Errorf("infinity %q query with the jq-backend parser runs in the browser; use the backend parser", typ)
	default:
		return fmt.Errorf("infinity panel uses the %q parser, which runs in the browser; only the backend and jq-backend parsers can be executed through the API. Change the query's parser in the panel", parser)
	}
}

// executeInfinityPanelQuery runs an Infinity panel target through Grafana's /api/ds/query
// endpoint. The target is sent as saved (URL, root_selector, parser, headers, ...) with
// template variables and Grafana macros substituted, so the plugin backend does the fetch
// and the parsing.
func executeInfinityPanelQuery(ctx context.Context, datasourceUID, datasourceType string, panelData *panelInfo, start, end string, variables templateVariableValues) (interface{}, map[string]interface{}, error) {
	if panelData == nil || panelData.RawTarget == nil {
		return nil, nil, fmt.Errorf("infinity panel target not available")
	}
	if err := infinityBackendQueryError(panelData.RawTarget); err != nil {
		return nil, nil, err
	}
	return executeStructuredPanelQuery(ctx, datasourceUID, datasourceType, panelData, start, end, variables)
}

// Neo4jDatasourceType is the plugin ID of the Neo4j datasource (kniepdennis-neo4j-datasource),
// which runs the Cypher query in the target's cypherQuery field. Only this plugin is routed:
// run_panel_query is read-only because this plugin opens its Neo4j sessions in read access
// mode, which another Neo4j plugin need not do.
const Neo4jDatasourceType = "kniepdennis-neo4j-datasource"

// executeElasticsearchPanelQuery runs an Elasticsearch panel target through /api/ds/query,
// interpolated as Grafana's Elasticsearch datasource does in applyTemplateVariables: the Lucene
// query and the queries of filters aggregations in the lucene format, the rest of the target
// generically. The target also carries intervalMs, the interval $__interval stands for: the
// plugin sizes auto date histograms from it, and without it uses one second, which exceeds
// Elasticsearch's bucket limit on ranges longer than about 18 hours.
func executeElasticsearchPanelQuery(ctx context.Context, datasourceUID string, panelData *panelInfo, start, end string, interp *variableInterpolator) (interface{}, map[string]interface{}, error) {
	if panelData == nil || panelData.RawTarget == nil {
		return nil, nil, fmt.Errorf("panel target not available")
	}
	raw := panelData.RawTarget
	if safeString(raw, "queryType") == "esql" {
		return nil, nil, fmt.Errorf("elasticsearch ES|QL panels get their time range filter from Grafana's frontend and cannot run through the API; run the query in Explore instead")
	}
	startTime, err := parseTime(start)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing start time: %w", err)
	}
	endTime, err := parseTime(end)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing end time: %w", err)
	}

	// Macros first, so a variable value that looks like a macro stays literal.
	raw = mapStrings(raw, func(s string) string {
		return substituteStructuredTargetMacros(s, startTime, endTime, panelData.Interval)
	}).(map[string]interface{})
	lucene := func(s string) string { return interp.interpolateQuery(s, elasticsearchDatasourceType) }
	target := interp.interpolateTarget(raw)
	target["query"] = lucene(safeString(raw, "query"))
	// The copy has the shape of raw, so its filters are found at the same indexes.
	aggs := safeArray(target, "bucketAggs")
	for i, agg := range safeArray(raw, "bucketAggs") {
		rawAgg, ok := agg.(map[string]interface{})
		if !ok || safeString(rawAgg, "type") != "filters" {
			continue
		}
		filters := safeArray(safeObject(aggs[i].(map[string]interface{}), "settings"), "filters")
		for j, filter := range safeArray(safeObject(rawAgg, "settings"), "filters") {
			if rawFilter, ok := filter.(map[string]interface{}); ok {
				query := lucene(safeString(rawFilter, "query"))
				if query == "" {
					query = "*" // matches all documents, as in Grafana
				}
				filters[j].(map[string]interface{})["query"] = query
			}
		}
	}
	target["intervalMs"] = macroInterval(startTime, endTime, panelData.Interval).Milliseconds()

	return queryStructuredTarget(ctx, datasourceUID, elasticsearchDatasourceType, target, startTime, endTime)
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
// $__timeFilter/$__timeFrom/$__timeGroup server-side in their backend plugin, so the
// query arrives with only frontend macros ($__interval, $__range, etc.) expanded. The
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

	// Deep copy the raw target and substitute variables in its fields (e.g. location).
	target := newVariableInterpolator(variables, nil).interpolateTarget(panelData.RawTarget)

	// Override the SQL with the fully-processed query and ensure required fields are set.
	target["rawSql"] = query
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
		ProcessedQuery: query,
	}, nil
}

// executeGrafanaDSQuery executes a query through Grafana's /api/ds/query endpoint. Grafana
// answers 200 even when the datasource fails a query (unreachable URL, upstream 404, bad
// query) and reports that per refId, so those failures are returned as an error here.
func executeGrafanaDSQuery(ctx context.Context, payload map[string]interface{}) (interface{}, error) {
	client, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := doDSQuery(ctx, client, baseURL, payload)
	if err != nil {
		return nil, err
	}
	if err := dsQueryResponsesError(resp.Responses); err != nil {
		return nil, err
	}

	return resp.Responses, nil
}

// dsQueryResponsesError collects the per-refId failures of a /api/ds/query response: a
// response whose error is set, or whose status is set and not 2xx.
func dsQueryResponsesError(responses backend.Responses) error {
	var failures []string
	for _, refID := range slices.Sorted(maps.Keys(responses)) {
		r := responses[refID]
		source := ""
		if r.ErrorSource != "" {
			source = fmt.Sprintf(" (%s)", r.ErrorSource)
		}
		switch {
		case r.Error != nil:
			failures = append(failures, fmt.Sprintf("refId %s%s: %v", refID, source, r.Error))
		case r.Status != 0 && (r.Status < 200 || r.Status >= 300):
			failures = append(failures, fmt.Sprintf("refId %s%s: status %d", refID, source, r.Status))
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return errors.New(strings.Join(failures, "; "))
}

// intervalOptions are the panel query inputs to Grafana's calculateInterval.
type intervalOptions struct {
	maxDataPoints float64
	minInterval   string
}

// defaultMaxDataPoints stands in for the panel width in pixels, which Grafana
// uses when the panel sets no max data points and is unknown server side.
const defaultMaxDataPoints = 100

// panelIntervalOptions reads maxDataPoints and interval from a v1 panel or v2 queryOptions.
func panelIntervalOptions(options map[string]interface{}) intervalOptions {
	maxDataPoints, _ := options["maxDataPoints"].(float64)
	return intervalOptions{maxDataPoints: maxDataPoints, minInterval: safeString(options, "interval")}
}

// panelIntervalOptionsV2 reads them from a v2 panel's data.spec.queryOptions.
func panelIntervalOptionsV2(panel map[string]interface{}) intervalOptions {
	return panelIntervalOptions(safeObject(safeObject(safeObject(panel, "data"), "spec"), "queryOptions"))
}

// withLowerLimit picks the lower limit as SceneQueryRunner.prepareRequests does:
// the panel's interpolated min interval if set, else the datasource's.
func (o intervalOptions) withLowerLimit(interp *variableInterpolator, datasourceType string, jsonData interface{}) intervalOptions {
	if o.minInterval != "" {
		o.minInterval = interp.interpolate(o.minInterval)
	} else {
		o.minInterval = datasourceMinInterval(datasourceType, jsonData)
	}
	return o
}

// datasourceMinInterval is the plugin's DataSourceApi.interval: jsonData.timeInterval,
// else 15s for Prometheus and 1m for the SQL datasources. Other plugins set none.
func datasourceMinInterval(datasourceType string, jsonData interface{}) string {
	var fallback string
	switch strings.ToLower(datasourceType) {
	case "prometheus":
		fallback = "15s"
	case "postgres", "grafana-postgresql-datasource", "mysql", "mssql":
		fallback = "1m"
	default:
		return ""
	}
	settings, _ := jsonData.(map[string]interface{})
	if interval := safeString(settings, "timeInterval"); interval != "" {
		return interval
	}
	return fallback
}

// macroInterval is $__interval and $__auto: Grafana's calculateInterval, rounded to a
// nice value and no lower than the min interval (1ms when unset).
func macroInterval(start, end time.Time, options intervalOptions) time.Duration {
	steps := options.maxDataPoints
	if steps <= 0 {
		steps = defaultMaxDataPoints
	}
	return time.Duration(grafanaIntervalMs(end.Sub(start), steps, options.minInterval)) * time.Millisecond
}

var grafanaTemporalMacroRE = regexp.MustCompile(`\$(?:\{__\w+\}|__\w+\b)`)

// substituteGrafanaMacros expands only whole frontend temporal macro names.
func substituteGrafanaMacros(query string, start, end time.Time, options intervalOptions) string {
	duration := end.Sub(start)
	interval := macroInterval(start, end, options)
	intervalStr := grafanaSecondsToHms(interval.Seconds())
	values := map[string]string{
		"__range_ms":         fmt.Sprintf("%d", duration.Milliseconds()),
		"__range_s":          fmt.Sprintf("%d", int64(duration.Seconds())),
		"__range":            formatPrometheusDuration(duration),
		"__rate_interval_ms": "60000",
		"__rate_interval":    "1m",
		"__interval_ms":      fmt.Sprintf("%d", int64(interval/time.Millisecond)),
		"__interval":         intervalStr,
		"__auto":             intervalStr,
	}
	return grafanaTemporalMacroRE.ReplaceAllStringFunc(query, func(match string) string {
		// Match whole names so a variable such as $__interval_env survives.
		if value, ok := values[strings.Trim(match, "${}")]; ok {
			return value
		}
		return match
	})
}

// formatPrometheusDuration formats a duration for Prometheus and Loki (e.g. "14m", "1h30m",
// "36s", "1m30s"). Every unit is kept, so the result matches the _s and _ms macros; only
// sub-second precision is dropped.
func formatPrometheusDuration(d time.Duration) string {
	secs := int64(d / time.Second)
	hours, mins, rem := secs/3600, secs/60%60, secs%60
	var b strings.Builder
	if hours > 0 {
		fmt.Fprintf(&b, "%dh", hours)
	}
	if mins > 0 {
		fmt.Fprintf(&b, "%dm", mins)
	}
	if rem > 0 || b.Len() == 0 {
		fmt.Fprintf(&b, "%ds", rem)
	}
	return b.String()
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

// defaultDatasourceVariableValue resolves a datasource variable saved without a
// value, as Grafana does when the dashboard loads: the first datasource of the
// variable's plugin type, by name, whose name matches the variable's regex.
// Dashboards imported from grafana.com are commonly saved this way.
func defaultDatasourceVariableValue(ctx context.Context, db map[string]interface{}, name string) (string, bool) {
	for _, item := range safeArray(safeObject(db, "templating"), "list") {
		variable, ok := item.(map[string]interface{})
		if !ok || safeString(variable, "name") != name || safeString(variable, "type") != "datasource" {
			continue
		}
		pluginID := safeString(variable, "query")
		if pluginID == "" {
			return "", false
		}
		var filter *regexp.Regexp
		if pattern := safeString(variable, "regex"); pattern != "" {
			var err error
			if filter, err = jsRegexp(pattern); err != nil {
				return "", false
			}
		}
		list, err := datasource.List(ctx)
		if err != nil {
			return "", false
		}
		var candidates []*models.DataSourceListItemDTO
		for _, ds := range list {
			if ds != nil && matchPluginID(pluginID, ds.Type) && (filter == nil || filter.MatchString(ds.Name)) {
				candidates = append(candidates, ds)
			}
		}
		if len(candidates) == 0 {
			return "", false
		}
		slices.SortStableFunc(candidates, func(a, b *models.DataSourceListItemDTO) int {
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
		return candidates[0].UID, true
	}
	return "", false
}

// jsRegexFlags matches the "/pattern/flags" form the way Grafana's
// stringToJsRegex does.
var jsRegexFlags = regexp.MustCompile(`^/(.*?)/(g?i?m?y?s?)$`)

// jsRegexp compiles a Grafana variable regex like stringToJsRegex: a bare
// pattern must match the whole name, and the "/pattern/flags" form is
// unanchored, with the i, m and s flags applied (g and y don't affect a match).
func jsRegexp(pattern string) (*regexp.Regexp, error) {
	if !strings.HasPrefix(pattern, "/") {
		return regexp.Compile("^" + pattern + "$")
	}
	match := jsRegexFlags.FindStringSubmatch(pattern)
	if match == nil {
		return nil, fmt.Errorf("'%s' is not a valid regular expression", pattern)
	}
	var flags string
	for _, f := range "ims" {
		if strings.ContainsRune(match[2], f) {
			flags += string(f)
		}
	}
	if flags != "" {
		return regexp.Compile("(?" + flags + ")" + match[1])
	}
	return regexp.Compile(match[1])
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
// Prometheus flavors match via isPromFlavor; Loki and CloudWatch use exact (case-insensitive) matching;
// ClickHouse uses substring matching because the API type is "grafana-clickhouse-datasource".
func normalizeDatasourceType(dsType string) string {
	lower := strings.ToLower(dsType)
	switch {
	case isPromFlavor(lower) || lower == "stackdriver":
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
	case strings.Contains(lower, "infinity"):
		return "infinity"
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
	case backend.Responses:
		// /api/ds/query results (CloudWatch, Infinity): empty unless some frame has rows.
		for _, r := range v {
			for _, frame := range r.Frames {
				if frame != nil && frame.Rows() > 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}

// generatePanelQueryHints generates helpful hints when panel query returns no data
func generatePanelQueryHints(datasourceType, query string) []string {
	typ := normalizeDatasourceType(datasourceType)
	if typ == "prometheus" || typ == "loki" {
		shared := GenerateEmptyResultHints(HintContext{DatasourceType: typ, Query: query})
		hints := []string{shared.Summary, "Possible causes:"}
		for _, cause := range shared.PossibleCauses {
			hints = append(hints, "- "+cause)
		}
		hints = append(hints, "Try:")
		for _, action := range shared.SuggestedActions {
			hints = append(hints, "- "+action)
		}
		if query != "" {
			hints = append(hints, "- Query executed: "+truncateString(query, 100))
		}
		return hints
	}
	hints := []string{"No data found for the panel query. Possible reasons:"}

	hints = append(hints, "- Time range may have no data - try extending with start='now-6h' or start='now-24h'")

	switch normalizeDatasourceType(datasourceType) {
	case "clickhouse":
		hints = append(hints,
			"- Table may be empty for this time range - use query_sql with a COUNT(*) to verify",
			"- Column names or WHERE clause may not match - use describe_sql_table to check schema",
			"- Time filter may not match the actual timestamp column format",
		)
	case "infinity":
		hints = append(hints,
			"- The URL or inline content may have returned no rows for these variables - the query field shows the substituted URL",
			"- root_selector may not match the response shape - verify it against a sample response in the panel editor",
		)
	case elasticsearchDatasourceType:
		hints = append(hints,
			"- The Lucene query or filters may not match any documents for these variables - the query field shows the substituted Lucene query",
			"- The index pattern or time field in the datasource settings may not cover these documents - query_elasticsearch with the same index shows what is there",
		)
	case Neo4jDatasourceType:
		hints = append(hints,
			"- The MATCH pattern, labels or property filters may not match any nodes for these variables - the query field shows the substituted Cypher",
			"- The datasource may point at a different Neo4j database than the panel expects - check the database setting in the datasource",
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
	"Executes one or more dashboard panel queries with optional time range and variable overrides. Accepts an array of panel IDs to query in a single call. Fetches the dashboard, extracts queries from the specified panels, substitutes template variables and Grafana macros ($__range, $__rate_interval, $__interval, $__auto), resolves the \"default\" datasource reference, and routes to the appropriate datasource (Prometheus, Loki, ClickHouse, CloudWatch, InfluxDB, BigQuery, MSSQL, PostgreSQL, Elasticsearch, Neo4j, or Infinity with a backend parser). Returns results keyed by panel ID - partial failures are allowed (some panels can succeed while others fail). If a panel uses a template variable datasource you cannot access, provide datasourceUid and datasourceType to override.",
	runPanelQuery,
	mcpgrafana.WithTitleAnnotation("Run panel query"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(dashboardRead...),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

// AddRunPanelQueryTools registers run panel query tools with the MCP server.
// Every tool in this category executes a query, so nothing is registered when
// enableQueryTools is false.
func AddRunPanelQueryTools(s *mcp.Server, enableQueryTools bool) {
	if enableQueryTools {
		RunPanelQuery.Register(s)
	}
}
