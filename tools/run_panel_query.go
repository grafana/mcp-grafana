package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client/datasources"
	mcpgrafana "github.com/grafana/mcp-grafana"
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
	DatasourceType string            `json:"datasourceType,omitempty" jsonschema:"description=Override datasource type (prometheus\\, loki\\, grafana-clickhouse-datasource\\, cloudwatch)"`
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
	DashboardUID string                   `json:"dashboardUid"`
	Results      map[int]*PanelQueryResult `json:"results"`
	Errors       map[int]string           `json:"errors,omitempty"`
	TimeRange    QueryTimeRange           `json:"timeRange"`
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
		result, err := runSinglePanelQuery(ctx, dashboard, db, panelID, queryIndex, start, end, args.Variables, args.DatasourceUID, args.DatasourceType)
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
func runSinglePanelQuery(ctx context.Context, _ interface{}, db map[string]interface{}, panelID int, queryIndex int, start, end string, variables map[string]string, dsUID, dsType string) (*PanelQueryResult, error) {
	// Find the panel by ID
	panel, err := findPanelByID(db, panelID)
	if err != nil {
		return nil, fmt.Errorf("finding panel: %w", err)
	}

	// Extract query and datasource info from the panel
	panelData, err := extractPanelInfo(panel, queryIndex)
	if err != nil {
		return nil, fmt.Errorf("extracting panel info: %w", err)
	}

	// Extract template variables from dashboard
	vars := extractTemplateVariables(db)

	// Apply variable overrides from user
	for name, value := range variables {
		vars[name] = value
	}

	// Resolve datasource UID and type
	datasourceUID := panelData.DatasourceUID
	datasourceType := panelData.DatasourceType

	// Apply explicit datasource overrides (highest priority)
	if dsUID != "" {
		datasourceUID = dsUID
		if dsType != "" {
			datasourceType = dsType
		}
	} else if isVariableReference(datasourceUID) {
		// Resolve variable reference only if no explicit override
		varName := extractVariableName(datasourceUID)
		if resolvedUID, ok := vars[varName]; ok {
			datasourceUID = resolvedUID
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
	query := substituteTemplateVariables(panelData.Query, vars)

	// Route to appropriate datasource and execute query
	var results interface{}

	switch {
	case strings.Contains(strings.ToLower(datasourceType), "prometheus"):
		results, err = executePrometheusQuery(ctx, datasourceUID, query, start, end)
	case strings.Contains(strings.ToLower(datasourceType), "loki"):
		results, err = executeLokiQuery(ctx, datasourceUID, query, start, end)
	case strings.Contains(strings.ToLower(datasourceType), "clickhouse"):
		results, err = executeClickHouseQuery(ctx, datasourceUID, query, start, end, vars)
	case strings.Contains(strings.ToLower(datasourceType), "cloudwatch"):
		results, err = executeCloudWatchPanelQuery(ctx, datasourceUID, panelData, start, end, vars)
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

	return &PanelQueryResult{
		PanelID:        panelID,
		PanelTitle:     panelData.Title,
		DatasourceType: datasourceType,
		DatasourceUID:  datasourceUID,
		Query:          query,
		Results:        results,
		Hints:          hints,
	}, nil
}

// findPanelByID searches for a panel by ID in the dashboard, including nested panels in rows
func findPanelByID(db map[string]interface{}, panelID int) (map[string]interface{}, error) {
	panels := safeArray(db, "panels")
	if panels == nil {
		return nil, fmt.Errorf("dashboard has no panels")
	}

	for _, p := range panels {
		panel, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		id := safeInt(panel, "id")
		if id == panelID {
			return panel, nil
		}

		// Check for nested panels in row type
		if safeString(panel, "type") == "row" {
			nestedPanels := safeArray(panel, "panels")
			for _, np := range nestedPanels {
				nestedPanel, ok := np.(map[string]interface{})
				if !ok {
					continue
				}
				if safeInt(nestedPanel, "id") == panelID {
					return nestedPanel, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("panel with ID %d not found", panelID)
}

// extractQueryExpression extracts query string from a target
func extractQueryExpression(target map[string]interface{}) string {
	queryFields := []string{
		"expr",       // Prometheus
		"query",      // Loki, ClickHouse, generic
		"expression", // CloudWatch
		"rawSql",     // SQL databases
		"rawQuery",   // Some datasources
	}

	for _, field := range queryFields {
		if val := safeString(target, field); val != "" {
			return val
		}
	}

	return ""
}

// substituteTemplateVariables replaces template variables in a query string
// Supports ${varname}, [[varname]], and $varname (with word boundary) patterns
func substituteTemplateVariables(query string, variables map[string]string) string {
	if variables == nil {
		return query
	}
	for name, value := range variables {
		// Replace ${varname}
		query = strings.ReplaceAll(query, "${"+name+"}", value)
		// Replace [[varname]]
		query = strings.ReplaceAll(query, "[["+name+"]]", value)
		// Replace $varname with word boundary to avoid partial matches
		varRe := regexp.MustCompile(fmt.Sprintf(`\$%s\b`, regexp.QuoteMeta(name)))
		query = varRe.ReplaceAllString(query, value)
	}
	return query
}

// substituteTemplateVariablesInMap recursively substitutes variables in a map's string values
func substituteTemplateVariablesInMap(target map[string]interface{}, variables map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range target {
		switch val := v.(type) {
		case string:
			result[k] = substituteTemplateVariables(val, variables)
		case map[string]interface{}:
			result[k] = substituteTemplateVariablesInMap(val, variables)
		case []interface{}:
			result[k] = substituteTemplateVariablesInSlice(val, variables)
		default:
			result[k] = v
		}
	}
	return result
}

// substituteTemplateVariablesInSlice recursively substitutes variables in a slice
func substituteTemplateVariablesInSlice(slice []interface{}, variables map[string]string) []interface{} {
	result := make([]interface{}, len(slice))
	for i, v := range slice {
		switch val := v.(type) {
		case string:
			result[i] = substituteTemplateVariables(val, variables)
		case map[string]interface{}:
			result[i] = substituteTemplateVariablesInMap(val, variables)
		case []interface{}:
			result[i] = substituteTemplateVariablesInSlice(val, variables)
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
	query := extractQueryExpression(target)

	// CloudWatch panels use structured targets rather than string expressions
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

// executeClickHouseQuery runs a ClickHouse query using the existing queryClickHouse function
// NOTE: Do NOT substitute macros here - queryClickHouse() handles them internally
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
func executeCloudWatchPanelQuery(ctx context.Context, datasourceUID string, panelData *panelInfo, start, end string, variables map[string]string) (interface{}, error) {
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
	target := substituteTemplateVariablesInMap(panelData.RawTarget, variables)

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

// executeGrafanaDSQuery executes a query through Grafana's /api/ds/query endpoint
func executeGrafanaDSQuery(ctx context.Context, payload map[string]interface{}) (interface{}, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/")

	// Create custom transport with TLS and extra headers support
	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
		Timeout:   30 * time.Second,
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
func substitutePrometheusMacros(query string, start, end time.Time) string {
	duration := end.Sub(start)

	// $__range - total time range as duration string
	rangeStr := formatPrometheusDuration(duration)
	query = strings.ReplaceAll(query, "$__range", rangeStr)

	// $__rate_interval - default to "1m"
	query = strings.ReplaceAll(query, "$__rate_interval", "1m")

	// Calculate interval based on time range / max data points (~100 points)
	interval := duration / 100
	if interval < time.Second {
		interval = time.Second
	}

	// Substitute $__interval_ms BEFORE $__interval to avoid partial replacement
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

	hints = append(hints, "- Time range may have no data - try extending with start='now-6h' or start='now-24h'")

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
	"Executes one or more dashboard panel queries with optional time range and variable overrides. Accepts an array of panel IDs to query in a single call. Fetches the dashboard\\, extracts queries from the specified panels\\, substitutes template variables and Grafana macros ($__range\\, $__rate_interval\\, $__interval)\\, and routes to the appropriate datasource (Prometheus\\, Loki\\, ClickHouse\\, or CloudWatch). Returns results keyed by panel ID - partial failures are allowed (some panels can succeed while others fail). Use get_dashboard_summary first to find panel IDs. If a panel uses a template variable datasource you cannot access\\, provide datasourceUid and datasourceType to override.",
	runPanelQuery,
	mcp.WithTitleAnnotation("Run panel query"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddRunPanelQueryTools registers run panel query tools with the MCP server
func AddRunPanelQueryTools(mcp *server.MCPServer) {
	RunPanelQuery.Register(mcp)
}
