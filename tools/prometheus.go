package tools

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
)

var (
	matchTypeMap = map[string]labels.MatchType{
		"":   labels.MatchEqual,
		"=":  labels.MatchEqual,
		"!=": labels.MatchNotEqual,
		"=~": labels.MatchRegexp,
		"!~": labels.MatchNotRegexp,
	}
)

func promClientFromContext(ctx context.Context, uid string) (promv1.API, error) {
	// First check if the datasource exists
	_, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	url := fmt.Sprintf("%s/api/datasources/uid/%s/resources", strings.TrimRight(cfg.URL, "/"), uid)

	// Create custom transport with TLS configuration if available
	rt, err := mcpgrafana.BuildTransport(&cfg, api.DefaultRoundTripper)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	if cfg.AccessToken != "" && cfg.IDToken != "" {
		rt = config.NewHeadersRoundTripper(&config.Headers{
			Headers: map[string]config.Header{
				"X-Access-Token": {
					Secrets: []config.Secret{config.Secret(cfg.AccessToken)},
				},
				"X-Grafana-Id": {
					Secrets: []config.Secret{config.Secret(cfg.IDToken)},
				},
			},
		}, rt)
	} else if cfg.APIKey != "" {
		rt = config.NewAuthorizationCredentialsRoundTripper(
			"Bearer", config.NewInlineSecret(cfg.APIKey), rt,
		)
	} else if cfg.BasicAuth != nil {
		password, _ := cfg.BasicAuth.Password()
		rt = config.NewBasicAuthRoundTripper(config.NewInlineSecret(cfg.BasicAuth.Username()), config.NewInlineSecret(password), rt)
	}

	// Wrap with org ID support
	rt = mcpgrafana.NewOrgIDRoundTripper(rt, cfg.OrgID)

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
	Limit          int    `json:"limit" jsonschema:"default=10,description=The maximum number of metrics to return"`
	LimitPerMetric int    `json:"limitPerMetric" jsonschema:"description=The maximum number of metrics to return per metric"`
	Metric         string `json:"metric" jsonschema:"description=The metric to query"`
}

func listPrometheusMetricMetadata(ctx context.Context, args ListPrometheusMetricMetadataParams) (map[string][]promv1.Metadata, error) {
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
	return metadata, nil
}

