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
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

const cloudMonitoringDatasourceType = "stackdriver"

// cloudMonitoringBackend implements promBackend for Cloud Monitoring datasources.
// It uses /api/ds/query for PromQL queries and the plugin's resource endpoints
// for metric discovery/metadata.
type cloudMonitoringBackend struct {
	httpClient     *http.Client
	baseURL        string
	datasourceUID  string
	defaultProject string
}

func newCloudMonitoringBackend(ctx context.Context, ds *models.DataSource, projectOverride string) (*cloudMonitoringBackend, error) {
	defaultProject := extractCMDefaultProject(ds)
	if projectOverride != "" {
		defaultProject = projectOverride
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := trimTrailingSlash(cfg.URL)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
		Timeout:   30 * time.Second,
	}

	return &cloudMonitoringBackend{
		httpClient:     client,
		baseURL:        baseURL,
		datasourceUID:  ds.UID,
		defaultProject: defaultProject,
	}, nil
}

func extractCMDefaultProject(ds *models.DataSource) string {
	if ds.JSONData == nil {
		return ""
	}
	jsonDataMap, ok := ds.JSONData.(map[string]interface{})
	if !ok {
		return ""
	}
	proj, _ := jsonDataMap["defaultProject"].(string)
	return proj
}

// project returns the configured GCP project or an actionable error if neither
// defaultProject nor a per-call projectName override was supplied.
func (b *cloudMonitoringBackend) project() (string, error) {
	if b.defaultProject == "" {
		return "", fmt.Errorf("no GCP project configured: set defaultProject on the datasource or pass projectName in the tool call")
	}
	return b.defaultProject, nil
}

// Query executes a PromQL query via Grafana's /api/ds/query endpoint.
func (b *cloudMonitoringBackend) Query(ctx context.Context, expr string, queryType string, start, end time.Time, stepSeconds int) (model.Value, error) {
	project, err := b.project()
	if err != nil {
		return nil, err
	}
	step := fmt.Sprintf("%ds", stepSeconds)
	if stepSeconds == 0 {
		step = "60s"
	}

	// For instant queries, start or end may be zero — ensure the plugin
	// receives a valid time range (start <= end).
	if start.IsZero() {
		start = end
	}
	if end.IsZero() || end.Before(start) {
		end = start
	}

	query := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]string{
			"type": cloudMonitoringDatasourceType,
			"uid":  b.datasourceUID,
		},
		"queryType": "promQL",
		"promQLQuery": map[string]interface{}{
			"expr":        expr,
			"projectName": project,
			"step":        step,
		},
		// timeSeriesList is required by the Cloud Monitoring plugin even for
		// PromQL queries — without it the plugin returns a 500 error.
		"timeSeriesList": map[string]interface{}{
			"filters":     []interface{}{},
			"projectName": project,
			"view":        "FULL",
		},
	}

	payload := map[string]interface{}{
		"queries": []interface{}{query},
		"from":    strconv.FormatInt(start.UnixMilli(), 10),
		"to":      strconv.FormatInt(end.UnixMilli(), 10),
	}

	resp, err := b.doDSQuery(ctx, payload)
	if err != nil {
		return nil, err
	}

	return framesToPrometheusValue(resp, queryType)
}

// LabelNames returns label names by issuing a TIME_SERIES_LIST HEADERS query
// and collecting all label keys from the returned frame fields.
// The Grafana Cloud Monitoring plugin strips label descriptors from metric
// descriptors, so we must query actual time series to discover labels.
func (b *cloudMonitoringBackend) LabelNames(ctx context.Context, matchers []string, start, end time.Time) ([]string, error) {
	project, err := b.project()
	if err != nil {
		return nil, err
	}
	if start.IsZero() {
		start = time.Now().Add(-1 * time.Hour)
	}
	if end.IsZero() {
		end = time.Now()
	}

	var filters []interface{}
	nameFilter := extractNameMatcher(matchers)
	if nameFilter != "" {
		filters = []interface{}{"metric.type", "=", nameFilter}
	} else {
		filters = []interface{}{}
	}

	query := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]string{
			"type": cloudMonitoringDatasourceType,
			"uid":  b.datasourceUID,
		},
		"queryType": "timeSeriesList",
		"timeSeriesList": map[string]interface{}{
			"projectName":        project,
			"filters":            filters,
			"view":               "HEADERS",
			"crossSeriesReducer": "REDUCE_NONE",
		},
	}

	payload := map[string]interface{}{
		"queries": []interface{}{query},
		"from":    strconv.FormatInt(start.UnixMilli(), 10),
		"to":      strconv.FormatInt(end.UnixMilli(), 10),
	}

	resp, err := b.doDSQuery(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("querying label names: %w", err)
	}

	return extractLabelNamesFromFrames(resp), nil
}

