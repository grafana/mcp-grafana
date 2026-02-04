package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// DefaultSearchLogsLimit is the default number of log entries to return
	DefaultSearchLogsLimit = 100

	// MaxSearchLogsLimit is the maximum number of log entries that can be requested
	MaxSearchLogsLimit = 1000

	// LokiDatasourceType is the type identifier for Loki datasources
	LokiDatasourceType = "loki"
)

// SearchLogsParams defines the parameters for searching logs across datasources
type SearchLogsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of a ClickHouse or Loki datasource"`
	Pattern       string `json:"pattern" jsonschema:"required,description=Text pattern or regex to search for in log messages"`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time (e.g. 'now-1h'\\, '2026-02-02T19:00:00Z'\\, Unix ms). Defaults to 'now-1h'"`
	End           string `json:"end,omitempty" jsonschema:"description=End time (e.g. 'now'\\, RFC3339\\, Unix ms). Defaults to 'now'"`
	Limit         int    `json:"limit,omitempty" jsonschema:"default=100,description=Maximum number of log entries to return (max 1000)"`
}

// SearchLogsResult represents the result of a log search
type SearchLogsResult struct {
	Logs           []LogResult `json:"logs"`
	DatasourceType string      `json:"datasourceType"`
	Query          string      `json:"query"`
	TotalFound     int         `json:"totalFound"`
	Hints          []string    `json:"hints,omitempty"`
}

// LogResult represents a single log entry in a normalized format
type LogResult struct {
	Timestamp string            `json:"timestamp"`
	Message   string            `json:"message"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// enforceSearchLogsLimit ensures a limit value is within acceptable bounds
func enforceSearchLogsLimit(requestedLimit int) int {
	if requestedLimit <= 0 {
		return DefaultSearchLogsLimit
	}
	if requestedLimit > MaxSearchLogsLimit {
		return MaxSearchLogsLimit
	}
	return requestedLimit
}

// isRegexPattern checks if a pattern contains regex metacharacters
func isRegexPattern(pattern string) bool {
	// Common regex metacharacters that indicate a regex pattern
	regexChars := []string{".", "*", "+", "?", "^", "$", "[", "]", "(", ")", "{", "}", "|", "\\"}
	for _, char := range regexChars {
		if strings.Contains(pattern, char) {
			return true
		}
	}
	return false
}

// escapeLogQLPattern escapes special characters for LogQL string matching
func escapeLogQLPattern(pattern string) string {
	// Escape backslashes and double quotes for LogQL
	pattern = strings.ReplaceAll(pattern, `\`, `\\`)
	pattern = strings.ReplaceAll(pattern, `"`, `\"`)
	return pattern
}

// escapeClickHousePattern escapes special characters for ClickHouse ILIKE
func escapeClickHousePattern(pattern string) string {
	// Escape single quotes for SQL
	pattern = strings.ReplaceAll(pattern, `'`, `''`)
	// Escape % and _ for ILIKE
	pattern = strings.ReplaceAll(pattern, `%`, `\%`)
	pattern = strings.ReplaceAll(pattern, `_`, `\_`)
	return pattern
}

// generateLokiQuery generates a LogQL query for the given pattern
func generateLokiQuery(pattern string) string {
	if isRegexPattern(pattern) {
		// Use regex filter operator for patterns with regex chars
		return fmt.Sprintf(`{} |~ "%s"`, escapeLogQLPattern(pattern))
	}
	// Use line contains operator for simple text patterns
	return fmt.Sprintf(`{} |= "%s"`, escapeLogQLPattern(pattern))
}

// generateClickHouseLogQuery generates a SQL query for searching logs in ClickHouse
// This function assumes OpenTelemetry log table structure (otel_logs)
// When useRegex is true, uses ClickHouse match() function for regex patterns
func generateClickHouseLogQuery(pattern string, limit int, useRegex bool) string {
	var whereClause string
	if useRegex {
		// Use ClickHouse's match() function for regex - escape single quotes for SQL
		escapedPattern := strings.ReplaceAll(pattern, `'`, `''`)
		whereClause = fmt.Sprintf("match(Body, '%s')", escapedPattern)
	} else {
		// Use ILIKE for case-insensitive substring matching
		escapedPattern := escapeClickHousePattern(pattern)
		whereClause = fmt.Sprintf("Body ILIKE '%%%s%%'", escapedPattern)
	}
	return fmt.Sprintf(
		`SELECT Timestamp, Body, ServiceName, SeverityText, ResourceAttributes, LogAttributes FROM otel_logs WHERE %s AND $__timeFilter(Timestamp) ORDER BY Timestamp DESC LIMIT %d`,
		whereClause, limit,
	)
}

