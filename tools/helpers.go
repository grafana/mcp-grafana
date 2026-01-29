package tools

import (
	"context"
	"fmt"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/common/model"
)

// QueryPrometheusHistogramParams defines the parameters for querying histogram percentiles
type QueryPrometheusHistogramParams struct {
	DatasourceUID string  `json:"datasourceUid" jsonschema:"required,description=The UID of the Prometheus datasource"`
	Metric        string  `json:"metric" jsonschema:"required,description=Base histogram metric name (without _bucket suffix)"`
	Percentile    float64 `json:"percentile" jsonschema:"required,description=Percentile to calculate (e.g. 50\\, 90\\, 95\\, 99)"`
	Labels        string  `json:"labels,omitempty" jsonschema:"description=Label selector (e.g. job=\"api\"\\, service=\"gateway\")"`
	RateInterval  string  `json:"rateInterval,omitempty" jsonschema:"description=Rate interval for the query (default: 5m)"`
	StartTime     string  `json:"startTime,omitempty" jsonschema:"description=Start time (default: now-1h). Supports RFC3339 or relative time."`
	EndTime       string  `json:"endTime,omitempty" jsonschema:"description=End time (default: now). Supports RFC3339 or relative time."`
	StepSeconds   int     `json:"stepSeconds,omitempty" jsonschema:"description=Step size in seconds for range query (default: 60)"`
}

// queryPrometheusHistogram generates and executes a histogram percentile query
func queryPrometheusHistogram(ctx context.Context, args QueryPrometheusHistogramParams) (model.Value, error) {
	// Set defaults
	rateInterval := args.RateInterval
	if rateInterval == "" {
		rateInterval = "5m"
	}

	startTime := args.StartTime
	if startTime == "" {
		startTime = "now-1h"
	}

	endTime := args.EndTime
	if endTime == "" {
		endTime = "now"
	}

	stepSeconds := args.StepSeconds
	if stepSeconds == 0 {
		stepSeconds = 60
	}

	// Convert percentile to quantile (e.g., 95 -> 0.95)
	quantile := args.Percentile / 100.0

	// Build the label selector
	labelSelector := ""
	if args.Labels != "" {
		labelSelector = args.Labels
	}

	// Build the PromQL expression for histogram_quantile
	// histogram_quantile(0.95, sum(rate(metric_bucket{labels}[5m])) by (le))
	var expr string
	if labelSelector != "" {
		expr = fmt.Sprintf(
			"histogram_quantile(%g, sum(rate(%s_bucket{%s}[%s])) by (le))",
			quantile, args.Metric, labelSelector, rateInterval,
		)
	} else {
		expr = fmt.Sprintf(
			"histogram_quantile(%g, sum(rate(%s_bucket[%s])) by (le))",
			quantile, args.Metric, rateInterval,
		)
	}

	// Execute the query using the existing queryPrometheus function
	return queryPrometheus(ctx, QueryPrometheusParams{
		DatasourceUID: args.DatasourceUID,
		Expr:          expr,
		StartTime:     startTime,
		EndTime:       endTime,
		StepSeconds:   stepSeconds,
		QueryType:     "range",
	})
}

