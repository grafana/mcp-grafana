package tools

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

const (
	defaultServiceHealthTimeRange = "1h"
	maxServiceIdentityLength      = 256
	maxServiceHealthPoints        = 121
	maxServiceDependencies        = 20
	maxServiceDependencyQuery     = maxServiceDependencies + 1
)

var serviceHealthDurations = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// ServiceHealthParams identifies an App O11y service and a bounded lookback.
type ServiceHealthParams struct {
	DatasourceUID    string  `json:"datasource_uid" jsonschema:"required,description=UID of the Prometheus datasource containing Application Observability metrics."`
	ServiceName      string  `json:"service_name" jsonschema:"required,description=Exact service label value in Application Observability span metrics."`
	ServiceNamespace *string `json:"service_namespace,omitempty" jsonschema:"description=Optional exact service_namespace label value used to disambiguate the service."`
	TimeRange        string  `json:"time_range,omitempty" jsonschema:"enum=1h,enum=6h,enum=24h,enum=7d,description=Lookback window. Defaults to 1h."`
}

// ServiceHealthResult is the structured content consumed by the service health MCP App.
type ServiceHealthResult struct {
	ServiceName      string               `json:"serviceName"`
	ServiceNamespace *string              `json:"serviceNamespace"`
	DatasourceUID    string               `json:"datasourceUid"`
	TimeRange        string               `json:"timeRange"`
	FromMS           int64                `json:"fromMs"`
	ToMS             int64                `json:"toMs"`
	Status           string               `json:"status"`
	Summary          string               `json:"summary"`
	Description      string               `json:"description"`
	Metrics          ServiceHealthMetrics `json:"metrics"`
	Dependencies     []ServiceDependency  `json:"dependencies"`
	GrafanaURL       *string              `json:"grafanaUrl"`
	DashboardURL     *string              `json:"dashboardUrl"`
	SLOURL           *string              `json:"sloUrl"`
	Warnings         []string             `json:"warnings"`
}

type ServiceHealthMetrics struct {
	LatencyP95  ServiceHealthMetric `json:"latencyP95"`
	ErrorRatio  ServiceHealthMetric `json:"errorRatio"`
	RequestRate ServiceHealthMetric `json:"requestRate"`
}

type ServiceHealthMetric struct {
	Value             *float64             `json:"value"`
	Unit              string               `json:"unit"`
	Points            []ServiceHealthPoint `json:"points"`
	UnavailableReason *string              `json:"unavailableReason"`
}

type ServiceHealthPoint struct {
	TimestampMS int64    `json:"timestampMs"`
	Value       *float64 `json:"value"`
}

type ServiceDependency struct {
	Name              string   `json:"name"`
	Namespace         *string  `json:"namespace"`
	Type              string   `json:"type"`
	LatencyP95MS      *float64 `json:"latencyP95Ms"`
	ErrorRatio        *float64 `json:"errorRatio"`
	UnavailableReason *string  `json:"unavailableReason"`
}

type rangeQueryResult struct {
	value     model.Value
	warnings  promv1.Warnings
	err       error
	attempted bool
}

