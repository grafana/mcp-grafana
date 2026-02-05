package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client/datasources"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/common/model"
)

// RunPanelQueryParams defines parameters for running a panel's query
type RunPanelQueryParams struct {
	DashboardUID   string            `json:"dashboardUid" jsonschema:"required,description=Dashboard UID"`
	PanelID        int               `json:"panelId" jsonschema:"required,description=Panel ID to execute"`
	QueryIndex     *int              `json:"queryIndex,omitempty" jsonschema:"description=Index of the query to execute (0-based). Defaults to 0 (first query). Use get_dashboard_panel_queries to see all queries in a panel."`
	Start          string            `json:"start" jsonschema:"description=Override start time (e.g. 'now-1h'\\, RFC3339\\, Unix ms)"`
	End            string            `json:"end" jsonschema:"description=Override end time (e.g. 'now'\\, RFC3339\\, Unix ms)"`
	Variables      map[string]string `json:"variables" jsonschema:"description=Override dashboard variables (e.g. {\"job\": \"api-server\"})"`
	DatasourceUID  string            `json:"datasourceUid,omitempty" jsonschema:"description=Override datasource UID. Use when panel uses a template variable datasource you cannot access."`
	DatasourceType string            `json:"datasourceType,omitempty" jsonschema:"description=Override datasource type (prometheus\\, loki\\, grafana-clickhouse-datasource\\, cloudwatch). Recommended when datasourceUid is provided to skip permission lookup."`
}

// QueryTimeRange represents the actual time range used for a panel query
type QueryTimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// RunPanelQueryResult contains the result of running a panel's query
type RunPanelQueryResult struct {
	DashboardUID   string         `json:"dashboardUid"`
	PanelID        int            `json:"panelId"`
	PanelTitle     string         `json:"panelTitle"`
	DatasourceType string         `json:"datasourceType"`
	DatasourceUID  string         `json:"datasourceUid"`
	Query          string         `json:"query"`     // The query that was executed
	TimeRange      QueryTimeRange `json:"timeRange"` // Actual time range used
	Results        interface{}    `json:"results"`   // Results in datasource-native format
	Hints          []string       `json:"hints,omitempty"` // Hints when results are empty
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

// runPanelQuery executes a dashboard panel's query with optional time range and variable overrides
func runPanelQuery(ctx context.Context, args RunPanelQueryParams) (*RunPanelQueryResult, error) {
	// Step 1: Fetch the dashboard
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: args.DashboardUID})
	if err != nil {
		return nil, fmt.Errorf("fetching dashboard: %w", err)
	}

	db, ok := dashboard.Dashboard.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}

	// Step 2: Find the panel by ID
	panel, err := findPanelByID(db, args.PanelID)
	if err != nil {
		return nil, fmt.Errorf("finding panel: %w", err)
	}

	// Step 3: Extract query and datasource info from the panel
	queryIndex := 0
	if args.QueryIndex != nil {
		queryIndex = *args.QueryIndex
	}
	panelData, err := extractPanelInfo(panel, queryIndex)
	if err != nil {
		return nil, fmt.Errorf("extracting panel info: %w", err)
	}

	// Step 4: Extract template variables from dashboard
	variables := extractTemplateVariables(db)

	// Step 5: Apply variable overrides from user
	for name, value := range args.Variables {
		variables[name] = value
	}

	// Step 6: Resolve datasource UID and type
	datasourceUID := panelData.DatasourceUID
	datasourceType := panelData.DatasourceType

	// Apply explicit datasource overrides (highest priority)
	if args.DatasourceUID != "" {
		datasourceUID = args.DatasourceUID
		if args.DatasourceType != "" {
			datasourceType = args.DatasourceType
		}
		// Note: if datasourceType not provided, we'll try to look it up below
	} else if isVariableReference(datasourceUID) {
		// Resolve variable reference only if no explicit override
		varName := extractVariableName(datasourceUID)
		if resolvedUID, ok := variables[varName]; ok {
			datasourceUID = resolvedUID
		} else {
			// Provide helpful hint with available datasources of the expected type
			availableDS := getAvailableDatasourceUIDs(ctx, panelData.DatasourceType)
			return nil, fmt.Errorf("datasource variable '%s' not found. Hint: Use 'datasourceUid' and 'datasourceType' to override. Available %s datasources: %v", datasourceUID, panelData.DatasourceType, availableDS)
		}
	}
	// Note: datasourceType without datasourceUID is ignored (type without UID is meaningless)

	// If we still need the datasource type, look it up with type-safe error handling
	if datasourceType == "" && datasourceUID != "" {
		ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: datasourceUID})
		if err != nil {
			// Type-safe error checking using OpenAPI client types
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

	// Step 7: Substitute variables in the query
	query := substituteVariables(panelData.Query, variables)

	// Step 8: Determine time range
	start := args.Start
	end := args.End
	if start == "" {
		start = "now-1h"
	}
	if end == "" {
		end = "now"
	}

	// Step 9: Route to appropriate datasource and execute query
	var results interface{}

	switch {
	case strings.Contains(strings.ToLower(datasourceType), "prometheus"):
		results, err = executePrometheusQuery(ctx, datasourceUID, query, start, end)
	case strings.Contains(strings.ToLower(datasourceType), "loki"):
		results, err = executeLokiQuery(ctx, datasourceUID, query, start, end)
	case strings.Contains(strings.ToLower(datasourceType), "clickhouse"):
		results, err = executeClickHouseQuery(ctx, datasourceUID, query, start, end, variables)
	case strings.Contains(strings.ToLower(datasourceType), "cloudwatch"):
		results, err = executeCloudWatchPanelQuery(ctx, datasourceUID, panelData, start, end, variables)
	default:
		return nil, fmt.Errorf("datasource type '%s' is not supported by run_panel_query; use the native query tool (e.g. query_prometheus\\, query_loki_logs\\, query_clickhouse\\, query_cloudwatch) directly", datasourceType)
	}

	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}

	// Check for empty results and generate hints
	var hints []string
	if isEmptyPanelResult(results) {
		hints = generatePanelQueryHints(datasourceType, query, start, end)
	}

	return &RunPanelQueryResult{
		DashboardUID:   args.DashboardUID,
		PanelID:        args.PanelID,
		PanelTitle:     panelData.Title,
		DatasourceType: datasourceType,
		DatasourceUID:  datasourceUID,
		Query:          query,
		TimeRange: QueryTimeRange{
			Start: start,
			End:   end,
		},
		Results: results,
		Hints:   hints,
	}, nil
}