// QueryPrometheusHistogram is a tool for querying histogram percentiles
var QueryPrometheusHistogram = mcpgrafana.MustTool(
	"query_prometheus_histogram",
	`Query Prometheus histogram percentiles. DISCOVER FIRST: Use list_prometheus_metric_names with regex='.*_bucket$' to find histograms.

Generates histogram_quantile PromQL. Example: metric='http_duration', percentile=95, labels='job="api"'`,
	queryPrometheusHistogram,
	mcp.WithTitleAnnotation("Query Prometheus histogram percentile"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// GetQueryExamplesParams defines the parameters for getting query examples
type GetQueryExamplesParams struct {
	DatasourceUID  string `json:"datasourceUid,omitempty" jsonschema:"description=Optional datasource UID to get examples for a specific datasource"`
	DatasourceType string `json:"datasourceType,omitempty" jsonschema:"enum=prometheus,enum=loki,enum=cloudwatch,enum=clickhouse,description=Datasource type to get examples for"`
}

// QueryExample represents a query example
type QueryExample struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Query       string `json:"query"`
}

// QueryExamplesResult contains examples for a datasource type
type QueryExamplesResult struct {
	DatasourceType string         `json:"datasourceType"`
	Examples       []QueryExample `json:"examples"`
}

// getQueryExamples returns query examples for different datasource types
func getQueryExamples(ctx context.Context, args GetQueryExamplesParams) (*QueryExamplesResult, error) {
	dsType := args.DatasourceType

	// If datasourceUID is provided, look up the type
	if dsType == "" && args.DatasourceUID != "" {
		ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
		if err != nil {
			return nil, fmt.Errorf("getting datasource: %w", err)
		}
		// Map datasource type to our supported types
		switch {
		case strings.Contains(ds.Type, "prometheus"):
			dsType = "prometheus"
		case strings.Contains(ds.Type, "loki"):
			dsType = "loki"
		case strings.Contains(ds.Type, "cloudwatch"):
			dsType = "cloudwatch"
		case strings.Contains(ds.Type, "clickhouse"):
			dsType = "clickhouse"
		default:
			return nil, fmt.Errorf("unsupported datasource type: %s", ds.Type)
		}
	}

	if dsType == "" {
		return nil, fmt.Errorf("either datasourceUid or datasourceType must be provided")
	}

	result := &QueryExamplesResult{
		DatasourceType: dsType,
	}

	switch dsType {
	case "prometheus":
		result.Examples = []QueryExample{
			{
				Name:        "CPU Usage",
				Description: "Average CPU usage per instance",
				Query:       `avg(rate(node_cpu_seconds_total{mode!="idle"}[5m])) by (instance)`,
			},
			{
				Name:        "Memory Usage",
				Description: "Memory usage percentage",
				Query:       `(1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) * 100`,
			},
			{
				Name:        "HTTP Request Rate",
				Description: "HTTP requests per second by status code",
				Query:       `sum(rate(http_requests_total[5m])) by (status_code)`,
			},
			{
				Name:        "Error Rate",
				Description: "Error rate as percentage of total requests",
				Query:       `sum(rate(http_requests_total{status_code=~"5.."}[5m])) / sum(rate(http_requests_total[5m])) * 100`,
			},
			{
				Name:        "Histogram Percentile (P95)",
				Description: "95th percentile latency from histogram",
				Query:       `histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[5m])) by (le))`,
			},
			{
				Name:        "Top 5 by CPU",
				Description: "Top 5 instances by CPU usage",
				Query:       `topk(5, avg(rate(node_cpu_seconds_total{mode!="idle"}[5m])) by (instance))`,
			},
		}

	case "loki":
		result.Examples = []QueryExample{
			{
				Name:        "Error Logs",
				Description: "Filter logs containing error",
				Query:       `{job="myapp"} |= "error"`,
			},
			{
				Name:        "JSON Log Parsing",
				Description: "Parse JSON logs and filter by level",
				Query:       `{job="myapp"} | json | level="error"`,
			},
			{
				Name:        "Log Count Rate",
				Description: "Count of logs per minute",
				Query:       `sum(rate({job="myapp"}[1m]))`,
			},
			{
				Name:        "Error Rate",
				Description: "Rate of error logs",
				Query:       `sum(rate({job="myapp"} |= "error"[5m]))`,
			},
			{
				Name:        "Top Error Messages",
				Description: "Most common error patterns",
				Query:       `topk(10, sum by (message) (count_over_time({job="myapp"} | json | level="error" [1h])))`,
			},
			{
				Name:        "Regex Filter",
				Description: "Filter logs using regex",
				Query:       `{job="myapp"} |~ "timeout|connection refused"`,
			},
		}

	case "cloudwatch":
		result.Examples = []QueryExample{
			{
				Name:        "ECS CPU Utilization",
				Description: "CPU utilization for ECS service",
				Query:       `Namespace: AWS/ECS, MetricName: CPUUtilization, Dimensions: {ClusterName: "my-cluster", ServiceName: "my-service"}`,
			},
			{
				Name:        "EC2 Network In",
				Description: "Network bytes received by EC2 instance",
				Query:       `Namespace: AWS/EC2, MetricName: NetworkIn, Dimensions: {InstanceId: "i-1234567890abcdef0"}`,
			},
			{
				Name:        "RDS Connections",
				Description: "Database connections for RDS instance",
				Query:       `Namespace: AWS/RDS, MetricName: DatabaseConnections, Dimensions: {DBInstanceIdentifier: "my-database"}`,
			},
			{
				Name:        "Lambda Invocations",
				Description: "Lambda function invocation count",
				Query:       `Namespace: AWS/Lambda, MetricName: Invocations, Dimensions: {FunctionName: "my-function"}`,
			},
			{
				Name:        "SQS Queue Depth",
				Description: "Number of messages in SQS queue",
				Query:       `Namespace: AWS/SQS, MetricName: ApproximateNumberOfMessagesVisible, Dimensions: {QueueName: "my-queue"}`,
			},
		}

	case "clickhouse":
		result.Examples = []QueryExample{
			{
				Name:        "Time Series Query",
				Description: "Query with time filter macro",
				Query:       `SELECT toStartOfMinute(timestamp) as time, count() as count FROM logs WHERE $__timeFilter(timestamp) GROUP BY time ORDER BY time`,
			},
			{
				Name:        "OTel Logs Query",
				Description: "Query OpenTelemetry logs",
				Query:       `SELECT Timestamp, ServiceName, SeverityText, Body FROM otel_logs WHERE $__timeFilter(Timestamp) AND ServiceName = 'my-service' ORDER BY Timestamp DESC`,
			},
			{
				Name:        "Aggregation with Interval",
				Description: "Aggregate data using interval macro",
				Query:       `SELECT toStartOfInterval(timestamp, INTERVAL $__interval) as time, avg(value) as avg_value FROM metrics WHERE $__timeFilter(timestamp) GROUP BY time ORDER BY time`,
			},
			{
				Name:        "Top Errors",
				Description: "Find top error messages",
				Query:       `SELECT Body, count() as count FROM otel_logs WHERE $__timeFilter(Timestamp) AND SeverityText = 'ERROR' GROUP BY Body ORDER BY count DESC LIMIT 10`,
			},
			{
				Name:        "Service Metrics",
				Description: "Metrics aggregated by service",
				Query:       `SELECT ServiceName, count() as log_count, countIf(SeverityText = 'ERROR') as error_count FROM otel_logs WHERE $__timeFilter(Timestamp) GROUP BY ServiceName ORDER BY log_count DESC`,
			},
		}

	default:
		return nil, fmt.Errorf("unsupported datasource type: %s", dsType)
	}

	return result, nil
}

// GetQueryExamples is a tool for getting query examples
var GetQueryExamples = mcpgrafana.MustTool(
	"get_query_examples",
	"Get example queries for a datasource type. Useful for learning query syntax and common patterns for Prometheus (PromQL), Loki (LogQL), CloudWatch, and ClickHouse.",
	getQueryExamples,
	mcp.WithTitleAnnotation("Get query examples"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// GenerateEmptyResultHints generates helpful hints when a query returns no data
func GenerateEmptyResultHints(datasourceType string) []string {
	hints := []string{"No data found. Possible reasons:"}

	switch datasourceType {
	case "prometheus":
		hints = append(hints,
			"- Metric name may not exist - use list_prometheus_metric_names to discover available metrics",
			"- Label selector may not match - use list_prometheus_label_values to check valid values",
			"- Time range may have no data - try extending with startTime=\"now-24h\"",
			"- Check if the metric is being scraped by the targets",
		)
	case "loki":
		hints = append(hints,
			"- Label selector may not match - use list_loki_label_names and list_loki_label_values to discover",
			"- Filter pattern may be too restrictive - try removing some filters",
			"- Try a broader query like {job=~\".+\"} first to verify data exists",
			"- Time range may have no logs - try extending with startRfc3339 set to earlier time",
		)
	case "cloudwatch":
		hints = append(hints,
			"- Namespace may not exist - use list_cloudwatch_namespaces to discover available namespaces",
			"- Metric name may be incorrect - use list_cloudwatch_metrics to find valid metrics",
			"- Dimensions may not match - use list_cloudwatch_dimensions to check valid dimension keys",
			"- Region may be incorrect - check if metrics exist in the specified region",
			"- Time range may have no data - try extending with start=\"now-6h\"",
		)
	case "clickhouse":
		hints = append(hints,
			"- Table may not exist - use list_clickhouse_tables to discover available tables",
			"- Column names may be incorrect - use describe_clickhouse_table to check schema",
			"- WHERE clause may be too restrictive - try removing some conditions",
			"- Time filter may not match any rows - verify the timestamp column name and format",
			"- Database name may be wrong - check the database parameter",
		)
	default:
		hints = append(hints,
			"- Verify the query syntax is correct",
			"- Check if the datasource is accessible",
			"- Try a simpler query to verify connectivity",
		)
	}

	return hints
}

// AddHelperTools registers all helper tools with the MCP server
func AddHelperTools(mcp *server.MCPServer) {
	QueryPrometheusHistogram.Register(mcp)
	GetQueryExamples.Register(mcp)
}