// RenderServiceHealthTool displays bounded RED metrics and outbound dependencies.
var RenderServiceHealthTool = mcpgrafana.MustTool(
	"render_service_health",
	"Display an Application Observability service health summary with RED metrics and outbound dependencies. Health remains unknown unless an explicit health policy is evaluated; absent telemetry and partial query failures are reported.",
	renderServiceHealth,
	mcp.WithTitleAnnotation("Display service health"),
	mcp.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithUIResource(mcpgrafana.ServiceHealthResourceURI),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddServiceHealthAppTools registers the query tool only when queries are enabled.
func AddServiceHealthAppTools(s *server.MCPServer, enableQueryTools bool) {
	if enableQueryTools {
		RenderServiceHealthTool.Register(s)
	}
}

func renderServiceHealth(ctx context.Context, args ServiceHealthParams) (*mcp.CallToolResult, error) {
	duration, err := validateServiceHealthParams(&args)
	if err != nil {
		return nil, err
	}

	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	if strings.TrimSpace(config.URL) == "" {
		return nil, fmt.Errorf("grafana URL is not available in the authenticated request context")
	}
	client, err := backendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("get Prometheus backend for datasource %q: %w", args.DatasourceUID, err)
	}

	to := time.Now().UTC()
	from := to.Add(-duration)
	step := serviceHealthStep(duration)
	rateWindow := serviceHealthRateWindow(step)
	spanSelector := serviceMatcher(args.ServiceName, args.ServiceNamespace, "service", "service_namespace") + `,span_kind=~"SPAN_KIND_SERVER|SPAN_KIND_CONSUMER"`
	graphSelector := serviceMatcher(args.ServiceName, args.ServiceNamespace, "service", "namespace") + `,server!="",server!="user"`
	dependencyLabels := "server, server_namespace, server_service_namespace, connection_type"

	queries := map[string]string{
		"latency_classic":            fmt.Sprintf(`histogram_quantile(0.95, sum by (le) (rate(traces_spanmetrics_latency_bucket{%s}[%s]))) * 1000`, spanSelector, promDuration(rateWindow)),
		"latency_native":             fmt.Sprintf(`histogram_quantile(0.95, sum(rate(traces_spanmetrics_latency{%s}[%s]))) * 1000`, spanSelector, promDuration(rateWindow)),
		"error_ratio":                fmt.Sprintf(`(sum(rate(traces_spanmetrics_calls_total{%s,status_code="STATUS_CODE_ERROR"}[%s])) or vector(0)) / sum(rate(traces_spanmetrics_calls_total{%s}[%s]))`, spanSelector, promDuration(rateWindow), spanSelector, promDuration(rateWindow)),
		"request_rate":               fmt.Sprintf(`sum(rate(traces_spanmetrics_calls_total{%s}[%s]))`, spanSelector, promDuration(rateWindow)),
		"dependency_latency_classic": fmt.Sprintf(`topk(%d, histogram_quantile(0.95, sum by (le, %s) (rate(traces_service_graph_request_client_seconds_bucket{%s}[%s]))) * 1000)`, maxServiceDependencyQuery, dependencyLabels, graphSelector, promDuration(rateWindow)),
		"dependency_latency_native":  fmt.Sprintf(`topk(%d, histogram_quantile(0.95, sum by (%s) (rate(traces_service_graph_request_client_seconds{%s}[%s]))) * 1000)`, maxServiceDependencyQuery, dependencyLabels, graphSelector, promDuration(rateWindow)),
		"dependency_error_ratio":     fmt.Sprintf(`topk(%d, (sum by (%s) (rate(traces_service_graph_request_failed_total{%s}[%s])) or (0 * sum by (%s) (rate(traces_service_graph_request_total{%s}[%s])))) / sum by (%s) (rate(traces_service_graph_request_total{%s}[%s])))`, maxServiceDependencyQuery, dependencyLabels, graphSelector, promDuration(rateWindow), dependencyLabels, graphSelector, promDuration(rateWindow), dependencyLabels, graphSelector, promDuration(rateWindow)),
	}

	results := runServiceHealthQueries(ctx, client, from, to, step, queries,
		"latency_classic", "error_ratio", "request_rate", "dependency_latency_classic", "dependency_error_ratio")
	if results["latency_classic"].err != nil || isEmptyPrometheusValue(results["latency_classic"].value) {
		results["latency_native"] = executeServiceHealthQuery(ctx, client, queries["latency_native"], from, to, step)
	}
	if results["dependency_latency_classic"].err != nil || isEmptyPrometheusValue(results["dependency_latency_classic"].value) {
		results["dependency_latency_native"] = executeServiceHealthQuery(ctx, client, queries["dependency_latency_native"], from, to, step)
	}
	for name, result := range results {
		if result.err != nil {
			mcpgrafana.LoggerFromContext(ctx).WarnContext(ctx, "service health Prometheus query failed", "query", name, "error", result.err)
		}
	}

	warnings := make([]string, 0)
	latencyResult := preferredHistogramResult(results["latency_classic"], results["latency_native"])
	dependencyLatencyResult := preferredHistogramResult(results["dependency_latency_classic"], results["dependency_latency_native"])
	latency := serviceHealthMetricFromQuery("p95 latency", "ms", latencyResult, to, step, &warnings)
	errorRatio := serviceHealthMetricFromQuery("error ratio", "ratio", results["error_ratio"], to, step, &warnings)
	requestRate := serviceHealthMetricFromQuery("request rate", "requests_per_second", results["request_rate"], to, step, &warnings)
	dependencies := mergeServiceDependencies(dependencyLatencyResult, results["dependency_error_ratio"], &warnings)

	for _, result := range results {
		for _, warning := range result.warnings {
			warnings = append(warnings, "Prometheus warning: "+warning)
		}
	}
	warnings = uniqueStrings(warnings)

	var grafanaURL *string
	if deeplinkResolvesInRenderOrg(ctx, config.OrgID) {
		baseURL, linkErr := grafanaBaseURLFromContext(ctx)
		if linkErr == nil {
			grafanaURL, linkErr = serviceHealthGrafanaURL(baseURL, args, from, to)
		}
		if linkErr != nil {
			warnings = append(warnings, "Application Observability link unavailable: "+linkErr.Error())
		}
	} else {
		warnings = append(warnings, "Application Observability link unavailable for the selected Grafana organization.")
	}
	status, summary, description := serviceHealthSummary(latency, errorRatio, requestRate)
	result := ServiceHealthResult{
		ServiceName:      args.ServiceName,
		ServiceNamespace: args.ServiceNamespace,
		DatasourceUID:    args.DatasourceUID,
		TimeRange:        args.TimeRange,
		FromMS:           from.UnixMilli(),
		ToMS:             to.UnixMilli(),
		Status:           status,
		Summary:          summary,
		Description:      description,
		Metrics: ServiceHealthMetrics{
			LatencyP95:  latency,
			ErrorRatio:  errorRatio,
			RequestRate: requestRate,
		},
		Dependencies: dependencies,
		GrafanaURL:   grafanaURL,
		DashboardURL: grafanaURL,
		SLOURL:       nil,
		Warnings:     warnings,
	}

	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent(summary)},
		StructuredContent: result,
	}, nil
}

