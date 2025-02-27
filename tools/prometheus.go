package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/config"
)

func promClientFromContext(ctx context.Context, uid string) (promv1.API, error) {
	grafanaURL, apiKey := mcpgrafana.GrafanaURLFromContext(ctx), mcpgrafana.GrafanaAPIKeyFromContext(ctx)
	url := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", strings.TrimRight(grafanaURL, "/"), uid)
	rt := api.DefaultRoundTripper
	if apiKey != "" {
		rt = config.NewAuthorizationCredentialsRoundTripper(
			"Bearer", config.NewInlineSecret(apiKey), rt,
		)
	}
	c, err := api.NewClient(api.Config{
		Address:      url,
		RoundTripper: rt,
	})
	if err != nil {
		return nil, fmt.Errorf("creating Prometheus client: %w", err)
	}
	return promv1.NewAPI(c), nil
}

type ListPrometheusMetricMetadataParams struct {
	DatasourceUID  string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Limit          int    `json:"limit" jsonschema:"description=The maximum number of metrics to return"`
	LimitPerMetric int    `json:"limitPerMetric" jsonschema:"description=The maximum number of metrics to return per metric"`
	Metric         string `json:"metric" jsonschema:"description=The metric to query"`
}

func ListPrometheusMetricMetadata(ctx context.Context, args ListPrometheusMetricMetadataParams) (*mcp.CallToolResult, error) {
	promClient, err := promClientFromContext(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting Prometheus client: %w", err)
	}

	limit := args.Limit
	if limit == 0 {
		limit = 10
	}

	metadata, err := promClient.Metadata(ctx, args.Metric, fmt.Sprintf("%d", limit))
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus metric metadata: %w", err)
	}
	b, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshalling Prometheus metric metadata: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

var ListPrometheusMetricMetadataTool, ListPrometheusMetricMetadataHandler = mcpgrafana.MustTool(
	"list_prometheus_metric_metadata",
	"List Prometheus metric metadata",
	ListPrometheusMetricMetadata,
)

type QueryPrometheusParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Expr          string `json:"expr" jsonschema:"required,description=The PromQL expression to query"`
	StartRFC3339  string `json:"startRfc3339" jsonschema:"required,description=The start time in RFC3339 format"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=The end time in RFC3339 format. Ignored if queryType is 'instant'"`
	StepSeconds   int    `json:"stepSeconds,omitempty" jsonschema:"description=The time series step size in seconds. Ignored if queryType is 'instant'"`
	QueryType     string `json:"queryType,omitempty" jsonschema:"description=The type of query to use. Either 'range' or 'instant'"`
}

func QueryPrometheus(ctx context.Context, args QueryPrometheusParams) (*mcp.CallToolResult, error) {
	promClient, err := promClientFromContext(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting Prometheus client: %w", err)
	}

	queryType := args.QueryType
	if queryType == "" {
		queryType = "range"
	}

	startTime, err := time.Parse(time.RFC3339, args.StartRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}

	if queryType == "range" {
		if args.EndRFC3339 == "" || args.StepSeconds == 0 {
			return nil, fmt.Errorf("endRfc3339 and stepSeconds must be provided when queryType is 'range'")
		}

		endTime, err := time.Parse(time.RFC3339, args.EndRFC3339)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}

		step := time.Duration(args.StepSeconds) * time.Second
		result, _, err := promClient.QueryRange(ctx, args.Expr, promv1.Range{
			Start: startTime,
			End:   endTime,
			Step:  step,
		})
		if err != nil {
			return nil, fmt.Errorf("querying Prometheus range: %w", err)
		}

		b, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshalling Prometheus query result: %w", err)
		}
		return mcp.NewToolResultText(string(b)), nil
	} else if queryType == "instant" {
		result, _, err := promClient.Query(ctx, args.Expr, startTime)
		if err != nil {
			return nil, fmt.Errorf("querying Prometheus instant: %w", err)
		}

		b, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshalling Prometheus query result: %w", err)
		}
		return mcp.NewToolResultText(string(b)), nil
	}

	return nil, fmt.Errorf("invalid query type: %s", queryType)
}

var QueryPrometheusTool, QueryPrometheusHandler = mcpgrafana.MustTool(
	"query_prometheus",
	"Query Prometheus using a range or instant request",
	QueryPrometheus,
)

type ListPrometheusMetricNamesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Regex         string `json:"regex" jsonschema:"description=The regex to match against the metric names"`
	Limit         int    `json:"limit,omitempty" jsonschema:"description=The maximum number of results to return"`
	Page          int    `json:"page,omitempty" jsonschema:"description=The page number to return"`
}