// LabelValues returns values for a label. For __name__, it returns metric type names
// from metric descriptors. For other labels, it uses a TIME_SERIES_LIST HEADERS query.
func (b *cloudMonitoringBackend) LabelValues(ctx context.Context, labelName string, matchers []string, start, end time.Time) ([]string, error) {
	if labelName == "__name__" {
		return b.metricNames(ctx, matchers)
	}

	return b.labelValuesViaQuery(ctx, labelName, matchers, start, end)
}

// MetricMetadata returns metadata for metrics by fetching metric descriptors.
func (b *cloudMonitoringBackend) MetricMetadata(ctx context.Context, metric string, limit int) (map[string][]promv1.Metadata, error) {
	descriptors, err := b.fetchMetricDescriptors(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching metric descriptors: %w", err)
	}

	result := make(map[string][]promv1.Metadata)
	count := 0
	for _, desc := range descriptors {
		if metric != "" && desc.Type != metric {
			continue
		}
		if limit > 0 && count >= limit {
			break
		}
		result[desc.Type] = []promv1.Metadata{{
			Type: mapGCPMetricKind(desc.MetricKind, desc.ValueType),
			Help: desc.Description,
			Unit: desc.Unit,
		}}
		count++
	}
	return result, nil
}

// metricNames returns metric type names from metric descriptors.
func (b *cloudMonitoringBackend) metricNames(ctx context.Context, matchers []string) ([]string, error) {
	descriptors, err := b.fetchMetricDescriptors(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching metric descriptors: %w", err)
	}

	nameFilter := extractNameMatcher(matchers)
	var names []string
	for _, desc := range descriptors {
		if nameFilter != "" && !matchesSimpleFilter(desc.Type, nameFilter) {
			continue
		}
		names = append(names, desc.Type)
	}
	return names, nil
}

// labelValuesViaQuery uses a TIME_SERIES_LIST HEADERS query via /api/ds/query
// to discover label values, matching how the Grafana frontend does it.
func (b *cloudMonitoringBackend) labelValuesViaQuery(ctx context.Context, labelName string, matchers []string, start, end time.Time) ([]string, error) {
	project, err := b.project()
	if err != nil {
		return nil, err
	}
	if start.IsZero() {
		start = time.Now().Add(-1 * time.Hour)
	}
	if end.IsZero() {
		end = time.Now()
	}

	var filters []interface{}
	nameFilter := extractNameMatcher(matchers)
	if nameFilter != "" {
		filters = []interface{}{"metric.type", "=", nameFilter}
	} else {
		filters = []interface{}{}
	}

	query := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]string{
			"type": cloudMonitoringDatasourceType,
			"uid":  b.datasourceUID,
		},
		"queryType": "timeSeriesList",
		"timeSeriesList": map[string]interface{}{
			"projectName":        project,
			"filters":            filters,
			"view":               "HEADERS",
			"crossSeriesReducer": "REDUCE_NONE",
		},
	}

	payload := map[string]interface{}{
		"queries": []interface{}{query},
		"from":    strconv.FormatInt(start.UnixMilli(), 10),
		"to":      strconv.FormatInt(end.UnixMilli(), 10),
	}

	resp, err := b.doDSQuery(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("querying label values: %w", err)
	}

	return extractLabelValuesFromFrames(resp, labelName), nil
}

// doDSQuery executes a request against Grafana's /api/ds/query endpoint.
func (b *cloudMonitoringBackend) doDSQuery(ctx context.Context, payload map[string]interface{}) (*dsQueryResponse, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/api/ds/query", bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query returned status %d: %s", resp.StatusCode, string(body[:min(len(body), 1024)]))
	}

	var queryResp dsQueryResponse
	if err := json.Unmarshal(body, &queryResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	return &queryResp, nil
}