func validateServiceHealthParams(args *ServiceHealthParams) (time.Duration, error) {
	if err := validateDatasourceUID(args.DatasourceUID); err != nil {
		return 0, err
	}
	if err := validateServiceIdentity("service_name", args.ServiceName, true); err != nil {
		return 0, err
	}
	if args.ServiceNamespace != nil {
		namespace := strings.TrimSpace(*args.ServiceNamespace)
		if err := validateServiceIdentity("service_namespace", namespace, false); err != nil {
			return 0, err
		}
		if namespace == "" {
			args.ServiceNamespace = nil
		} else {
			args.ServiceNamespace = &namespace
		}
	}
	if args.TimeRange == "" {
		args.TimeRange = defaultServiceHealthTimeRange
	}
	duration, ok := serviceHealthDurations[args.TimeRange]
	if !ok {
		return 0, fmt.Errorf("time_range must be one of 1h, 6h, 24h, or 7d")
	}
	return duration, nil
}

func validateServiceIdentity(field, value string, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(value) > maxServiceIdentityLength {
		return fmt.Errorf("%s must be at most %d bytes", field, maxServiceIdentityLength)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

func serviceHealthStep(duration time.Duration) time.Duration {
	step := duration / (maxServiceHealthPoints - 1)
	if step < 30*time.Second {
		return 30 * time.Second
	}
	return step.Round(time.Second)
}

func serviceHealthRateWindow(step time.Duration) time.Duration {
	window := 4 * step
	if window < 5*time.Minute {
		return 5 * time.Minute
	}
	return window.Round(time.Minute)
}

func promDuration(duration time.Duration) string {
	seconds := int64(duration.Seconds())
	if seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}

func serviceMatcher(name string, namespace *string, nameLabel, namespaceLabel string) string {
	matcher := nameLabel + "=" + strconv.Quote(name)
	if namespace != nil {
		matcher += "," + namespaceLabel + "=" + strconv.Quote(*namespace)
	}
	return matcher
}

func runServiceHealthQueries(ctx context.Context, client promBackend, from, to time.Time, step time.Duration, queries map[string]string, names ...string) map[string]rangeQueryResult {
	results := make(map[string]rangeQueryResult, len(names))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := executeServiceHealthQuery(ctx, client, queries[name], from, to, step)
			mu.Lock()
			results[name] = result
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
}

func executeServiceHealthQuery(ctx context.Context, client promBackend, query string, from, to time.Time, step time.Duration) rangeQueryResult {
	value, warnings, err := client.Query(ctx, query, "range", from, to, int(step.Seconds()))
	return rangeQueryResult{value: value, warnings: warnings, err: err, attempted: true}
}

func preferredHistogramResult(classic, native rangeQueryResult) rangeQueryResult {
	if classic.err == nil && !isEmptyPrometheusValue(classic.value) {
		return classic
	}
	if native.err == nil && !isEmptyPrometheusValue(native.value) {
		return native
	}
	if native.attempted && native.err == nil {
		return native
	}
	if classic.err != nil {
		return classic
	}
	return native
}

func serviceHealthMetricFromQuery(name, unit string, query rangeQueryResult, expectedEnd time.Time, step time.Duration, warnings *[]string) ServiceHealthMetric {
	metric := ServiceHealthMetric{Unit: unit, Points: []ServiceHealthPoint{}}
	if query.err != nil {
		reason := fmt.Sprintf("The %s query failed.", name)
		metric.UnavailableReason = &reason
		*warnings = append(*warnings, reason)
		return metric
	}
	points, err := aggregatePrometheusPoints(query.value)
	if err != nil {
		reason := fmt.Sprintf("Could not read %s result: %v", name, err)
		metric.UnavailableReason = &reason
		*warnings = append(*warnings, reason)
		return metric
	}
	if len(points) == 0 {
		reason := fmt.Sprintf("No %s samples were found for this service and time range.", name)
		metric.UnavailableReason = &reason
		*warnings = append(*warnings, reason)
		return metric
	}
	if len(points) > maxServiceHealthPoints {
		*warnings = append(*warnings, fmt.Sprintf("%s samples are limited to the latest %d points.", name, maxServiceHealthPoints))
		points = points[len(points)-maxServiceHealthPoints:]
	}
	metric.Points = points
	validateMetricPoints(&metric, name, expectedEnd, step)
	return metric
}

func aggregatePrometheusPoints(value model.Value) ([]ServiceHealthPoint, error) {
	if value == nil {
		return nil, nil
	}
	matrix, ok := value.(model.Matrix)
	if !ok {
		return nil, fmt.Errorf("expected a Prometheus matrix, got %s", value.Type().String())
	}
	byTimestamp := make(map[int64][]float64)
	allTimestamps := make(map[int64]struct{})
	for _, stream := range matrix {
		for _, pair := range stream.Values {
			timestamp := int64(pair.Timestamp)
			allTimestamps[timestamp] = struct{}{}
			v := float64(pair.Value)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			byTimestamp[timestamp] = append(byTimestamp[timestamp], v)
		}
	}
	timestamps := make([]int64, 0, len(allTimestamps))
	for timestamp := range allTimestamps {
		timestamps = append(timestamps, timestamp)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	points := make([]ServiceHealthPoint, 0, len(timestamps))
	for _, timestamp := range timestamps {
		values := byTimestamp[timestamp]
		if len(values) == 0 {
			points = append(points, ServiceHealthPoint{TimestampMS: timestamp, Value: nil})
			continue
		}
		var total float64
		for _, value := range values {
			total += value
		}
		if math.IsNaN(total) || math.IsInf(total, 0) {
			points = append(points, ServiceHealthPoint{TimestampMS: timestamp})
			continue
		}
		pointValue := total
		points = append(points, ServiceHealthPoint{TimestampMS: timestamp, Value: &pointValue})
	}
	return points, nil
}

func validateMetricPoints(metric *ServiceHealthMetric, name string, expectedEnd time.Time, step time.Duration) {
	for i := range metric.Points {
		if !validServiceHealthValue(metric.Points[i].Value, metric.Unit) {
			metric.Points[i].Value = nil
		}
	}
	latest := metric.Points[len(metric.Points)-1]
	fresh := latest.TimestampMS >= expectedEnd.Add(-2*step).UnixMilli()
	if fresh && latest.Value != nil {
		value := *latest.Value
		metric.Value = &value
		return
	}
	reason := fmt.Sprintf("The latest %s sample was unavailable or outside its valid range.", name)
	metric.UnavailableReason = &reason
}

func validServiceHealthValue(value *float64, unit string) bool {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return false
	}
	return unit != "ratio" || *value <= 1
}

type dependencyKey struct {
	name       string
	namespace  string
	connection string
}

func mergeServiceDependencies(latency, errorRatio rangeQueryResult, warnings *[]string) []ServiceDependency {
	dependencies := make(map[dependencyKey]*ServiceDependency)
	merge := func(metricName string, query rangeQueryResult, assign func(*ServiceDependency, *float64)) {
		if query.err != nil {
			reason := fmt.Sprintf("The dependency %s query failed.", metricName)
			*warnings = append(*warnings, reason)
			return
		}
		matrix, ok := query.value.(model.Matrix)
		if !ok {
			if query.value != nil {
				*warnings = append(*warnings, fmt.Sprintf("Could not read dependency %s result: expected a Prometheus matrix, got %s", metricName, query.value.Type().String()))
			}
			return
		}
		for _, stream := range matrix {
			name := string(stream.Metric["server"])
			if name == "" || name == "user" {
				continue
			}
			namespace := string(stream.Metric["server_service_namespace"])
			if namespace == "" {
				namespace = string(stream.Metric["server_namespace"])
			}
			key := dependencyKey{
				name:       name,
				namespace:  namespace,
				connection: string(stream.Metric["connection_type"]),
			}
			dependency := dependencies[key]
			if dependency == nil {
				dependency = &ServiceDependency{Name: name, Namespace: optionalString(key.namespace), Type: dependencyType(key.connection)}
				dependencies[key] = dependency
			}
			if value := lastSampleValue(stream); value != nil {
				assign(dependency, value)
			}
		}
	}
	merge("p95 latency", latency, func(dependency *ServiceDependency, value *float64) {
		if validServiceHealthValue(value, "ms") {
			dependency.LatencyP95MS = value
		}
	})
	merge("error ratio", errorRatio, func(dependency *ServiceDependency, value *float64) {
		if validServiceHealthValue(value, "ratio") {
			dependency.ErrorRatio = value
		}
	})

	result := make([]ServiceDependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		if dependency.LatencyP95MS == nil || dependency.ErrorRatio == nil {
			reason := "One or more dependency metrics were unavailable."
			dependency.UnavailableReason = &reason
		}
		result = append(result, *dependency)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return stringValue(result[i].Namespace) < stringValue(result[j].Namespace)
		}
		return result[i].Name < result[j].Name
	})
	if len(result) > maxServiceDependencies {
		*warnings = append(*warnings, fmt.Sprintf("Dependencies are limited to the first %d results.", maxServiceDependencies))
		result = result[:maxServiceDependencies]
	}
	return result
}