func ListPrometheusMetricNames(ctx context.Context, args ListPrometheusMetricNamesParams) (*mcp.CallToolResult, error) {
	promClient, err := promClientFromContext(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting Prometheus client: %w", err)
	}

	limit := args.Limit
	if limit == 0 {
		limit = 10
	}

	page := args.Page
	if page == 0 {
		page = 1
	}

	// Get all metric names by querying for __name__ label values
	labelValues, _, err := promClient.LabelValues(ctx, "__name__", nil, time.Time{}, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus metric names: %w", err)
	}

	// Filter by regex if provided
	matches := []string{}
	if args.Regex != "" {
		re, err := regexp.Compile(args.Regex)
		if err != nil {
			return nil, fmt.Errorf("compiling regex: %w", err)
		}
		for _, val := range labelValues {
			if re.MatchString(string(val)) {
				matches = append(matches, string(val))
			}
		}
	} else {
		for _, val := range labelValues {
			matches = append(matches, string(val))
		}
	}

	// Apply pagination
	start := (page - 1) * limit
	end := start + limit
	if start >= len(matches) {
		matches = []string{}
	} else if end > len(matches) {
		matches = matches[start:]
	} else {
		matches = matches[start:end]
	}

	b, err := json.Marshal(matches)
	if err != nil {
		return nil, fmt.Errorf("marshalling Prometheus metric names: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

var ListPrometheusMetricNamesTool, ListPrometheusMetricNamesHandler = mcpgrafana.MustTool(
	"list_prometheus_metric_names",
	"List metric names in a Prometheus datasource that match the given regex",
	ListPrometheusMetricNames,
)

type LabelMatcher struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"` // "=" or "!=" or "=~" or "!~"
}

type Selector struct {
	Filters []LabelMatcher `json:"filters"`
}

func (s Selector) String() string {
	b := strings.Builder{}
	b.WriteRune('{')
	for i, f := range s.Filters {
		b.WriteString(fmt.Sprintf(`%s%s'%s'`, f.Name, f.Type, f.Value))
		if i < len(s.Filters)-1 {
			b.WriteString(", ")
		}
	}
	b.WriteRune('}')
	return b.String()
}

type ListPrometheusLabelNamesParams struct {
	DatasourceUID string     `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Matches       []Selector `json:"matches,omitempty" jsonschema:"description=Optionally, a list of label matchers to filter the results by"`
	StartRFC3339  string     `json:"startRfc3339,omitempty" jsonschema:"description=Optionally, the start time of the time range to filter the results by"`
	EndRFC3339    string     `json:"endRfc3339,omitempty" jsonschema:"description=Optionally, the end time of the time range to filter the results by"`
	Limit         int        `json:"limit,omitempty" jsonschema:"description=Optionally, the maximum number of results to return"`
}

func ListPrometheusLabelNames(ctx context.Context, args ListPrometheusLabelNamesParams) (*mcp.CallToolResult, error) {
	promClient, err := promClientFromContext(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting Prometheus client: %w", err)
	}

	limit := args.Limit
	if limit == 0 {
		limit = 100
	}

	var startTime, endTime time.Time
	if args.StartRFC3339 != "" {
		if startTime, err = time.Parse(time.RFC3339, args.StartRFC3339); err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
	}
	if args.EndRFC3339 != "" {
		if endTime, err = time.Parse(time.RFC3339, args.EndRFC3339); err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
	}

	var matchers []string
	for _, m := range args.Matches {
		matchers = append(matchers, m.String())
	}

	labelNames, _, err := promClient.LabelNames(ctx, matchers, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus label names: %w", err)
	}

	// Apply limit
	if len(labelNames) > limit {
		labelNames = labelNames[:limit]
	}

	b, err := json.Marshal(labelNames)
	if err != nil {
		return nil, fmt.Errorf("marshalling Prometheus label names: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

var ListPrometheusLabelNamesTool, ListPrometheusLabelNamesHandler = mcpgrafana.MustTool(
	"list_prometheus_label_names",
	"List the label names in a Prometheus datasource",
	ListPrometheusLabelNames,
)

type ListPrometheusLabelValuesParams struct {
	DatasourceUID string     `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	LabelName     string     `json:"labelName" jsonschema:"required,description=The name of the label to query"`
	Matches       []Selector `json:"matches,omitempty" jsonschema:"description=Optionally, a list of selectors to filter the results by"`
	StartRFC3339  string     `json:"startRfc3339,omitempty" jsonschema:"description=Optionally, the start time of the query"`
	EndRFC3339    string     `json:"endRfc3339,omitempty" jsonschema:"description=Optionally, the end time of the query"`
	Limit         int        `json:"limit,omitempty" jsonschema:"description=Optionally, the maximum number of results to return"`
}

func ListPrometheusLabelValues(ctx context.Context, args ListPrometheusLabelValuesParams) (*mcp.CallToolResult, error) {
	promClient, err := promClientFromContext(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting Prometheus client: %w", err)
	}

	limit := args.Limit
	if limit == 0 {
		limit = 100
	}

	var startTime, endTime time.Time
	if args.StartRFC3339 != "" {
		if startTime, err = time.Parse(time.RFC3339, args.StartRFC3339); err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
	}
	if args.EndRFC3339 != "" {
		if endTime, err = time.Parse(time.RFC3339, args.EndRFC3339); err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
	}

	var matchers []string
	for _, m := range args.Matches {
		matchers = append(matchers, m.String())
	}

	labelValues, _, err := promClient.LabelValues(ctx, args.LabelName, matchers, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus label values: %w", err)
	}

	// Apply limit
	if len(labelValues) > limit {
		labelValues = labelValues[:limit]
	}

	b, err := json.Marshal(labelValues)
	if err != nil {
		return nil, fmt.Errorf("marshalling Prometheus label values: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

var ListPrometheusLabelValuesTool, ListPrometheusLabelValuesHandler = mcpgrafana.MustTool(
	"list_prometheus_label_values",
	"Get the values of a label in Prometheus",
	ListPrometheusLabelValues,
)

func AddPrometheusTools(mcp *server.MCPServer) {
	mcp.AddTool(ListPrometheusMetricMetadataTool, ListPrometheusMetricMetadataHandler)
	mcp.AddTool(QueryPrometheusTool, QueryPrometheusHandler)
	mcp.AddTool(ListPrometheusMetricNamesTool, ListPrometheusMetricNamesHandler)
	mcp.AddTool(ListPrometheusLabelNamesTool, ListPrometheusLabelNamesHandler)
	mcp.AddTool(ListPrometheusLabelValuesTool, ListPrometheusLabelValuesHandler)
}