// fetchMetricDescriptors calls the Cloud Monitoring plugin's /metricDescriptors/ resource endpoint.
// The Grafana plugin handles GCP API pagination internally and returns all descriptors
// as a flat JSON array (not the raw GCP API wrapper).
func (b *cloudMonitoringBackend) fetchMetricDescriptors(ctx context.Context) ([]gcpMetricDescriptor, error) {
	project, err := b.project()
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/api/datasources/uid/%s/resources/metricDescriptors/v3/projects/%s/metricDescriptors",
		b.baseURL, b.datasourceUID, project)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching metric descriptors: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metric descriptors returned status %d: %s", resp.StatusCode, string(body[:min(len(body), 1024)]))
	}

	var descriptors []gcpMetricDescriptor
	if err := json.Unmarshal(body, &descriptors); err != nil {
		return nil, fmt.Errorf("unmarshaling metric descriptors: %w", err)
	}

	return descriptors, nil
}

// --- GCP types ---

// gcpMetricDescriptor represents a metric descriptor as returned by the
// Grafana Cloud Monitoring plugin's resource endpoint. Note: the plugin
// strips the labels field and adds service/serviceShortName fields.
type gcpMetricDescriptor struct {
	Type             string `json:"type"`
	MetricKind       string `json:"metricKind"`
	ValueType        string `json:"valueType"`
	Unit             string `json:"unit,omitempty"`
	Description      string `json:"description,omitempty"`
	DisplayName      string `json:"displayName,omitempty"`
	Service          string `json:"service,omitempty"`
	ServiceShortName string `json:"serviceShortName,omitempty"`
}

// --- /api/ds/query response types ---

type dsQueryResponse struct {
	Results map[string]dsQueryResult `json:"results"`
}