var ListPrometheusMetricMetadata = mcpgrafana.MustTool(
	"list_prometheus_metric_metadata",
	"List Prometheus metric metadata. Returns metadata about metrics currently scraped from targets. Note: This endpoint is experimental.",
	listPrometheusMetricMetadata,
	mcp.WithTitleAnnotation("List Prometheus metric metadata"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type QueryPrometheusParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Expr          string `json:"expr" jsonschema:"required,description=The PromQL expression to query"`
	StartTime     string `json:"startTime" jsonschema:"required,description=The start time. Supported formats are RFC3339 or relative to now (e.g. 'now'\\, 'now-1.5h'\\, 'now-2h45m'). Valid time units are 'ns'\\, 'us' (or 'µs')\\, 'ms'\\, 's'\\, 'm'\\, 'h'\\, 'd'."`
	EndTime       string `json:"endTime,omitempty" jsonschema:"description=The end time. Required if queryType is 'range'\\, ignored if queryType is 'instant' Supported formats are RFC3339 or relative to now (e.g. 'now'\\, 'now-1.5h'\\, 'now-2h45m'). Valid time units are 'ns'\\, 'us' (or 'µs')\\, 'ms'\\, 's'\\, 'm'\\, 'h'\\, 'd'."`
	StepSeconds   int    `json:"stepSeconds,omitempty" jsonschema:"description=The time series step size in seconds. Required if queryType is 'range'\\, ignored if queryType is 'instant'"`
	QueryType     string `json:"queryType,omitempty" jsonschema:"description=The type of query to use. Either 'range' or 'instant'"`
}

// QueryPrometheusResult wraps the Prometheus query result with optional hints
type QueryPrometheusResult struct {
	Data  model.Value       `json:"data"`
	Hints *EmptyResultHints `json:"hints,omitempty"`
}

func parseTime(timeStr string) (time.Time, error) {
	tr := gtime.TimeRange{
		From: timeStr,
		Now:  time.Now(),
	}
	return tr.ParseFrom()
}

// isPrometheusResultEmpty checks if a Prometheus query result contains no data
func isPrometheusResultEmpty(result model.Value) bool {
	if result == nil {
		return true
	}
	switch v := result.(type) {
	case model.Vector:
		return len(v) == 0
	case model.Matrix:
		return len(v) == 0
	case *model.Scalar:
		return v == nil // Scalars are never "empty" if they exist
	case *model.String:
		return v == nil || v.Value == ""
	default:
		return false
	}
}

// queryPrometheus executes a PromQL query and returns raw results.
// This is the internal function - use queryPrometheusWithHints for MCP tools.
func queryPrometheus(ctx context.Context, args QueryPrometheusParams) (model.Value, error) {
	promClient, err := promClientFromContext(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting Prometheus client: %w", err)
	}

	queryType := args.QueryType
	if queryType == "" {
		queryType = "range"
	}

	var startTime time.Time
	startTime, err = parseTime(args.StartTime)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}

	var result model.Value

	switch queryType {
	case "range":
		if args.StepSeconds == 0 {
			return nil, fmt.Errorf("stepSeconds must be provided when queryType is 'range'")
		}

		endTime, err := parseTime(args.EndTime)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}

		step := time.Duration(args.StepSeconds) * time.Second
		result, _, err = promClient.QueryRange(ctx, args.Expr, promv1.Range{
			Start: startTime,
			End:   endTime,
			Step:  step,
		})
		if err != nil {
			return nil, fmt.Errorf("querying Prometheus range: %w", err)
		}
	case "instant":
		result, _, err = promClient.Query(ctx, args.Expr, startTime)
		if err != nil {
			return nil, fmt.Errorf("querying Prometheus instant: %w", err)
		}
	default:
		return nil, fmt.Errorf("invalid query type: %s", queryType)
	}

	return result, nil
}

// queryPrometheusWithHints wraps queryPrometheus and adds hints for empty results.
// This is the MCP tool handler - hints are added at this layer, not in the internal function.
func queryPrometheusWithHints(ctx context.Context, args QueryPrometheusParams) (*QueryPrometheusResult, error) {
	result, err := queryPrometheus(ctx, args)
	if err != nil {
		return nil, err
	}

	response := &QueryPrometheusResult{
		Data: result,
	}

	// Add hints if the result is empty
	if isPrometheusResultEmpty(result) {
		startTime, _ := parseTime(args.StartTime)
		endTime, _ := parseTime(args.EndTime)
		response.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: "prometheus",
			Query:          args.Expr,
			StartTime:      startTime,
			EndTime:        endTime,
		})
	}

	return response, nil
}

