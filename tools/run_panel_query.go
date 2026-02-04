package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/common/model"
)

// RunPanelQueryParams defines parameters for running a panel's query
type RunPanelQueryParams struct {
	DashboardUID string            `json:"dashboardUid" jsonschema:"required,description=Dashboard UID"`
	PanelID      int               `json:"panelId" jsonschema:"required,description=Panel ID to execute"`
	Start        string            `json:"start" jsonschema:"description=Override start time (e.g. 'now-1h'\\, RFC3339\\, Unix ms)"`
	End          string            `json:"end" jsonschema:"description=Override end time (e.g. 'now'\\, RFC3339\\, Unix ms)"`
	Variables    map[string]string `json:"variables" jsonschema:"description=Override dashboard variables (e.g. {\"job\": \"api-server\"})"`
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
}

// panelInfo contains extracted information about a panel
type panelInfo struct {
	ID             int
	Title          string
	DatasourceUID  string
	DatasourceType string
	Query          string
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
	panelData, err := extractPanelInfo(panel)
	if err != nil {
		return nil, fmt.Errorf("extracting panel info: %w", err)
	}

	// Step 4: Extract template variables from dashboard
	variables := extractTemplateVariables(db)

	// Step 5: Apply variable overrides from user
	for name, value := range args.Variables {
		variables[name] = value
	}

	// Step 6: Resolve datasource UID if it's a variable reference
	datasourceUID := panelData.DatasourceUID
	datasourceType := panelData.DatasourceType

	if isVariableReference(datasourceUID) {
		varName := extractVariableName(datasourceUID)
		if resolvedUID, ok := variables[varName]; ok {
			datasourceUID = resolvedUID
		} else {
			return nil, fmt.Errorf("datasource variable '%s' not found in dashboard variables. Available variables: %v", datasourceUID, getVariableNames(variables))
		}
	}

	// If we don't have the datasource type, look it up
	if datasourceType == "" && datasourceUID != "" {
		ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: datasourceUID})
		if err != nil {
			return nil, fmt.Errorf("fetching datasource info: %w", err)
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
	default:
		return nil, fmt.Errorf("datasource type '%s' is not supported by run_panel_query; use the native query tool (e.g. query_prometheus\\, query_loki_logs\\, query_clickhouse) directly", datasourceType)
	}

	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
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
	}, nil
}

// findPanelByID searches for a panel by ID, including nested panels in rows
func findPanelByID(db map[string]interface{}, panelID int) (map[string]interface{}, error) {
	panels, ok := db["panels"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("dashboard has no panels")
	}

	// Search top-level panels
	for _, p := range panels {
		panel, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		// Check if this panel matches
		id := safeInt(panel, "id")
		if id == panelID {
			return panel, nil
		}

		// Check nested panels (for row panels)
		panelType := safeString(panel, "type")
		if panelType == "row" {
			nestedPanels := safeArray(panel, "panels")
			for _, np := range nestedPanels {
				nestedPanel, ok := np.(map[string]interface{})
				if !ok {
					continue
				}
				nestedID := safeInt(nestedPanel, "id")
				if nestedID == panelID {
					return nestedPanel, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("panel with ID %d not found", panelID)
}

// extractPanelInfo extracts query and datasource information from a panel
func extractPanelInfo(panel map[string]interface{}) (*panelInfo, error) {
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

	// Get the first target
	target, ok := targets[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid target format")
	}

	// Try to get query expression - different datasources use different field names
	// Prometheus/Loki use "expr", some use "query", ClickHouse uses "rawSql", etc.
	query := extractQueryExpression(target)
	if query == "" {
		return nil, fmt.Errorf("could not extract query from panel target (checked: expr, query, expression, rawSql, rawQuery)")
	}
	info.Query = query

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

// substituteVariables replaces $varname and ${varname} patterns with actual values
func substituteVariables(query string, variables map[string]string) string {
	result := query

	for name, value := range variables {
		// Replace ${varname} pattern
		result = strings.ReplaceAll(result, "${"+name+"}", value)
		// Replace $varname pattern (word boundary aware)
		// Use regex to avoid partial matches like $jobname when we want $job
		pattern := regexp.MustCompile(`\$` + regexp.QuoteMeta(name) + `\b`)
		result = pattern.ReplaceAllString(result, value)
	}

	return result
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

// getVariableNames returns a list of variable names from a map
func getVariableNames(vars map[string]string) []string {
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	return names
}

// extractQueryExpression tries multiple field names to extract the query expression
func extractQueryExpression(target map[string]any) string {
	// Check fields in order of priority - different datasources use different field names
	queryFields := []string{"expr", "query", "expression", "rawSql", "rawQuery"}
	for _, field := range queryFields {
		if q := safeString(target, field); q != "" {
			return q
		}
	}
	return ""
}

// RunPanelQuery is the tool definition for running a panel's query
var RunPanelQuery = mcpgrafana.MustTool(
	"run_panel_query",
	"Executes a dashboard panel's query with optional time range and variable overrides. Fetches the dashboard\\, extracts the query from the specified panel\\, substitutes template variables and Grafana macros ($__range\\, $__rate_interval\\, $__interval)\\, and routes to the appropriate datasource (Prometheus\\, Loki\\, or ClickHouse). Returns query results in the datasource's native format. Use get_dashboard_summary first to find panel IDs.",
	runPanelQuery,
	mcp.WithTitleAnnotation("Run panel query"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddRunPanelQueryTools registers run panel query tools with the MCP server
func AddRunPanelQueryTools(mcp *server.MCPServer) {
	RunPanelQuery.Register(mcp)
}
