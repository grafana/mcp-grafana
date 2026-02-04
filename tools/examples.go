package tools

import (
	"context"
	"fmt"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// GetQueryExamplesParams defines the parameters for the get_query_examples tool.
type GetQueryExamplesParams struct {
	DatasourceType string `json:"datasourceType" jsonschema:"required,description=The datasource type to get examples for (e.g. 'prometheus'\\, 'loki'\\, 'clickhouse'\\, 'cloudwatch')"`
}

// QueryExample represents a single example query for a datasource.
type QueryExample struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Query       string            `json:"query"`
	Namespace   string            `json:"namespace,omitempty"`
	MetricName  string            `json:"metricName,omitempty"`
	Dimensions  map[string]string `json:"dimensions,omitempty"`
}

// GetQueryExamplesResult contains the result of the get_query_examples tool.
type GetQueryExamplesResult struct {
	DatasourceType string         `json:"datasourceType"`
	Examples       []QueryExample `json:"examples"`
}

var prometheusExamples = []QueryExample{
	{
		Name:        "Request rate",
		Description: "Calculate the per-second rate of HTTP requests over the last 5 minutes",
		Query:       "rate(http_requests_total[5m])",
	},
	{
		Name:        "95th percentile latency",
		Description: "Calculate the 95th percentile of request duration using histogram buckets",
		Query:       "histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))",
	},
	{
		Name:        "Up targets by job",
		Description: "Count the number of up targets grouped by job label",
		Query:       "sum by (job) (up)",
	},
	{
		Name:        "Memory usage percentage",
		Description: "Calculate memory usage as a percentage of total memory",
		Query:       "100 * (1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes))",
	},
	{
		Name:        "CPU usage by mode",
		Description: "Calculate CPU usage rate grouped by mode (user, system, idle, etc.)",
		Query:       "sum by (mode) (rate(node_cpu_seconds_total[5m]))",
	},
}

var lokiExamples = []QueryExample{
	{
		Name:        "Error logs",
		Description: "Find logs containing the word 'error' from nginx job",
		Query:       `{job="nginx"} |= "error"`,
	},
	{
		Name:        "JSON logs with status filter",
		Description: "Parse JSON logs and filter for HTTP status codes >= 500",
		Query:       `{namespace="prod"} | json | status >= 500`,
	},
	{
		Name:        "Log volume by status",
		Description: "Calculate log volume rate grouped by HTTP status code",
		Query:       `sum(rate({job="nginx"}[5m])) by (status)`,
	},
	{
		Name:        "Regex filter",
		Description: "Find logs matching a regex pattern for exception messages",
		Query:       `{job="app"} |~ "(?i)exception|error|fail"`,
	},
	{
		Name:        "Log line format",
		Description: "Parse and format log lines using logfmt parser",
		Query:       `{job="app"} | logfmt | level="error" | line_format "{{.msg}}"`,
	},
}

var clickhouseExamples = []QueryExample{
	{
		Name:        "Basic time-filtered query",
		Description: "Select all columns from a table with time filtering using Grafana macros",
		Query:       "SELECT * FROM $table WHERE $__timeFilter(timestamp)",
	},
	{
		Name:        "Time series count",
		Description: "Count records grouped by time intervals using Grafana macros",
		Query:       "SELECT $__timeInterval(timestamp) as time, count(*) as count FROM $table WHERE $__timeFilter(timestamp) GROUP BY time ORDER BY time",
	},
	{
		Name:        "Aggregation with conditions",
		Description: "Calculate average value with filtering and grouping",
		Query:       "SELECT $__timeInterval(timestamp) as time, avg(value) as avg_value FROM $table WHERE $__timeFilter(timestamp) AND status = 'active' GROUP BY time ORDER BY time",
	},
	{
		Name:        "Top N query",
		Description: "Find top 10 entries by count",
		Query:       "SELECT name, count(*) as cnt FROM $table WHERE $__timeFilter(timestamp) GROUP BY name ORDER BY cnt DESC LIMIT 10",
	},
}

