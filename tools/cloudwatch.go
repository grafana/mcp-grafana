package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	// DefaultCloudWatchPeriod is the default period in seconds for CloudWatch metrics
	DefaultCloudWatchPeriod = 300

	// CloudWatchDatasourceType is the type identifier for CloudWatch datasources
	CloudWatchDatasourceType = "cloudwatch"
)

// CloudWatchQueryParams defines the parameters for querying CloudWatch
type CloudWatchQueryParams struct {
	DatasourceUID string            `json:"datasourceUid" jsonschema:"required,description=The UID of the CloudWatch datasource to query. Use list_datasources to find available UIDs."`
	Namespace     string            `json:"namespace" jsonschema:"required,description=CloudWatch namespace (e.g. AWS/ECS\\, AWS/EC2\\, AWS/RDS\\, AWS/Lambda)"`
	MetricName    string            `json:"metricName" jsonschema:"required,description=Metric name (e.g. CPUUtilization\\, MemoryUtilization\\, Invocations)"`
	Dimensions    map[string]string `json:"dimensions,omitempty" jsonschema:"description=Dimensions as key-value pairs (e.g. {\"ClusterName\": \"my-cluster\"})"`
	Statistic     string            `json:"statistic,omitempty" jsonschema:"enum=Average,enum=Sum,enum=Maximum,enum=Minimum,enum=SampleCount,description=Statistic type: Average\\, Sum\\, Maximum\\, Minimum\\, SampleCount. Default: Average"`
	Period        int               `json:"period,omitempty" jsonschema:"description=Period in seconds (default: 300)"`
	Start         string            `json:"start,omitempty" jsonschema:"description=Start time. Formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: now-1h"`
	End           string            `json:"end,omitempty" jsonschema:"description=End time. Formats: 'now'\\, '2026-02-02T20:00:00Z'\\, '1738522800000' (Unix ms). Default: now"`
	Region        string            `json:"region" jsonschema:"required,description=AWS region (e.g. us-east-1)"`
}

// CloudWatchQueryResult represents the result of a CloudWatch query
type CloudWatchQueryResult struct {
	Label      string             `json:"label"`
	Timestamps []int64            `json:"timestamps"`
	Values     []float64          `json:"values"`
	Statistics map[string]float64 `json:"statistics,omitempty"`
	Hints      []string           `json:"hints,omitempty"`
}

// cloudWatchQueryResponse represents the raw API response from Grafana's /api/ds/query
type cloudWatchQueryResponse struct {
	Results map[string]struct {
		Status int `json:"status,omitempty"`
		Frames []struct {
			Schema struct {
				Name   string `json:"name,omitempty"`
				RefID  string `json:"refId,omitempty"`
				Fields []struct {
					Name     string                 `json:"name"`
					Type     string                 `json:"type"`
					Labels   map[string]string      `json:"labels,omitempty"`
					Config   map[string]interface{} `json:"config,omitempty"`
					TypeInfo struct {
						Frame string `json:"frame,omitempty"`
					} `json:"typeInfo,omitempty"`
				} `json:"fields"`
			} `json:"schema"`
			Data struct {
				Values [][]interface{} `json:"values"`
			} `json:"data"`
		} `json:"frames,omitempty"`
		Error string `json:"error,omitempty"`
	} `json:"results"`
}

// cloudWatchClient handles communication with Grafana's CloudWatch datasource
type cloudWatchClient struct {
	httpClient *http.Client
	baseURL    string
}

// newCloudWatchClient creates a new CloudWatch client for the given datasource
func newCloudWatchClient(ctx context.Context, uid string) (*cloudWatchClient, error) {
	// Verify the datasource exists and is a CloudWatch datasource
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	if ds.Type != CloudWatchDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, CloudWatchDatasourceType)
	}

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

	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
	}

	return &cloudWatchClient{
		httpClient: client,
		baseURL:    baseURL,
	}, nil
}

