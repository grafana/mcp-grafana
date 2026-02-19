package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/common/model"
)

const (
	CloudMonitoringDatasourceType = "stackdriver"
)

// QueryCloudMonitoringPromQLParams defines the parameters for querying Cloud Monitoring with PromQL.
type QueryCloudMonitoringPromQLParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Cloud Monitoring datasource to query. Use list_datasources to find available UIDs."`
	Expr          string `json:"expr" jsonschema:"required,description=The PromQL expression to query"`
	StartTime     string `json:"startTime" jsonschema:"required,description=The start time. Supported formats are RFC3339 or relative to now (e.g. 'now'\\, 'now-1.5h'\\, 'now-2h45m'). Valid time units are 'ns'\\, 'us' (or 'µs')\\, 'ms'\\, 's'\\, 'm'\\, 'h'\\, 'd'."`
	EndTime       string `json:"endTime,omitempty" jsonschema:"description=The end time. Required if queryType is 'range'\\, ignored if queryType is 'instant'. Supported formats are RFC3339 or relative to now (e.g. 'now'\\, 'now-1.5h'\\, 'now-2h45m'). Valid time units are 'ns'\\, 'us' (or 'µs')\\, 'ms'\\, 's'\\, 'm'\\, 'h'\\, 'd'."`
	StepSeconds   int    `json:"stepSeconds,omitempty" jsonschema:"description=The time series step size in seconds. Required if queryType is 'range'\\, ignored if queryType is 'instant'"`
	QueryType     string `json:"queryType,omitempty" jsonschema:"description=The type of query to use. Either 'range' or 'instant'"`
}

// QueryCloudMonitoringPromQLResult wraps the query result.
type QueryCloudMonitoringPromQLResult struct {
	Data  model.Value `json:"data"`
	Hints []string    `json:"hints,omitempty"`
}

type cloudMonitoringClient struct {
	httpClient     *http.Client
	baseURL        string
	datasourceUID  string
	defaultProject string
}

// cloudMonitoringQueryResponse represents the raw API response from Grafana's /api/ds/query
type cloudMonitoringQueryResponse struct {
	Results map[string]struct {
		Status int                    `json:"status,omitempty"`
		Frames []cloudMonitoringFrame `json:"frames,omitempty"`
		Error  string                 `json:"error,omitempty"`
	} `json:"results"`
}

type cloudMonitoringFrame struct {
	Schema cloudMonitoringFrameSchema `json:"schema"`
	Data   cloudMonitoringFrameData   `json:"data"`
}

type cloudMonitoringFrameSchema struct {
	Name   string                      `json:"name,omitempty"`
	RefID  string                      `json:"refId,omitempty"`
	Fields []cloudMonitoringFrameField `json:"fields"`
}

type cloudMonitoringFrameField struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Labels map[string]string `json:"labels,omitempty"`
}

type cloudMonitoringFrameData struct {
	Values [][]interface{} `json:"values"`
}

func newCloudMonitoringClient(ctx context.Context, ds *models.DataSource) (*cloudMonitoringClient, error) {
	defaultProject, err := extractDefaultProject(ds)
	if err != nil {
		return nil, err
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/")

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
	}

	return &cloudMonitoringClient{
		httpClient:     client,
		baseURL:        baseURL,
		datasourceUID:  ds.UID,
		defaultProject: defaultProject,
	}, nil
}

func extractDefaultProject(ds *models.DataSource) (string, error) {
	if ds.JSONData == nil {
		return "", fmt.Errorf("Cloud Monitoring datasource %s has no jsonData configured", ds.UID)
	}
	jsonDataMap, ok := ds.JSONData.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("Cloud Monitoring datasource %s has unexpected jsonData format", ds.UID)
	}
	proj, ok := jsonDataMap["defaultProject"].(string)
	if !ok || proj == "" {
		return "", fmt.Errorf("Cloud Monitoring datasource %s has no defaultProject configured in jsonData", ds.UID)
	}
	return proj, nil
}

