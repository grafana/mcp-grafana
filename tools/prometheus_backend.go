package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// nativePromTypes are datasource types that implement the Prometheus HTTP API
// at the /api/datasources/uid/{uid}/resources proxy.
var nativePromTypes = map[string]bool{
	"prometheus": true,
	"mimir":      true,
	"cortex":     true,
	"thanos":     true,
}

// backendPromQLTypes are datasource types that support PromQL via Grafana's
// /api/ds/query backend endpoint but do NOT implement the Prometheus HTTP API
// at the resource proxy.
var backendPromQLTypes = map[string]bool{
	"stackdriver": true,
}

// isNativePrometheusType returns true if the datasource type natively implements
// the Prometheus HTTP API at the resource proxy.
func isNativePrometheusType(dsType string) bool {
	return nativePromTypes[strings.ToLower(dsType)]
}

// supportsPromQLViaBackend returns true if the datasource type supports PromQL
// queries routed through Grafana's /api/ds/query endpoint.
func supportsPromQLViaBackend(dsType string) bool {
	return backendPromQLTypes[strings.ToLower(dsType)]
}

// backendPromClient implements promv1.API by routing PromQL queries through
// Grafana's /api/ds/query endpoint. This is used for datasources like
// Google Cloud Monitoring (stackdriver) that support PromQL but don't
// implement the standard Prometheus HTTP API at the resource proxy.
type backendPromClient struct {
	httpClient     *http.Client
	baseURL        string
	datasourceUID  string
	datasourceType string
}

// newBackendPromClient creates a backendPromClient for the given datasource.
func newBackendPromClient(ctx context.Context, uid, dsType string) (*backendPromClient, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/")

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

	return &backendPromClient{
		httpClient:     client,
		baseURL:        baseURL,
		datasourceUID:  uid,
		datasourceType: dsType,
	}, nil
}

// dsQueryResponse represents the raw /api/ds/query response structure.
type dsQueryResponse struct {
	Results map[string]dsQueryResult `json:"results"`
}

type dsQueryResult struct {
	Status int              `json:"status,omitempty"`
	Frames []dsQueryFrame   `json:"frames,omitempty"`
	Error  string           `json:"error,omitempty"`
}

type dsQueryFrame struct {
	Schema dsQueryFrameSchema `json:"schema"`
	Data   dsQueryFrameData   `json:"data"`
}

type dsQueryFrameSchema struct {
	Name   string              `json:"name,omitempty"`
	RefID  string              `json:"refId,omitempty"`
	Fields []dsQueryFieldSchema `json:"fields"`
}

type dsQueryFieldSchema struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Labels   map[string]string `json:"labels,omitempty"`
	TypeInfo struct {
		Frame string `json:"frame,omitempty"`
	} `json:"typeInfo,omitempty"`
}

type dsQueryFrameData struct {
	Values [][]interface{} `json:"values"`
}

// buildDSQueryPayload constructs the /api/ds/query request body.
func (c *backendPromClient) buildDSQueryPayload(expr string, start, end time.Time, step time.Duration) map[string]interface{} {
	query := map[string]interface{}{
		"datasource": map[string]string{
			"uid":  c.datasourceUID,
			"type": c.datasourceType,
		},
		"refId":         "A",
		"expr":          expr,
		"intervalMs":    int64(step / time.Millisecond),
		"maxDataPoints": 1000,
	}

	return map[string]interface{}{
		"queries": []map[string]interface{}{query},
		"from":    fmt.Sprintf("%d", start.UnixMilli()),
		"to":      fmt.Sprintf("%d", end.UnixMilli()),
	}
}

// executeDSQuery sends a request to /api/ds/query and returns the parsed response.
func (c *backendPromClient) executeDSQuery(ctx context.Context, payload map[string]interface{}) (*dsQueryResponse, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/ds/query", bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResult map[string]interface{}
		if json.Unmarshal(bodyBytes, &errResult) == nil {
			if errMsg, ok := errResult["message"].(string); ok {
				return nil, fmt.Errorf("query failed: %s", errMsg)
			}
		}
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result dsQueryResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &result, nil
}

// framesToMatrix converts /api/ds/query data frames into a model.Matrix.
// Each frame with a time column and one or more numeric value columns
// produces one SampleStream per value column.
func framesToMatrix(resp *dsQueryResponse) (model.Matrix, error) {
	var matrix model.Matrix

	for _, result := range resp.Results {
		if result.Error != "" {
			return nil, fmt.Errorf("query error: %s", result.Error)
		}

		for _, frame := range result.Frames {
			// Find time column index
			timeColIdx := -1
			for i, field := range frame.Schema.Fields {
				if field.Type == "time" {
					timeColIdx = i
					break
				}
			}
			if timeColIdx == -1 {
				continue
			}

			// Process each numeric column as a separate series
			for colIdx, field := range frame.Schema.Fields {
				if colIdx == timeColIdx {
					continue
				}
				if field.Type != "number" && field.TypeInfo.Frame != "float64" && field.TypeInfo.Frame != "int64" {
					continue
				}

				// Build metric labels
				metric := model.Metric{}
				if frame.Schema.Name != "" {
					metric[model.MetricNameLabel] = model.LabelValue(frame.Schema.Name)
				}
				if field.Name != "" && field.Name != "Value" {
					metric[model.MetricNameLabel] = model.LabelValue(field.Name)
				}
				for k, v := range field.Labels {
					metric[model.LabelName(k)] = model.LabelValue(v)
				}

				// Extract time-value pairs
				if timeColIdx >= len(frame.Data.Values) || colIdx >= len(frame.Data.Values) {
					continue
				}

				timeValues := frame.Data.Values[timeColIdx]
				metricValues := frame.Data.Values[colIdx]

				var pairs []model.SamplePair
				for i := 0; i < len(timeValues) && i < len(metricValues); i++ {
					ts, ok := toFloat64(timeValues[i])
					if !ok {
						continue
					}
					val, ok := toFloat64(metricValues[i])
					if !ok {
						continue
					}
					pairs = append(pairs, model.SamplePair{
						Timestamp: model.Time(ts),
						Value:     model.SampleValue(val),
					})
				}

				if len(pairs) > 0 {
					matrix = append(matrix, &model.SampleStream{
						Metric: metric,
						Values: pairs,
					})
				}
			}
		}
	}

	return matrix, nil
}