// extractPanelInfo extracts query and datasource information from a panel
func extractPanelInfo(panel map[string]interface{}, queryIndex int) (*panelInfo, error) {
	info := &panelInfo{
		ID:    safeInt(panel, "id"),
		Title: safeString(panel, "title"),
	}

	// Extract datasource from panel-level if present
	if dsField := safeObject(panel, "datasource"); dsField != nil {
		info.DatasourceUID = safeString(dsField, "uid")
		info.DatasourceType = safeString(dsField, "type")
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

	// If datasource not set at panel level, try target level
	if info.DatasourceUID == "" {
		if targetDS := safeObject(target, "datasource"); targetDS != nil {
			info.DatasourceUID = safeString(targetDS, "uid")
			info.DatasourceType = safeString(targetDS, "type")
		}
	}

	if info.DatasourceUID == "" {
		return nil, fmt.Errorf("could not determine datasource for panel")
	}

	// Try to get query expression - different datasources use different field names
	// Prometheus/Loki use "expr", some use "query", ClickHouse uses "rawSql", etc.
	query := extractQueryExpression(target)

	// CloudWatch panels use structured targets (namespace, metrics, dimensions) rather than string expressions
	// Allow empty query for CloudWatch - we'll use the RawTarget instead
	if query == "" && !strings.Contains(strings.ToLower(info.DatasourceType), "cloudwatch") {
		return nil, fmt.Errorf("could not extract query from panel target (checked: expr, query, expression, rawSql, rawQuery)")
	}
	info.Query = query

	return info, nil
}

// extractTemplateVariables extracts template variables and their current values from dashboard
func extractTemplateVariables(db map[string]interface{}) map[string]string {
	variables := make(map[string]string)

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
		if current != nil {
			// Try "value" field first (can be string or array)
			if val, ok := current["value"]; ok {
				switch v := val.(type) {
				case string:
					variables[name] = v
				case []interface{}:
					// Multi-value - take first value for simplicity
					if len(v) > 0 {
						if str, ok := v[0].(string); ok {
							variables[name] = str
						}
					}
				}
			}
			// Fall back to "text" field
			if variables[name] == "" {
				if text, ok := current["text"].(string); ok && text != "" && text != "All" {
					variables[name] = text
				}
			}
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

	// Substitute Prometheus macros ($__range, $__rate_interval, $__interval)
	query = substitutePrometheusMacros(query, startTime, endTime)

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

	return queryLokiLogs(ctx, QueryLokiLogsParams{
		DatasourceUID: datasourceUID,
		LogQL:         query,
		StartRFC3339:  startTime.Format("2006-01-02T15:04:05Z07:00"),
		EndRFC3339:    endTime.Format("2006-01-02T15:04:05Z07:00"),
		Limit:         100,
		Direction:     "backward",
		QueryType:     "range",
	})
}

// executeClickHouseQuery runs a ClickHouse query using the existing queryClickHouse function
// NOTE: Do NOT substitute macros here - queryClickHouse() handles them internally
// via substituteClickHouseMacros() which properly handles $__timeFilter(column),
// $__from, $__to, $__interval, $__interval_ms
func executeClickHouseQuery(ctx context.Context, datasourceUID, query, start, end string, variables map[string]string) (*ClickHouseQueryResult, error) {
	return queryClickHouse(ctx, ClickHouseQueryParams{
		DatasourceUID: datasourceUID,
		Query:         query,
		Start:         start,
		End:           end,
		Variables:     variables,
	})
}

// executeCloudWatchPanelQuery runs a CloudWatch query using Grafana's /api/ds/query endpoint
// CloudWatch panels have structured targets (namespace, metrics, dimensions) rather than string expressions
func executeCloudWatchPanelQuery(ctx context.Context, datasourceUID string, panelData *panelInfo, start, end string, variables map[string]string) (interface{}, error) {
	if panelData.RawTarget == nil {
		return nil, fmt.Errorf("CloudWatch panel target not available")
	}

	// Check for math expression panels (type: __expr__ or expression)
	// These require executing multiple queries which we don't support yet
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
	target := substituteVariablesInMap(panelData.RawTarget, variables)

	// Ensure datasource is set correctly
	target["datasource"] = map[string]interface{}{"uid": datasourceUID, "type": "cloudwatch"}

	// Ensure refId is set
	if safeString(target, "refId") == "" {
		target["refId"] = "A"
	}

	// Build /api/ds/query payload
	payload := map[string]interface{}{
		"queries": []map[string]interface{}{target},
		"from":    fmt.Sprintf("%d", startTime.UnixMilli()),
		"to":      fmt.Sprintf("%d", endTime.UnixMilli()),
	}

	return executeGrafanaDSQuery(ctx, payload)
}

// substituteVariablesInMap recursively substitutes variables in a map's string values
func substituteVariablesInMap(target map[string]interface{}, variables map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range target {
		switch val := v.(type) {
		case string:
			result[k] = substituteVariables(val, variables)
		case map[string]interface{}:
			result[k] = substituteVariablesInMap(val, variables)
		case []interface{}:
			result[k] = substituteVariablesInSlice(val, variables)
		default:
			result[k] = v
		}
	}
	return result
}

// substituteVariablesInSlice recursively substitutes variables in a slice
func substituteVariablesInSlice(slice []interface{}, variables map[string]string) []interface{} {
	result := make([]interface{}, len(slice))
	for i, v := range slice {
		switch val := v.(type) {
		case string:
			result[i] = substituteVariables(val, variables)
		case map[string]interface{}:
			result[i] = substituteVariablesInMap(val, variables)
		case []interface{}:
			result[i] = substituteVariablesInSlice(val, variables)
		default:
			result[i] = v
		}
	}
	return result
}

// executeGrafanaDSQuery executes a query through Grafana's /api/ds/query endpoint
func executeGrafanaDSQuery(ctx context.Context, payload map[string]interface{}) (interface{}, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/")

	// Create custom transport with TLS configuration if available
	var transport = http.DefaultTransport
	if tlsConfig := cfg.TLSConfig; tlsConfig != nil {
		var err error
		transport, err = tlsConfig.HTTPTransport(transport.(*http.Transport))
		if err != nil {
			return nil, fmt.Errorf("failed to create custom transport: %w", err)
		}
	}

	// Add authentication
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/ds/query", bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to extract error message from response
		if errMsg, ok := result["message"].(string); ok {
			return nil, fmt.Errorf("query failed: %s", errMsg)
		}
		return nil, fmt.Errorf("query failed with status %d: %v", resp.StatusCode, result)
	}

	// Return the results from the response
	if results, ok := result["results"].(map[string]interface{}); ok {
		return results, nil
	}

	return result, nil
}

// substitutePrometheusMacros substitutes Grafana temporal macros for Prometheus queries
// Handles: $__range, $__rate_interval, $__interval, $__interval_ms, ${__interval}
func substitutePrometheusMacros(query string, start, end time.Time) string {
	duration := end.Sub(start)

	// $__range - total time range as duration string (e.g., "14m", "1h30m")
	rangeStr := formatPrometheusDuration(duration)
	query = strings.ReplaceAll(query, "$__range", rangeStr)

	// $__rate_interval - typically scrape_interval * 4, default to "1m"
	// This is a reasonable default for most Prometheus setups
	query = strings.ReplaceAll(query, "$__rate_interval", "1m")

	// Calculate interval based on time range / max data points (~100 points)
	interval := duration / 100
	if interval < time.Second {
		interval = time.Second
	}

	// IMPORTANT: Substitute $__interval_ms BEFORE $__interval to avoid partial replacement
	// ($__interval is a substring of $__interval_ms)
	intervalMs := int64(interval / time.Millisecond)
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
// Supports: $varname, ${varname}, [[varname]]
func isVariableReference(s string) bool {
	return strings.HasPrefix(s, "$") || strings.HasPrefix(s, "[[")
}

// extractVariableName extracts the variable name from different reference formats
// $varname -> varname, ${varname} -> varname, [[varname]] -> varname
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
// Used to provide helpful hints when datasource resolution fails
func getAvailableDatasourceUIDs(ctx context.Context, dsType string) []string {
	datasources, err := listDatasources(ctx, ListDatasourcesParams{Type: dsType})
	if err != nil {
		return nil
	}
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
	case *ClickHouseQueryResult:
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
func generatePanelQueryHints(datasourceType, query, start, end string) []string {
	hints := []string{"No data found for the panel query. Possible reasons:"}

	// Add time range hint
	hints = append(hints, "- Time range may have no data - try extending with start='now-6h' or start='now-24h'")

	// Add datasource-specific hints
	switch {
	case strings.Contains(strings.ToLower(datasourceType), "prometheus"):
		hints = append(hints,
			"- Metric may not exist - use list_prometheus_metric_names to discover available metrics",
			"- Label selectors may be too restrictive - try removing some filters",
			"- Prometheus may not have scraped data for this time range",
		)
	case strings.Contains(strings.ToLower(datasourceType), "loki"):
		hints = append(hints,
			"- Log stream selectors may not match any streams - use list_loki_label_names to discover labels",
			"- Pipeline filters may be filtering out all logs - try simplifying the query",
			"- Use query_loki_stats to check if logs exist in this time range",
		)
	case strings.Contains(strings.ToLower(datasourceType), "clickhouse"):
		hints = append(hints,
			"- Table may be empty for this time range - use query_clickhouse with a COUNT(*) to verify",
			"- Column names or WHERE clause may not match - use describe_clickhouse_table to check schema",
			"- Time filter may not match the actual timestamp column format",
		)
	case strings.Contains(strings.ToLower(datasourceType), "cloudwatch"):
		hints = append(hints,
			"- Namespace or metric name may be incorrect - use list_cloudwatch_namespaces and list_cloudwatch_metrics to discover available options",
			"- Dimension filters may not match any resources - use list_cloudwatch_dimensions to check available dimensions",
			"- AWS region may be incorrect - verify the region setting in the datasource",
			"- CloudWatch metrics may have longer retention periods than the selected time range",
		)
	}

	// Add query-specific hint
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

// RunPanelQuery is the tool definition for running a panel's query
var RunPanelQuery = mcpgrafana.MustTool(
	"run_panel_query",
	"Executes a dashboard panel's query with optional time range and variable overrides. Fetches the dashboard\\, extracts the query from the specified panel\\, substitutes template variables and Grafana macros ($__range\\, $__rate_interval\\, $__interval)\\, and routes to the appropriate datasource (Prometheus\\, Loki\\, ClickHouse\\, or CloudWatch). Returns query results in the datasource's native format. Use get_dashboard_summary first to find panel IDs. If panel uses a template variable datasource you cannot access\\, provide datasourceUid and datasourceType to override. Note: CloudWatch math expression panels are not supported - use query_cloudwatch directly for the underlying metrics.",
	runPanelQuery,
	mcp.WithTitleAnnotation("Run panel query"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// RunPanelQueriesParams defines parameters for running multiple panel queries
type RunPanelQueriesParams struct {
	DashboardUID   string            `json:"dashboardUid" jsonschema:"required,description=Dashboard UID"`
	PanelIDs       []int             `json:"panelIds" jsonschema:"required,description=Array of panel IDs to execute"`
	Start          string            `json:"start" jsonschema:"description=Override start time (e.g. 'now-1h'\\, RFC3339\\, Unix ms)"`
	End            string            `json:"end" jsonschema:"description=Override end time (e.g. 'now'\\, RFC3339\\, Unix ms)"`
	Variables      map[string]string `json:"variables" jsonschema:"description=Override dashboard variables (e.g. {\"job\": \"api-server\"})"`
	DatasourceUID  string            `json:"datasourceUid,omitempty" jsonschema:"description=Override datasource UID for all panels"`
	DatasourceType string            `json:"datasourceType,omitempty" jsonschema:"description=Override datasource type for all panels"`
}

// RunPanelQueriesResult contains the results of running multiple panel queries
type RunPanelQueriesResult struct {
	DashboardUID string                          `json:"dashboardUid"`
	Results      map[int]*RunPanelQueryResult    `json:"results"`        // Successful results by panel ID
	Errors       map[int]string                  `json:"errors,omitempty"` // Errors by panel ID
	TimeRange    QueryTimeRange                  `json:"timeRange"`
}

// runPanelQueries executes multiple dashboard panel queries in a single call
func runPanelQueries(ctx context.Context, args RunPanelQueriesParams) (*RunPanelQueriesResult, error) {
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

	results := make(map[int]*RunPanelQueryResult)
	errors := make(map[int]string)

	// Execute each panel query sequentially
	for _, panelID := range args.PanelIDs {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID:   args.DashboardUID,
			PanelID:        panelID,
			Start:          start,
			End:            end,
			Variables:      args.Variables,
			DatasourceUID:  args.DatasourceUID,
			DatasourceType: args.DatasourceType,
		})

		if err != nil {
			// Partial failure: capture error but continue with other panels
			errors[panelID] = err.Error()
		} else {
			results[panelID] = result
		}
	}

	return &RunPanelQueriesResult{
		DashboardUID: args.DashboardUID,
		Results:      results,
		Errors:       errors,
		TimeRange: QueryTimeRange{
			Start: start,
			End:   end,
		},
	}, nil
}

// RunPanelQueries is the tool definition for running multiple panel queries
var RunPanelQueries = mcpgrafana.MustTool(
	"run_panel_queries",
	"Executes multiple dashboard panel queries in a single call. Reduces API round-trips when analyzing multiple panels. Fetches dashboard once\\, then executes queries for each specified panel ID. Returns results and errors keyed by panel ID - partial failures are allowed (some panels can succeed while others fail). Use get_dashboard_summary to find panel IDs.",
	runPanelQueries,
	mcp.WithTitleAnnotation("Run multiple panel queries"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddRunPanelQueryTools registers run panel query tools with the MCP server
func AddRunPanelQueryTools(mcp *server.MCPServer) {
	RunPanelQuery.Register(mcp)
	RunPanelQueries.Register(mcp)
}