// parseSearchLogsTime parses time strings in various formats for search_logs
func parseSearchLogsTime(timeStr string, isEnd bool) (time.Time, error) {
	if timeStr == "" {
		return time.Time{}, nil
	}

	tr := gtime.TimeRange{
		Now: time.Now(),
	}
	if isEnd {
		tr.To = timeStr
		return tr.ParseTo()
	}
	tr.From = timeStr
	return tr.ParseFrom()
}

// searchLogsInLoki searches logs in a Loki datasource
func searchLogsInLoki(ctx context.Context, args SearchLogsParams, limit int, startRFC3339, endRFC3339 string) (*SearchLogsResult, error) {
	query := generateLokiQuery(args.Pattern)

	// Query Loki using existing infrastructure
	entries, err := queryLokiLogs(ctx, QueryLokiLogsParams{
		DatasourceUID: args.DatasourceUID,
		LogQL:         query,
		StartRFC3339:  startRFC3339,
		EndRFC3339:    endRFC3339,
		Limit:         limit,
		Direction:     "backward", // Most recent first
	})
	if err != nil {
		return nil, fmt.Errorf("querying Loki: %w", err)
	}

	// Convert Loki entries to normalized LogResult format
	logs := make([]LogResult, 0, len(entries.Data))
	for _, entry := range entries.Data {
		logs = append(logs, LogResult{
			Timestamp: entry.Timestamp,
			Message:   entry.Line,
			Labels:    entry.Labels,
		})
	}

	result := &SearchLogsResult{
		Logs:           logs,
		DatasourceType: LokiDatasourceType,
		Query:          query,
		TotalFound:     len(logs),
	}

	// Add hints if no data was found
	if len(logs) == 0 {
		result.Hints = generateSearchLogsHints(LokiDatasourceType, args.Pattern)
	}

	return result, nil
}

// searchLogsInClickHouse searches logs in a ClickHouse datasource
func searchLogsInClickHouse(ctx context.Context, args SearchLogsParams, limit int) (*SearchLogsResult, error) {
	// Detect if pattern contains regex metacharacters
	useRegex := isRegexPattern(args.Pattern)
	query := generateClickHouseLogQuery(args.Pattern, limit, useRegex)

	// Query ClickHouse using existing infrastructure
	chResult, err := queryClickHouse(ctx, ClickHouseQueryParams{
		DatasourceUID: args.DatasourceUID,
		Query:         query,
		Start:         args.Start,
		End:           args.End,
		Limit:         limit,
	})
	if err != nil {
		return nil, fmt.Errorf("querying ClickHouse: %w", err)
	}

	// Convert ClickHouse rows to normalized LogResult format
	logs := make([]LogResult, 0, len(chResult.Rows))
	for _, row := range chResult.Rows {
		logEntry := LogResult{
			Labels: make(map[string]string),
		}

		// Extract timestamp
		if ts, ok := row["Timestamp"].(string); ok {
			logEntry.Timestamp = ts
		} else if ts, ok := row["Timestamp"].(float64); ok {
			// Handle numeric timestamp (Unix ms)
			logEntry.Timestamp = time.UnixMilli(int64(ts)).Format(time.RFC3339Nano)
		}

		// Extract message body
		if body, ok := row["Body"].(string); ok {
			logEntry.Message = body
		}

		// Extract common labels from OpenTelemetry schema
		if svc, ok := row["ServiceName"].(string); ok && svc != "" {
			logEntry.Labels["service"] = svc
		}
		if severity, ok := row["SeverityText"].(string); ok && severity != "" {
			logEntry.Labels["level"] = severity
		}

		logs = append(logs, logEntry)
	}

	result := &SearchLogsResult{
		Logs:           logs,
		DatasourceType: ClickHouseDatasourceType,
		Query:          query,
		TotalFound:     len(logs),
	}

	// Add hints if no data was found
	if len(logs) == 0 {
		result.Hints = generateSearchLogsHints(ClickHouseDatasourceType, args.Pattern)
	}

	return result, nil
}