// framesToVector converts /api/ds/query data frames into a model.Vector
// by taking only the last sample from each series.
func framesToVector(resp *dsQueryResponse) (model.Vector, error) {
	matrix, err := framesToMatrix(resp)
	if err != nil {
		return nil, err
	}

	var vector model.Vector
	for _, stream := range matrix {
		if len(stream.Values) == 0 {
			continue
		}
		last := stream.Values[len(stream.Values)-1]
		vector = append(vector, &model.Sample{
			Metric:    stream.Metric,
			Timestamp: last.Timestamp,
			Value:     last.Value,
		})
	}
	return vector, nil
}

// toFloat64 converts a JSON-decoded numeric value to float64.
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case nil:
		return 0, false
	default:
		return 0, false
	}
}

// Query implements promv1.API by sending an instant query via /api/ds/query.
func (c *backendPromClient) Query(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
	// For instant queries, use a small window around the timestamp
	start := ts.Add(-5 * time.Minute)
	end := ts

	payload := c.buildDSQueryPayload(query, start, end, 15*time.Second)
	resp, err := c.executeDSQuery(ctx, payload)
	if err != nil {
		return nil, nil, err
	}

	vector, err := framesToVector(resp)
	if err != nil {
		return nil, nil, err
	}

	return vector, nil, nil
}

// QueryRange implements promv1.API by sending a range query via /api/ds/query.
func (c *backendPromClient) QueryRange(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
	step := r.Step
	if step == 0 {
		step = 15 * time.Second
	}

	payload := c.buildDSQueryPayload(query, r.Start, r.End, step)
	resp, err := c.executeDSQuery(ctx, payload)
	if err != nil {
		return nil, nil, err
	}

	matrix, err := framesToMatrix(resp)
	if err != nil {
		return nil, nil, err
	}

	return matrix, nil, nil
}

// The following methods are required by promv1.API but are not supported
// via the /api/ds/query backend for non-native datasources.

func (c *backendPromClient) LabelNames(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]string, promv1.Warnings, error) {
	return nil, nil, fmt.Errorf("listing label names is not supported for %s datasources via the backend query path; use the Grafana UI or native Prometheus datasources for metadata operations", c.datasourceType)
}

func (c *backendPromClient) LabelValues(ctx context.Context, label string, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelValues, promv1.Warnings, error) {
	return nil, nil, fmt.Errorf("listing label values is not supported for %s datasources via the backend query path; use the Grafana UI or native Prometheus datasources for metadata operations", c.datasourceType)
}

func (c *backendPromClient) Metadata(ctx context.Context, metric string, limit string) (map[string][]promv1.Metadata, error) {
	return nil, fmt.Errorf("listing metric metadata is not supported for %s datasources via the backend query path; use the Grafana UI or native Prometheus datasources for metadata operations", c.datasourceType)
}

// Unused promv1.API methods — return not-supported errors.

func (c *backendPromClient) Alerts(ctx context.Context) (promv1.AlertsResult, error) {
	return promv1.AlertsResult{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) AlertManagers(ctx context.Context) (promv1.AlertManagersResult, error) {
	return promv1.AlertManagersResult{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) CleanTombstones(ctx context.Context) error {
	return fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) Config(ctx context.Context) (promv1.ConfigResult, error) {
	return promv1.ConfigResult{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) DeleteSeries(ctx context.Context, matches []string, startTime time.Time, endTime time.Time) error {
	return fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) Flags(ctx context.Context) (promv1.FlagsResult, error) {
	return promv1.FlagsResult{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) Buildinfo(ctx context.Context) (promv1.BuildinfoResult, error) {
	return promv1.BuildinfoResult{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) Runtimeinfo(ctx context.Context) (promv1.RuntimeinfoResult, error) {
	return promv1.RuntimeinfoResult{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) Series(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]model.LabelSet, promv1.Warnings, error) {
	return nil, nil, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) Snapshot(ctx context.Context, skipHead bool) (promv1.SnapshotResult, error) {
	return promv1.SnapshotResult{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) Rules(ctx context.Context) (promv1.RulesResult, error) {
	return promv1.RulesResult{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) Targets(ctx context.Context) (promv1.TargetsResult, error) {
	return promv1.TargetsResult{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) TargetsMetadata(ctx context.Context, matchTarget string, metric string, limit string) ([]promv1.MetricMetadata, error) {
	return nil, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) TSDB(ctx context.Context, opts ...promv1.Option) (promv1.TSDBResult, error) {
	return promv1.TSDBResult{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) WalReplay(ctx context.Context) (promv1.WalReplayStatus, error) {
	return promv1.WalReplayStatus{}, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}

func (c *backendPromClient) QueryExemplars(ctx context.Context, query string, startTime time.Time, endTime time.Time) ([]promv1.ExemplarQueryResult, error) {
	return nil, fmt.Errorf("not supported for %s datasources", c.datasourceType)
}