// query executes a CloudWatch query via Grafana's /api/ds/query endpoint
func (c *cloudWatchClient) query(ctx context.Context, args CloudWatchQueryParams, from, to time.Time) (*cloudWatchQueryResponse, error) {
	// Format dimensions for CloudWatch query
	// CloudWatch expects dimensions as map[string][]string
	dimensions := make(map[string][]string)
	for k, v := range args.Dimensions {
		dimensions[k] = []string{v}
	}

	// Set defaults
	statistic := args.Statistic
	if statistic == "" {
		statistic = "Average"
	}

	period := args.Period
	if period <= 0 {
		period = DefaultCloudWatchPeriod
	}

	region := args.Region
	if region == "" {
		region = "default"
	}

	// Build the query payload
	payload := map[string]interface{}{
		"queries": []map[string]interface{}{
			{
				"datasource": map[string]string{
					"uid":  args.DatasourceUID,
					"type": CloudWatchDatasourceType,
				},
				"refId":      "A",
				"type":       "timeSeriesQuery",
				"namespace":  args.Namespace,
				"metricName": args.MetricName,
				"dimensions": dimensions,
				"statistic":  statistic,
				"period":     strconv.Itoa(period),
				"region":     region,
				"matchExact": true,
			},
		},
		"from": strconv.FormatInt(from.UnixMilli(), 10),
		"to":   strconv.FormatInt(to.UnixMilli(), 10),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	url := c.baseURL + "/api/ds/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CloudWatch query returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read and parse response
	body := io.LimitReader(resp.Body, 1024*1024*10) // 10MB limit
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var queryResp cloudWatchQueryResponse
	if err := json.Unmarshal(bodyBytes, &queryResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	return &queryResp, nil
}

// queryCloudWatch executes a CloudWatch query via Grafana
func queryCloudWatch(ctx context.Context, args CloudWatchQueryParams) (*CloudWatchQueryResult, error) {
	client, err := newCloudWatchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating CloudWatch client: %w", err)
	}

	// Parse time range
	now := time.Now()
	fromTime := now.Add(-1 * time.Hour) // Default: 1 hour ago
	toTime := now                       // Default: now

	if args.Start != "" {
		parsed, err := parseStartTime(args.Start)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			fromTime = parsed
		}
	}

	if args.End != "" {
		parsed, err := parseEndTime(args.End)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			toTime = parsed
		}
	}

	// Execute query
	resp, err := client.query(ctx, args, fromTime, toTime)
	if err != nil {
		return nil, err
	}

	// Process response
	result := &CloudWatchQueryResult{
		Label:      fmt.Sprintf("%s - %s", args.Namespace, args.MetricName),
		Timestamps: []int64{},
		Values:     []float64{},
		Statistics: make(map[string]float64),
	}

	// Check for errors in the response
	for refID, r := range resp.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("query error (refId=%s): %s", refID, r.Error)
		}

		// Process frames - accumulate statistics across all frames
		var sum, min, max float64
		var count int64
		first := true

		for _, frame := range r.Frames {
			// Find time and value columns
			var timeColIdx, valueColIdx = -1, -1
			for i, field := range frame.Schema.Fields {
				switch field.Type {
				case "time":
					timeColIdx = i
				case "number":
					valueColIdx = i
					// Update label if available from field config
					if field.Config != nil {
						if displayName, ok := field.Config["displayNameFromDS"].(string); ok && displayName != "" {
							result.Label = displayName
						}
					}
				}
			}

			if timeColIdx == -1 || valueColIdx == -1 {
				continue
			}

			// Extract data
			if len(frame.Data.Values) > timeColIdx && len(frame.Data.Values) > valueColIdx {
				timeValues := frame.Data.Values[timeColIdx]
				metricValues := frame.Data.Values[valueColIdx]

				for i := 0; i < len(timeValues) && i < len(metricValues); i++ {
					// Parse timestamp (can be float64 or int64 from JSON)
					var ts int64
					switch v := timeValues[i].(type) {
					case float64:
						ts = int64(v)
					case int64:
						ts = v
					default:
						continue
					}

					// Parse value
					var val float64
					switch v := metricValues[i].(type) {
					case float64:
						val = v
					case int64:
						val = float64(v)
					case nil:
						continue
					default:
						continue
					}

					result.Timestamps = append(result.Timestamps, ts)
					result.Values = append(result.Values, val)

					// Calculate statistics
					sum += val
					count++
					if first {
						min = val
						max = val
						first = false
					} else {
						if val < min {
							min = val
						}
						if val > max {
							max = val
						}
					}
				}
			}
		}

		// Add computed statistics across all frames
		if count > 0 {
			result.Statistics["sum"] = sum
			result.Statistics["min"] = min
			result.Statistics["max"] = max
			result.Statistics["avg"] = sum / float64(count)
			result.Statistics["count"] = float64(count)
		}
	}

	// Add hints if no data was found
	if len(result.Values) == 0 {
		result.Hints = generateCloudWatchEmptyResultHints()
	}

	return result, nil
}