func (c *cloudMonitoringClient) queryPromQL(ctx context.Context, expr, queryType string, start, end time.Time, stepSeconds int) (model.Value, error) {
	step := fmt.Sprintf("%ds", stepSeconds)
	if stepSeconds == 0 {
		step = "60s"
	}

	query := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]string{
			"type": CloudMonitoringDatasourceType,
			"uid":  c.datasourceUID,
		},
		"queryType": "promQL",
		"promQLQuery": map[string]interface{}{
			"expr":        expr,
			"projectName": c.defaultProject,
			"step":        step,
		},
		// timeSeriesList is required by the Cloud Monitoring plugin even for
		// PromQL queries — without it the plugin returns a 500 error.
		// This appears to be a plugin limitation rather than intentional API design.
		"timeSeriesList": map[string]interface{}{
			"filters":     []interface{}{},
			"projectName": c.defaultProject,
			"view":        "FULL",
		},
	}

	payload := map[string]interface{}{
		"queries": []interface{}{query},
		"from":    strconv.FormatInt(start.UnixMilli(), 10),
		"to":      strconv.FormatInt(end.UnixMilli(), 10),
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
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // 1MB limit
		return nil, fmt.Errorf("Cloud Monitoring query returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	body := io.LimitReader(resp.Body, 10*1024*1024) // 10MB limit
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var queryResp cloudMonitoringQueryResponse
	if err := json.Unmarshal(bodyBytes, &queryResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	return framesToPrometheusValue(&queryResp, queryType)
}

func framesToPrometheusValue(resp *cloudMonitoringQueryResponse, queryType string) (model.Value, error) {
	// We only send a single query with refId "A", so we process the first result only.
	for refID, r := range resp.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("query error (refId=%s): %s", refID, r.Error)
		}

		if queryType == "instant" {
			return framesToVector(r.Frames)
		}
		return framesToMatrix(r.Frames)
	}

	if queryType == "instant" {
		return model.Vector{}, nil
	}
	return model.Matrix{}, nil
}

func framesToMatrix(frames []cloudMonitoringFrame) (model.Matrix, error) {
	var matrix model.Matrix
	for _, frame := range frames {
		timeIdx, valueIdx := findTimeAndValueColumns(frame.Schema.Fields)
		if timeIdx == -1 || valueIdx == -1 {
			continue
		}
		if len(frame.Data.Values) <= timeIdx || len(frame.Data.Values) <= valueIdx {
			continue
		}

		metric := buildMetric(frame.Schema.Fields[valueIdx].Labels, frame.Schema.Name)

		timeValues := frame.Data.Values[timeIdx]
		metricValues := frame.Data.Values[valueIdx]

		ss := &model.SampleStream{
			Metric: metric,
			Values: make([]model.SamplePair, 0, len(timeValues)),
		}

		for i := 0; i < len(timeValues) && i < len(metricValues); i++ {
			ts, ok := toMilliseconds(timeValues[i])
			if !ok {
				continue
			}
			val, ok := toFloat64(metricValues[i])
			if !ok {
				continue
			}
			ss.Values = append(ss.Values, model.SamplePair{
				Timestamp: model.Time(ts),
				Value:     model.SampleValue(val),
			})
		}

		matrix = append(matrix, ss)
	}
	return matrix, nil
}

func framesToVector(frames []cloudMonitoringFrame) (model.Vector, error) {
	var vector model.Vector
	for _, frame := range frames {
		timeIdx, valueIdx := findTimeAndValueColumns(frame.Schema.Fields)
		if timeIdx == -1 || valueIdx == -1 {
			continue
		}
		if len(frame.Data.Values) <= timeIdx || len(frame.Data.Values) <= valueIdx {
			continue
		}

		timeValues := frame.Data.Values[timeIdx]
		metricValues := frame.Data.Values[valueIdx]
		if len(timeValues) == 0 || len(metricValues) == 0 {
			continue
		}

		metric := buildMetric(frame.Schema.Fields[valueIdx].Labels, frame.Schema.Name)

		lastIdx := len(timeValues) - 1
		ts, ok := toMilliseconds(timeValues[lastIdx])
		if !ok {
			continue
		}
		val, ok := toFloat64(metricValues[lastIdx])
		if !ok {
			continue
		}

		vector = append(vector, &model.Sample{
			Metric:    metric,
			Timestamp: model.Time(ts),
			Value:     model.SampleValue(val),
		})
	}
	return vector, nil
}