type dsQueryResult struct {
	Status int            `json:"status,omitempty"`
	Frames []dsQueryFrame `json:"frames,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type dsQueryFrame struct {
	Schema dsQueryFrameSchema `json:"schema"`
	Data   dsQueryFrameData   `json:"data"`
}

type dsQueryFrameSchema struct {
	Name   string              `json:"name,omitempty"`
	RefID  string              `json:"refId,omitempty"`
	Fields []dsQueryFrameField `json:"fields"`
}

type dsQueryFrameField struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Labels map[string]string `json:"labels,omitempty"`
}

type dsQueryFrameData struct {
	Values [][]interface{} `json:"values"`
}

// --- Frame conversion ---

// framesToPrometheusValue converts /api/ds/query response frames to Prometheus model values.
func framesToPrometheusValue(resp *dsQueryResponse, queryType string) (model.Value, error) {
	r, ok := resp.Results["A"]
	if !ok {
		if queryType == "instant" {
			return model.Vector{}, nil
		}
		return model.Matrix{}, nil
	}

	if r.Error != "" {
		return nil, fmt.Errorf("query error: %s", r.Error)
	}

	if queryType == "instant" {
		return framesToVector(r.Frames)
	}
	return framesToMatrix(r.Frames)
}

func framesToMatrix(frames []dsQueryFrame) (model.Matrix, error) {
	var matrix model.Matrix
	for _, frame := range frames {
		timeIdx, valueIdx := findTimeAndValueFields(frame.Schema.Fields)
		if timeIdx == -1 || valueIdx == -1 {
			continue
		}
		if len(frame.Data.Values) <= timeIdx || len(frame.Data.Values) <= valueIdx {
			continue
		}

		metric := buildMetricFromLabels(frame.Schema.Fields[valueIdx].Labels, frame.Schema.Name)
		timeValues := frame.Data.Values[timeIdx]
		metricValues := frame.Data.Values[valueIdx]

		ss := &model.SampleStream{
			Metric: metric,
			Values: make([]model.SamplePair, 0, len(timeValues)),
		}

		for i := 0; i < len(timeValues) && i < len(metricValues); i++ {
			ts, tsOk := toMillis(timeValues[i])
			if !tsOk {
				continue
			}
			val, valOk := toFloat(metricValues[i])
			if !valOk {
				continue
			}
			ss.Values = append(ss.Values, model.SamplePair{
				Timestamp: model.Time(ts),
				Value:     model.SampleValue(val),
			})
		}

		matrix = append(matrix, ss)
	}
	if matrix == nil {
		return model.Matrix{}, nil
	}
	return matrix, nil
}

func framesToVector(frames []dsQueryFrame) (model.Vector, error) {
	var vector model.Vector
	for _, frame := range frames {
		timeIdx, valueIdx := findTimeAndValueFields(frame.Schema.Fields)
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

		lastIdx := len(timeValues) - 1
		ts, tsOk := toMillis(timeValues[lastIdx])
		if !tsOk {
			continue
		}
		val, valOk := toFloat(metricValues[lastIdx])
		if !valOk {
			continue
		}

		metric := buildMetricFromLabels(frame.Schema.Fields[valueIdx].Labels, frame.Schema.Name)
		vector = append(vector, &model.Sample{
			Metric:    metric,
			Timestamp: model.Time(ts),
			Value:     model.SampleValue(val),
		})
	}
	if vector == nil {
		return model.Vector{}, nil
	}
	return vector, nil
}

func findTimeAndValueFields(fields []dsQueryFrameField) (timeIdx, valueIdx int) {
	timeIdx = -1
	valueIdx = -1
	for i, f := range fields {
		switch f.Type {
		case "time":
			timeIdx = i
		case "number", "float64", "int64":
			valueIdx = i
		}
	}
	return
}

func buildMetricFromLabels(labels map[string]string, name string) model.Metric {
	metric := make(model.Metric, len(labels)+1)
	if name != "" {
		metric["__name__"] = model.LabelValue(name)
	}
	for k, v := range labels {
		metric[model.LabelName(k)] = model.LabelValue(v)
	}
	return metric
}

func toMillis(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case nil:
		return 0, false
	default:
		return 0, false
	}
}

// extractLabelNamesFromFrames extracts unique label keys from HEADERS query frames.
func extractLabelNamesFromFrames(resp *dsQueryResponse) []string {
	seen := make(map[string]bool)
	r, ok := resp.Results["A"]
	if !ok {
		return nil
	}

	for _, frame := range r.Frames {
		for _, field := range frame.Schema.Fields {
			for k := range field.Labels {
				if !seen[k] {
					seen[k] = true
				}
			}
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}

// extractLabelValuesFromFrames extracts unique values for a label from HEADERS query frames.
func extractLabelValuesFromFrames(resp *dsQueryResponse, labelName string) []string {
	seen := make(map[string]bool)
	r, ok := resp.Results["A"]
	if !ok {
		return nil
	}

	for _, frame := range r.Frames {
		for _, field := range frame.Schema.Fields {
			if v, ok := field.Labels[labelName]; ok && !seen[v] {
				seen[v] = true
			}
		}
	}

	values := make([]string, 0, len(seen))
	for v := range seen {
		values = append(values, v)
	}
	return values
}

// --- Helpers ---

// extractNameMatcher looks for a __name__="..." exact equality matcher in the matchers list.
// Regex matchers (__name__=~"...") are skipped since they cannot be used as
// literal metric type filters in Cloud Monitoring queries.
func extractNameMatcher(matchers []string) string {
	for _, m := range matchers {
		m = strings.TrimSpace(m)
		m = strings.Trim(m, "{}")
		for _, part := range strings.Split(m, ",") {
			part = strings.TrimSpace(part)
			// Skip regex and negative matchers — only exact equality is usable.
			if strings.HasPrefix(part, "__name__=~") || strings.HasPrefix(part, "__name__!") {
				continue
			}
			if strings.HasPrefix(part, "__name__=") {
				val := strings.TrimPrefix(part, "__name__=")
				val = strings.Trim(val, `"'`)
				return val
			}
		}
	}
	return ""
}

// matchesSimpleFilter checks if a metric name matches a simple filter string.
func matchesSimpleFilter(name, filter string) bool {
	if filter == "" {
		return true
	}
	if strings.HasPrefix(filter, "*") && strings.HasSuffix(filter, "*") {
		return strings.Contains(name, strings.Trim(filter, "*"))
	}
	if strings.HasSuffix(filter, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(filter, "*"))
	}
	if strings.HasPrefix(filter, "*") {
		return strings.HasSuffix(name, strings.TrimPrefix(filter, "*"))
	}
	return name == filter
}

// mapGCPMetricKind maps GCP MetricKind + ValueType to a Prometheus metric type.
func mapGCPMetricKind(metricKind, valueType string) promv1.MetricType {
	switch {
	case valueType == "DISTRIBUTION":
		return promv1.MetricTypeHistogram
	case metricKind == "CUMULATIVE":
		return promv1.MetricTypeCounter
	case metricKind == "GAUGE":
		return promv1.MetricTypeGauge
	default:
		return promv1.MetricTypeUnknown
	}
}