// generateCloudWatchEmptyResultHints generates helpful hints when a CloudWatch query returns no data
func generateCloudWatchEmptyResultHints() []string {
	return []string{
		"No data found. Possible reasons:",
		"- Namespace may not exist - use list_cloudwatch_namespaces to discover available namespaces",
		"- Metric name may be incorrect - use list_cloudwatch_metrics to find valid metrics",
		"- Dimensions may not match - use list_cloudwatch_dimensions to check valid dimension keys",
		"- Region may be incorrect - check if metrics exist in the specified region",
		"- Time range may have no data - try extending with start=\"now-6h\"",
	}
}

// QueryCloudWatch is a tool for querying CloudWatch datasources via Grafana
var QueryCloudWatch = mcpgrafana.MustTool(
	"query_cloudwatch",
	`Query AWS CloudWatch metrics via Grafana. Requires region.

REQUIRED FIRST: Use list_cloudwatch_namespaces -> list_cloudwatch_metrics -> list_cloudwatch_dimensions -> then query.

Time formats: 'now-1h', '2026-02-02T19:00:00Z', '1738519200000' (Unix ms)

Common namespaces: AWS/EC2, AWS/ECS, AWS/RDS, AWS/Lambda, ECS/ContainerInsights

Example dimensions: ECS: {ClusterName, ServiceName}, EC2: {InstanceId}`,
	queryCloudWatch,
	mcp.WithTitleAnnotation("Query CloudWatch"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListCloudWatchNamespacesParams defines the parameters for listing CloudWatch namespaces
type ListCloudWatchNamespacesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the CloudWatch datasource"`
	Region        string `json:"region" jsonschema:"required,description=AWS region (e.g. us-east-1)"`
}

// cloudWatchResourceItem represents an item returned by CloudWatch resource APIs
// The Grafana CloudWatch API returns arrays of objects with text and value fields
type cloudWatchResourceItem struct {
	Text  string `json:"text"`
	Value string `json:"value"`
}

// cloudWatchMetricItem represents an item returned by CloudWatch metrics API
// The metrics API returns a different format: [{value: {name: "...", namespace: "..."}}]
type cloudWatchMetricItem struct {
	Value struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"value"`
}

// parseCloudWatchResourceResponse extracts values from CloudWatch resource API response
func parseCloudWatchResourceResponse(bodyBytes []byte) ([]string, error) {
	var items []cloudWatchResourceItem
	if err := json.Unmarshal(bodyBytes, &items); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	result := make([]string, len(items))
	for i, item := range items {
		result[i] = item.Value
	}
	return result, nil
}

// parseCloudWatchMetricsResponse extracts metric names from CloudWatch metrics API response
func parseCloudWatchMetricsResponse(bodyBytes []byte) ([]string, error) {
	var items []cloudWatchMetricItem
	if err := json.Unmarshal(bodyBytes, &items); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	result := make([]string, len(items))
	for i, item := range items {
		result[i] = item.Value.Name
	}
	return result, nil
}

// listCloudWatchNamespaces lists available CloudWatch namespaces
func listCloudWatchNamespaces(ctx context.Context, args ListCloudWatchNamespacesParams) ([]string, error) {
	client, err := newCloudWatchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating CloudWatch client: %w", err)
	}

	// Build query parameters
	params := url.Values{}
	if args.Region != "" {
		params.Set("region", args.Region)
	}

	resourceURL := client.baseURL + "/api/datasources/uid/" + args.DatasourceUID + "/resources/namespaces"
	if len(params) > 0 {
		resourceURL += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CloudWatch namespaces returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	body := io.LimitReader(resp.Body, 1024*1024) // 1MB limit
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return parseCloudWatchResourceResponse(bodyBytes)
}

// ListCloudWatchNamespaces is a tool for listing CloudWatch namespaces
var ListCloudWatchNamespaces = mcpgrafana.MustTool(
	"list_cloudwatch_namespaces",
	"START HERE for CloudWatch: List available namespaces (AWS/EC2, AWS/ECS, AWS/RDS, etc.). Requires region. NEXT: Use list_cloudwatch_metrics with a namespace.",
	listCloudWatchNamespaces,
	mcp.WithTitleAnnotation("List CloudWatch namespaces"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListCloudWatchMetricsParams defines the parameters for listing CloudWatch metrics
type ListCloudWatchMetricsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the CloudWatch datasource"`
	Namespace     string `json:"namespace" jsonschema:"required,description=CloudWatch namespace (e.g. AWS/ECS\\, AWS/EC2)"`
	Region        string `json:"region" jsonschema:"required,description=AWS region (e.g. us-east-1)"`
}

// listCloudWatchMetrics lists available metrics for a CloudWatch namespace
func listCloudWatchMetrics(ctx context.Context, args ListCloudWatchMetricsParams) ([]string, error) {
	client, err := newCloudWatchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating CloudWatch client: %w", err)
	}

	// Build query parameters
	params := url.Values{}
	params.Set("namespace", args.Namespace)
	if args.Region != "" {
		params.Set("region", args.Region)
	}

	resourceURL := client.baseURL + "/api/datasources/uid/" + args.DatasourceUID + "/resources/metrics?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CloudWatch metrics returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	body := io.LimitReader(resp.Body, 1024*1024) // 1MB limit
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return parseCloudWatchMetricsResponse(bodyBytes)
}