func buildMetric(labels map[string]string, schemaName string) model.Metric {
	metric := model.Metric{}
	for k, v := range labels {
		metric[model.LabelName(k)] = model.LabelValue(v)
	}
	if schemaName != "" {
		metric[model.MetricNameLabel] = model.LabelValue(schemaName)
	}
	return metric
}

// findTimeAndValueColumns returns the indices of the first time and last number
// columns. Cloud Monitoring PromQL responses use one time + one value column per
// frame, so picking the last number column is safe for the expected format.
func findTimeAndValueColumns(fields []cloudMonitoringFrameField) (int, int) {
	timeIdx, valueIdx := -1, -1
	for i, field := range fields {
		switch field.Type {
		case "time":
			timeIdx = i
		case "number":
			valueIdx = i
		}
	}
	return timeIdx, valueIdx
}

func toMilliseconds(v interface{}) (int64, bool) {
	switch val := v.(type) {
	case float64:
		return int64(val), true
	case int64:
		return val, true
	default:
		return 0, false
	}
}

func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int64:
		return float64(val), true
	case nil:
		return 0, false
	default:
		return 0, false
	}
}

// queryCloudMonitoringPromQL is the MCP tool handler for Cloud Monitoring PromQL queries.
func queryCloudMonitoringPromQL(ctx context.Context, args QueryCloudMonitoringPromQLParams) (*QueryCloudMonitoringPromQLResult, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
	if err != nil {
		return nil, err
	}
	if ds.Type != CloudMonitoringDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", args.DatasourceUID, ds.Type, CloudMonitoringDatasourceType)
	}

	client, err := newCloudMonitoringClient(ctx, ds)
	if err != nil {
		return nil, fmt.Errorf("creating Cloud Monitoring client: %w", err)
	}

	queryType := args.QueryType
	if queryType == "" {
		queryType = "range"
	}

	startTime, err := parseStartTime(args.StartTime)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}

	var endTime time.Time
	switch queryType {
	case "range":
		if args.StepSeconds == 0 {
			return nil, fmt.Errorf("stepSeconds must be provided when queryType is 'range'")
		}
		endTime, err = parseEndTime(args.EndTime)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
	case "instant":
		endTime = startTime
	default:
		return nil, fmt.Errorf("invalid query type: %s", queryType)
	}

	result, err := client.queryPromQL(ctx, args.Expr, queryType, startTime, endTime, args.StepSeconds)
	if err != nil {
		return nil, err
	}

	response := &QueryCloudMonitoringPromQLResult{
		Data: result,
	}

	if isCloudMonitoringResultEmpty(result) {
		response.Hints = generateCloudMonitoringEmptyResultHints()
	}

	return response, nil
}

func generateCloudMonitoringEmptyResultHints() []string {
	return []string{
		"No data found. Possible reasons:",
		"- The PromQL expression may reference metrics that don't exist in this GCP project",
		"- The time range may have no data — try extending with startTime",
		"- The GCP project configured on this datasource may not have Cloud Monitoring enabled",
		"- Label matchers may not match any time series",
	}
}

func isCloudMonitoringResultEmpty(result model.Value) bool {
	if result == nil {
		return true
	}
	switch v := result.(type) {
	case model.Vector:
		return len(v) == 0
	case model.Matrix:
		return len(v) == 0
	default:
		return false
	}
}

var QueryCloudMonitoringPromQL = mcpgrafana.MustTool(
	"query_cloud_monitoring_promql",
	`Query Google Cloud Monitoring using PromQL expressions via Grafana. Supports instant and range queries.

The datasource must be a Cloud Monitoring (stackdriver) type. The GCP project is read automatically from the datasource config.

Time formats: 'now-1h', '2026-02-02T19:00:00Z', 'now'`,
	queryCloudMonitoringPromQL,
	mcp.WithTitleAnnotation("Query Cloud Monitoring with PromQL"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddCloudMonitoringTools registers all Cloud Monitoring tools with the MCP server.
func AddCloudMonitoringTools(mcp *server.MCPServer) {
	QueryCloudMonitoringPromQL.Register(mcp)
}