// generateSearchLogsHints generates helpful hints when no logs are found
func generateSearchLogsHints(datasourceType, pattern string) []string {
	hints := []string{"No logs found matching the pattern. Possible reasons:"}

	switch datasourceType {
	case LokiDatasourceType:
		hints = append(hints,
			"- Pattern may not match any log content - try a simpler pattern",
			"- Time range may have no logs - try extending with start='now-6h' or start='now-24h'",
			"- The query uses an empty stream selector {} - consider using list_loki_label_names to discover labels",
			"- Use query_loki_stats first to check if logs exist in the time range",
		)
		if isRegexPattern(pattern) {
			hints = append(hints, "- Regex pattern may be invalid or too specific - try a simpler pattern first")
		}
	case ClickHouseDatasourceType:
		hints = append(hints,
			"- Pattern may not match any log content - try a simpler pattern",
			"- Time range may have no logs - try extending with start='now-6h' or start='now-24h'",
			"- The query targets the 'otel_logs' table - use list_clickhouse_tables to verify table exists",
			"- Column names may differ - use describe_clickhouse_table to check the actual schema",
			"- Pattern matching is case-insensitive (ILIKE) but may still not match",
		)
	default:
		hints = append(hints,
			"- Verify the datasource is accessible",
			"- Try a simpler pattern",
			"- Try extending the time range",
		)
	}

	return hints
}

// searchLogs searches for log entries matching a pattern across supported datasources
func searchLogs(ctx context.Context, args SearchLogsParams) (*SearchLogsResult, error) {
	// Get datasource info to determine type
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
	if err != nil {
		return nil, fmt.Errorf("getting datasource: %w", err)
	}

	// Enforce limit
	limit := enforceSearchLogsLimit(args.Limit)

	// Parse time range with defaults
	now := time.Now()
	startTime := now.Add(-1 * time.Hour) // Default: 1 hour ago
	endTime := now                        // Default: now

	if args.Start != "" {
		parsed, err := parseSearchLogsTime(args.Start, false)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			startTime = parsed
		}
	}

	if args.End != "" {
		parsed, err := parseSearchLogsTime(args.End, true)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			endTime = parsed
		}
	}

	// Route to appropriate datasource handler
	switch {
	case strings.Contains(ds.Type, "loki"):
		// Convert times to RFC3339 for Loki
		startRFC3339 := startTime.Format(time.RFC3339)
		endRFC3339 := endTime.Format(time.RFC3339)
		return searchLogsInLoki(ctx, args, limit, startRFC3339, endRFC3339)

	case strings.Contains(ds.Type, "clickhouse"):
		return searchLogsInClickHouse(ctx, args, limit)

	default:
		return nil, fmt.Errorf("unsupported datasource type '%s': search_logs supports 'loki' and 'grafana-clickhouse-datasource' types", ds.Type)
	}
}

// SearchLogs is a tool for searching logs across ClickHouse and Loki datasources
var SearchLogs = mcpgrafana.MustTool(
	"search_logs",
	`Search for log entries matching a text pattern across ClickHouse and Loki datasources.

This is a high-level abstraction that automatically generates the appropriate query (LogQL or SQL) based on the datasource type.

For simple text searches, use a plain string pattern. For regex matching, include regex characters in the pattern.

Examples:
- Pattern "error" searches for logs containing "error" (case-insensitive for ClickHouse)
- Pattern "timeout|connection refused" uses regex matching
- Pattern "failed to connect" searches for exact phrase

For more control over queries, use query_loki_logs or query_clickhouse directly.`,
	searchLogs,
	mcp.WithTitleAnnotation("Search logs"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddSearchLogsTools registers all search logs tools with the MCP server
func AddSearchLogsTools(mcp *server.MCPServer) {
	SearchLogs.Register(mcp)
}