// ListCloudWatchMetrics is a tool for listing CloudWatch metrics
var ListCloudWatchMetrics = mcpgrafana.MustTool(
	"list_cloudwatch_metrics",
	"List metrics for a CloudWatch namespace. Requires region. Use after list_cloudwatch_namespaces. NEXT: Use list_cloudwatch_dimensions\\, then query_cloudwatch.",
	listCloudWatchMetrics,
	mcp.WithTitleAnnotation("List CloudWatch metrics"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListCloudWatchDimensionsParams defines the parameters for listing CloudWatch dimensions
type ListCloudWatchDimensionsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the CloudWatch datasource"`
	Namespace     string `json:"namespace" jsonschema:"required,description=CloudWatch namespace (e.g. AWS/ECS)"`
	MetricName    string `json:"metricName" jsonschema:"required,description=Metric name (e.g. CPUUtilization)"`
	Region        string `json:"region" jsonschema:"required,description=AWS region (e.g. us-east-1)"`
}

// listCloudWatchDimensions lists available dimension keys for a CloudWatch metric
func listCloudWatchDimensions(ctx context.Context, args ListCloudWatchDimensionsParams) ([]string, error) {
	client, err := newCloudWatchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating CloudWatch client: %w", err)
	}

	// Build query parameters
	params := url.Values{}
	params.Set("namespace", args.Namespace)
	params.Set("metricName", args.MetricName)
	if args.Region != "" {
		params.Set("region", args.Region)
	}

	resourceURL := client.baseURL + "/api/datasources/uid/" + args.DatasourceUID + "/resources/dimension-keys?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CloudWatch dimensions returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	body := io.LimitReader(resp.Body, 1024*1024) // 1MB limit
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return parseCloudWatchResourceResponse(bodyBytes)
}

// ListCloudWatchDimensions is a tool for listing CloudWatch dimension keys
var ListCloudWatchDimensions = mcpgrafana.MustTool(
	"list_cloudwatch_dimensions",
	"List dimension keys for a CloudWatch metric. Requires region. Use after list_cloudwatch_metrics. NEXT: Use query_cloudwatch with discovered dimensions.",
	listCloudWatchDimensions,
	mcp.WithTitleAnnotation("List CloudWatch dimensions"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddCloudWatchTools registers all CloudWatch tools with the MCP server
func AddCloudWatchTools(adder mcpgrafana.ToolAdder) {
	QueryCloudWatch.Register(adder)
	ListCloudWatchNamespaces.Register(adder)
	ListCloudWatchMetrics.Register(adder)
	ListCloudWatchDimensions.Register(adder)
}