var QueryPrometheus = mcpgrafana.MustTool(
	"query_prometheus",
	"WORKFLOW: list_prometheus_metric_names -> list_prometheus_label_values -> query_prometheus. Query Prometheus using a PromQL expression. Supports instant queries (single point) and range queries (time range). Time: RFC3339 or relative expressions like 'now'\\, 'now-1h'.",
	queryPrometheusWithHints,
	mcp.WithTitleAnnotation("Query Prometheus metrics"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListPrometheusMetricNamesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Regex         string `json:"regex" jsonschema:"description=The regex to match against the metric names"`
	Limit         int    `json:"limit,omitempty" jsonschema:"default=10,description=The maximum number of results to return"`
	Page          int    `json:"page,omitempty" jsonschema:"default=1,description=The page number to return"`
}

func listPrometheusMetricNames(ctx context.Context, args ListPrometheusMetricNamesParams) ([]string, error) {
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

	return matches, nil
}

var ListPrometheusMetricNames = mcpgrafana.MustTool(
	"list_prometheus_metric_names",
	"DISCOVERY: Call this first to find available metrics before querying. Lists metric names in a Prometheus datasource. Retrieves all metric names and filters them using the provided regex. Supports pagination.",
	listPrometheusMetricNames,
	mcp.WithTitleAnnotation("List Prometheus metric names"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type LabelMatcher struct {
	Name  string `json:"name" jsonschema:"required,description=The name of the label to match against"`
	Value string `json:"value" jsonschema:"required,description=The value to match against"`
	Type  string `json:"type" jsonschema:"required,description=One of the '=' or '!=' or '=~' or '!~'"`
}

type Selector struct {
	Filters []LabelMatcher `json:"filters"`
}

func (s Selector) String() string {
	b := strings.Builder{}
	b.WriteRune('{')
	for i, f := range s.Filters {
		if f.Type == "" {
			f.Type = "="
		}
		b.WriteString(fmt.Sprintf(`%s%s'%s'`, f.Name, f.Type, f.Value))
		if i < len(s.Filters)-1 {
			b.WriteString(", ")
		}
	}
	b.WriteRune('}')
	return b.String()
}

// Matches runs the matchers against the given labels and returns whether they match the selector.
func (s Selector) Matches(lbls labels.Labels) (bool, error) {
	matchers := make(labels.Selector, 0, len(s.Filters))

	for _, filter := range s.Filters {
		matchType, ok := matchTypeMap[filter.Type]
		if !ok {
			return false, fmt.Errorf("invalid matcher type: %s", filter.Type)
		}

		matcher, err := labels.NewMatcher(matchType, filter.Name, filter.Value)
		if err != nil {
			return false, fmt.Errorf("creating matcher: %w", err)
		}

		matchers = append(matchers, matcher)
	}

	return matchers.Matches(lbls), nil
}

type ListPrometheusLabelNamesParams struct {
	DatasourceUID string     `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Matches       []Selector `json:"matches,omitempty" jsonschema:"description=Optionally\\, a list of label matchers to filter the results by"`
	StartRFC3339  string     `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the time range to filter the results by"`
	EndRFC3339    string     `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the time range to filter the results by"`
	Limit         int        `json:"limit,omitempty" jsonschema:"default=100,description=Optionally\\, the maximum number of results to return"`
}

func listPrometheusLabelNames(ctx context.Context, args ListPrometheusLabelNamesParams) ([]string, error) {
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

	return labelNames, nil
}

var ListPrometheusLabelNames = mcpgrafana.MustTool(
	"list_prometheus_label_names",
	"List label names in a Prometheus datasource. Allows filtering by series selectors and time range.",
	listPrometheusLabelNames,
	mcp.WithTitleAnnotation("List Prometheus label names"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListPrometheusLabelValuesParams struct {
	DatasourceUID string     `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	LabelName     string     `json:"labelName" jsonschema:"required,description=The name of the label to query"`
	Matches       []Selector `json:"matches,omitempty" jsonschema:"description=Optionally\\, a list of selectors to filter the results by"`
	StartRFC3339  string     `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query"`
	EndRFC3339    string     `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query"`
	Limit         int        `json:"limit,omitempty" jsonschema:"default=100,description=Optionally\\, the maximum number of results to return"`
}

func listPrometheusLabelValues(ctx context.Context, args ListPrometheusLabelValuesParams) (model.LabelValues, error) {
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

	return labelValues, nil
}

var ListPrometheusLabelValues = mcpgrafana.MustTool(
	"list_prometheus_label_values",
	"Use after list_prometheus_metric_names to find label values for filtering queries. Gets the values for a specific label name in Prometheus. Allows filtering by series selectors and time range.",
	listPrometheusLabelValues,
	mcp.WithTitleAnnotation("List Prometheus label values"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// PrometheusHistogramResult wraps histogram query results with debugging info
type PrometheusHistogramResult struct {
	Result model.Value `json:"result"`
	Query  string      `json:"query"` // Generated PromQL for debugging
	Hints  []string    `json:"hints,omitempty"`
}

// QueryPrometheusHistogramParams defines the parameters for querying histogram percentiles
type QueryPrometheusHistogramParams struct {
	DatasourceUID string  `json:"datasourceUid" jsonschema:"required,description=The UID of the Prometheus datasource"`
	Metric        string  `json:"metric" jsonschema:"required,description=Base histogram metric name (without _bucket suffix)"`
	Percentile    float64 `json:"percentile" jsonschema:"required,description=Percentile to calculate (e.g. 50\\, 90\\, 95\\, 99)"`
	Labels        string  `json:"labels,omitempty" jsonschema:"description=Label selector (e.g. job=\"api\"\\, service=\"gateway\")"`
	RateInterval  string  `json:"rateInterval,omitempty" jsonschema:"description=Rate interval for the query (default: 5m)"`
	StartTime     string  `json:"startTime,omitempty" jsonschema:"description=Start time (default: now-1h). Supports RFC3339\\, relative (now-1h)\\, or Unix ms."`
	EndTime       string  `json:"endTime,omitempty" jsonschema:"description=End time (default: now). Supports RFC3339\\, relative\\, or Unix ms."`
	StepSeconds   int     `json:"stepSeconds,omitempty" jsonschema:"description=Step size in seconds for range query (default: 60)"`
}

// queryPrometheusHistogram generates and executes a histogram percentile query
func queryPrometheusHistogram(ctx context.Context, args QueryPrometheusHistogramParams) (*PrometheusHistogramResult, error) {
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

	// Validate percentile is in valid range
	if args.Percentile < 0 || args.Percentile > 100 {
		return nil, fmt.Errorf("percentile must be between 0 and 100, got %g", args.Percentile)
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
	result, err := queryPrometheus(ctx, QueryPrometheusParams{
		DatasourceUID: args.DatasourceUID,
		Expr:          expr,
		StartTime:     startTime,
		EndTime:       endTime,
		StepSeconds:   stepSeconds,
		QueryType:     "range",
	})
	if err != nil {
		return nil, err
	}

	// Generate hints if result is empty or contains NaN
	var hints []string
	if isPrometheusResultEmptyOrNaN(result) {
		hints = []string{
			"No data found or result is NaN. Possible reasons:",
			"- Histogram metric may not exist - use list_prometheus_metric_names with regex='.*_bucket$'",
			"- Label selector may not match any series - verify labels with list_prometheus_label_values",
			"- Time range may have no data - try extending with startTime",
			"- Metric may not be a histogram (missing _bucket suffix)",
		}
	}

	return &PrometheusHistogramResult{
		Result: result,
		Query:  expr,
		Hints:  hints,
	}, nil
}

// isPrometheusResultEmptyOrNaN checks if a Prometheus result is empty or contains only NaN values
func isPrometheusResultEmptyOrNaN(v model.Value) bool {
	switch val := v.(type) {
	case model.Matrix:
		if len(val) == 0 {
			return true
		}
		// Check if all values are NaN
		allNaN := true
		for _, ss := range val {
			for _, sp := range ss.Values {
				if !math.IsNaN(float64(sp.Value)) {
					allNaN = false
					break
				}
			}
			if !allNaN {
				break
			}
		}
		return allNaN
	case model.Vector:
		if len(val) == 0 {
			return true
		}
		// Check if all values are NaN
		for _, s := range val {
			if !math.IsNaN(float64(s.Value)) {
				return false
			}
		}
		return true
	}
	return false
}

// QueryPrometheusHistogram is a tool for querying histogram percentiles
var QueryPrometheusHistogram = mcpgrafana.MustTool(
	"query_prometheus_histogram",
	`Query Prometheus histogram percentiles. DISCOVER FIRST: Use list_prometheus_metric_names with regex='.*_bucket$' to find histograms.

Generates histogram_quantile PromQL. Example: metric='http_duration', percentile=95, labels='job="api"'

Time formats: 'now-1h', '2026-02-02T19:00:00Z', '1738519200000' (Unix ms)`,
	queryPrometheusHistogram,
	mcp.WithTitleAnnotation("Query Prometheus histogram percentile"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddPrometheusTools(mcp *server.MCPServer) {
	ListPrometheusMetricMetadata.Register(mcp)
	QueryPrometheus.Register(mcp)
	QueryPrometheusHistogram.Register(mcp)
	ListPrometheusMetricNames.Register(mcp)
	ListPrometheusLabelNames.Register(mcp)
	ListPrometheusLabelValues.Register(mcp)
}