func lastSampleValue(stream *model.SampleStream) *float64 {
	if len(stream.Values) == 0 {
		return nil
	}
	value := float64(stream.Values[len(stream.Values)-1].Value)
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func dependencyType(connection string) string {
	switch connection {
	case "database", "db":
		return "database"
	case "":
		return "unknown"
	default:
		return "service"
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func isEmptyPrometheusValue(value model.Value) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case model.Matrix:
		return len(typed) == 0
	case model.Vector:
		return len(typed) == 0
	default:
		return false
	}
}

func serviceHealthGrafanaURL(baseURL string, args ServiceHealthParams, from, to time.Time) (*string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Grafana URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("grafana URL must be an absolute HTTP or HTTPS URL")
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	job := args.ServiceName
	if args.ServiceNamespace != nil {
		job = *args.ServiceNamespace + "---" + args.ServiceName
	}
	basePath := strings.TrimRight(u.EscapedPath(), "/")
	u.RawPath = basePath + "/a/grafana-app-observability-app/services/service/" + url.PathEscape(job)
	decodedPath, err := url.PathUnescape(u.RawPath)
	if err != nil {
		return nil, fmt.Errorf("build Application Observability path: %w", err)
	}
	u.Path = decodedPath
	query := url.Values{}
	query.Set("from", strconv.FormatInt(from.UnixMilli(), 10))
	query.Set("to", strconv.FormatInt(to.UnixMilli(), 10))
	u.RawQuery = query.Encode()
	value := u.String()
	return &value, nil
}

func serviceHealthSummary(latency, errorRatio, requestRate ServiceHealthMetric) (string, string, string) {
	available := 0
	for _, metric := range []ServiceHealthMetric{latency, errorRatio, requestRate} {
		if metric.Value != nil {
			available++
		}
	}
	if available == 0 {
		return "unknown",
			"Service health is unavailable.",
			"No latency, error, or request-rate data was found for this service and time range."
	}
	return "unknown",
		"Service metrics are available. Health status has not been evaluated.",
		fmt.Sprintf("%d of 3 metrics are available. Alert and SLO status are unavailable.", available)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
