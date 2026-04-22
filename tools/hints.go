package tools

import (
	"fmt"
	"strings"
	"time"
)

// HintContext provides context for generating helpful hints
type HintContext struct {
	DatasourceType string    // "prometheus", "loki", "clickhouse", "cloudwatch"
	Query          string    // The original query
	ProcessedQuery string    // The query after macro/variable substitution (if different)
	StartTime      time.Time // Parsed start time
	EndTime        time.Time // Parsed end time
	Error          error     // Any error that occurred (optional)
}

// EmptyResultHints contains hints for debugging empty results
type EmptyResultHints struct {
	Summary          string     `json:"summary"`
	PossibleCauses   []string   `json:"possibleCauses"`
	SuggestedActions []string   `json:"suggestedActions"`
	Debug            *DebugInfo `json:"debug,omitempty"`
}

// DebugInfo contains debugging information about the query
type DebugInfo struct {
	ProcessedQuery string `json:"processedQuery,omitempty"`
	TimeRange      string `json:"timeRange,omitempty"` // e.g., "2026-02-02T19:00:00Z to 2026-02-02T20:00:00Z"
}

// GenerateEmptyResultHints creates helpful hints when a query returns no data
func GenerateEmptyResultHints(ctx HintContext) *EmptyResultHints {
	hints := &EmptyResultHints{
		PossibleCauses:   []string{},
		SuggestedActions: []string{},
	}

	// Build debug info
	debug := &DebugInfo{}
	if ctx.ProcessedQuery != "" && ctx.ProcessedQuery != ctx.Query {
		debug.ProcessedQuery = ctx.ProcessedQuery
	}
	if !ctx.StartTime.IsZero() && !ctx.EndTime.IsZero() {
		debug.TimeRange = fmt.Sprintf("%s to %s",
			ctx.StartTime.Format(time.RFC3339),
			ctx.EndTime.Format(time.RFC3339))
	}
	if debug.ProcessedQuery != "" || debug.TimeRange != "" {
		hints.Debug = debug
	}

	// Generate datasource-specific hints
	switch strings.ToLower(ctx.DatasourceType) {
	case "prometheus":
		hints.Summary = "The Prometheus query returned no data for the specified time range."
		hints.PossibleCauses = getPrometheusCauses(ctx)
		hints.SuggestedActions = getPrometheusActions(ctx)

	case "loki":
		hints.Summary = "The Loki query returned no log entries for the specified time range."
		hints.PossibleCauses = getLokiCauses(ctx)
		hints.SuggestedActions = getLokiActions(ctx)

	case "clickhouse":
		hints.Summary = "The ClickHouse query returned no rows for the specified parameters."
		hints.PossibleCauses = getClickHouseCauses(ctx)
		hints.SuggestedActions = getClickHouseActions(ctx)

	case "cloudwatch":
		hints.Summary = "The CloudWatch query returned no data for the specified time range."
		hints.PossibleCauses = getCloudWatchCauses(ctx)
		hints.SuggestedActions = getCloudWatchActions(ctx)

	default:
		hints.Summary = "The query returned no data for the specified parameters."
		hints.PossibleCauses = getGenericCauses()
		hints.SuggestedActions = getGenericActions()
	}

	return hints
}

// getPrometheusCauses returns possible causes for empty Prometheus results
func getPrometheusCauses(ctx HintContext) []string {
	causes := []string{
		"The metric may not exist in this Prometheus instance",
		"The label selectors may not match any time series",
		"The time range may be outside when the metric was being scraped",
		"The scrape target may be down or not configured",
	}

	// Add query-specific causes
	if strings.Contains(ctx.Query, "rate(") || strings.Contains(ctx.Query, "irate(") {
		causes = append(causes, "Rate functions require at least two data points within the range vector window")
	}
	if strings.Contains(ctx.Query, "histogram_quantile") {
		causes = append(causes, "Histogram quantile requires histogram buckets (le labels) to be present")
	}

	return causes
}