var cloudwatchExamples = []QueryExample{
	{
		Name:        "ECS CPU Utilization",
		Description: "Monitor CPU utilization for ECS services",
		Query:       "",
		Namespace:   "AWS/ECS",
		MetricName:  "CPUUtilization",
		Dimensions:  map[string]string{"ClusterName": "*", "ServiceName": "*"},
	},
	{
		Name:        "ECS Memory Utilization",
		Description: "Monitor memory utilization for ECS services",
		Query:       "",
		Namespace:   "AWS/ECS",
		MetricName:  "MemoryUtilization",
		Dimensions:  map[string]string{"ClusterName": "*", "ServiceName": "*"},
	},
	{
		Name:        "EC2 CPU Utilization",
		Description: "Monitor CPU utilization for EC2 instances",
		Query:       "",
		Namespace:   "AWS/EC2",
		MetricName:  "CPUUtilization",
		Dimensions:  map[string]string{"InstanceId": "*"},
	},
	{
		Name:        "EC2 Network In",
		Description: "Monitor incoming network traffic for EC2 instances",
		Query:       "",
		Namespace:   "AWS/EC2",
		MetricName:  "NetworkIn",
		Dimensions:  map[string]string{"InstanceId": "*"},
	},
	{
		Name:        "EC2 Network Out",
		Description: "Monitor outgoing network traffic for EC2 instances",
		Query:       "",
		Namespace:   "AWS/EC2",
		MetricName:  "NetworkOut",
		Dimensions:  map[string]string{"InstanceId": "*"},
	},
	{
		Name:        "RDS Database Connections",
		Description: "Monitor the number of database connections for RDS instances",
		Query:       "",
		Namespace:   "AWS/RDS",
		MetricName:  "DatabaseConnections",
		Dimensions:  map[string]string{"DBInstanceIdentifier": "*"},
	},
	{
		Name:        "RDS CPU Utilization",
		Description: "Monitor CPU utilization for RDS instances",
		Query:       "",
		Namespace:   "AWS/RDS",
		MetricName:  "CPUUtilization",
		Dimensions:  map[string]string{"DBInstanceIdentifier": "*"},
	},
	{
		Name:        "Lambda Invocations",
		Description: "Monitor the number of Lambda function invocations",
		Query:       "",
		Namespace:   "AWS/Lambda",
		MetricName:  "Invocations",
		Dimensions:  map[string]string{"FunctionName": "*"},
	},
	{
		Name:        "Lambda Duration",
		Description: "Monitor Lambda function execution duration",
		Query:       "",
		Namespace:   "AWS/Lambda",
		MetricName:  "Duration",
		Dimensions:  map[string]string{"FunctionName": "*"},
	},
	{
		Name:        "Lambda Errors",
		Description: "Monitor Lambda function errors",
		Query:       "",
		Namespace:   "AWS/Lambda",
		MetricName:  "Errors",
		Dimensions:  map[string]string{"FunctionName": "*"},
	},
}

// supportedDatasourceTypes contains the list of supported datasource types for examples.
var supportedDatasourceTypes = []string{"prometheus", "loki", "clickhouse", "cloudwatch"}

func getQueryExamples(_ context.Context, args GetQueryExamplesParams) (*GetQueryExamplesResult, error) {
	datasourceType := strings.ToLower(args.DatasourceType)

	var examples []QueryExample
	switch datasourceType {
	case "prometheus":
		examples = prometheusExamples
	case "loki":
		examples = lokiExamples
	case "clickhouse":
		examples = clickhouseExamples
	case "cloudwatch":
		examples = cloudwatchExamples
	default:
		return nil, fmt.Errorf("unsupported datasource type: %s. Supported types are: %s",
			args.DatasourceType, strings.Join(supportedDatasourceTypes, ", "))
	}

	return &GetQueryExamplesResult{
		DatasourceType: datasourceType,
		Examples:       examples,
	}, nil
}

// GetQueryExamples is the MCP tool that provides example queries for each datasource type.
var GetQueryExamples = mcpgrafana.MustTool(
	"get_query_examples",
	"Get example queries for a specific datasource type. Provides sample queries with descriptions for Prometheus (PromQL), Loki (LogQL), ClickHouse (SQL with Grafana macros), and CloudWatch (metric configurations). Use this to understand query syntax and common patterns for each datasource. TIP: Use list_datasources to find datasource UIDs, or get_datasource_by_name if you know the exact name.",
	getQueryExamples,
	mcp.WithTitleAnnotation("Get query examples"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddExamplesTools registers all example-related tools to the MCP server.
func AddExamplesTools(mcp *server.MCPServer) {
	GetQueryExamples.Register(mcp)
}