// getPrometheusActions returns suggested actions for empty Prometheus results
func getPrometheusActions(ctx HintContext) []string {
	actions := []string{
		"Use list_prometheus_metric_names to verify the metric exists",
		"Use list_prometheus_label_values to check available label values",
		"Try expanding the time range to see if data exists in a different period",
		"Verify the scrape configuration and target health in Prometheus",
	}

	// Add query-specific actions
	if strings.Contains(ctx.Query, "{") && strings.Contains(ctx.Query, "}") {
		actions = append(actions, "Try removing or simplifying label matchers to broaden the search")
	}

	return actions
}

// getLokiCauses returns possible causes for empty Loki results
func getLokiCauses(ctx HintContext) []string {
	causes := []string{
		"The stream selector labels may not match any log streams",
		"No logs were ingested during the specified time range",
		"The filter expression may be too restrictive",
		"The label values in the selector may be misspelled or incorrect",
	}

	// Add query-specific causes
	if strings.Contains(ctx.Query, "|=") || strings.Contains(ctx.Query, "!=") ||
		strings.Contains(ctx.Query, "|~") || strings.Contains(ctx.Query, "!~") {
		causes = append(causes, "Line filter expressions may be filtering out all matching logs")
	}
	if strings.Contains(ctx.Query, "| json") || strings.Contains(ctx.Query, "| logfmt") {
		causes = append(causes, "Log parsing may fail if logs are not in the expected format")
	}

	return causes
}

// getLokiActions returns suggested actions for empty Loki results
func getLokiActions(ctx HintContext) []string {
	actions := []string{
		"Use list_loki_label_names to verify available labels",
		"Use list_loki_label_values to check values for specific labels",
		"Use query_loki_stats to check if logs exist for the stream selector",
		"Try expanding the time range to see if logs exist in a different period",
	}

	// Add query-specific actions
	if strings.Contains(ctx.Query, "|") {
		actions = append(actions, "Try removing pipeline stages to see if the base stream selector matches any logs")
	}
	if strings.Contains(ctx.Query, "=~") {
		actions = append(actions, "Verify regex patterns are correct - use list_loki_label_values to see actual values")
	}

	return actions
}

// getClickHouseCauses returns possible causes for empty ClickHouse results
func getClickHouseCauses(ctx HintContext) []string {
	return []string{
		"The table may not contain data for the specified time range",
		"The WHERE clause filters may not match any rows",
		"The table or column names may be incorrect",
		"The time column filter may use an incorrect format",
	}
}

// getClickHouseActions returns suggested actions for empty ClickHouse results
func getClickHouseActions(ctx HintContext) []string {
	return []string{
		"Use list_clickhouse_tables to verify the table exists",
		"Use describe_clickhouse_table to check column names and types",
		"Try removing WHERE clause filters to see if the table contains data",
		"Verify time parameters are in Unix milliseconds format",
	}
}

// getCloudWatchCauses returns possible causes for empty CloudWatch results
func getCloudWatchCauses(ctx HintContext) []string {
	return []string{
		"The metric may not exist in the specified namespace",
		"The dimension values may not match any metrics",
		"The time range may be outside the data retention period",
		"The metric may not have been published during this time period",
	}
}

// getCloudWatchActions returns suggested actions for empty CloudWatch results
func getCloudWatchActions(ctx HintContext) []string {
	return []string{
		"Use list_cloudwatch_namespaces to verify available namespaces",
		"Use list_cloudwatch_metrics to check metrics in the namespace",
		"Use list_cloudwatch_dimensions to verify dimension values",
		"Try expanding the time range - CloudWatch data may have ingestion delays",
	}
}

// getGenericCauses returns generic causes for empty results
func getGenericCauses() []string {
	return []string{
		"No data exists for the specified query parameters",
		"The time range may not contain any data",
		"The query filters may be too restrictive",
	}
}

// getGenericActions returns generic actions for empty results
func getGenericActions() []string {
	return []string{
		"Try expanding the time range",
		"Review and simplify query filters",
		"Verify that the data source is configured correctly",
	}
}
