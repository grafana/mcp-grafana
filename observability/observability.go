// Package observability provides OpenTelemetry-based metrics, tracing, and
// log export for the MCP Grafana server.
//
// Metrics follow the OTel MCP semantic conventions using the mcpconv package.
// Tracing and log export are configured via standard OTEL_* environment variables.
package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	datasourceschemas "github.com/grafana/mcp-grafana/v2/tools/datasource_schemas"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/semconv/v1.40.0/mcpconv"
	"go.opentelemetry.io/otel/trace"
)

var otelErrHandlerOnce sync.Once

// Config holds configuration for observability features.
type Config struct {
	// MetricsEnabled enables Prometheus metrics at /metrics.
	MetricsEnabled bool

	// MetricsAddress is an optional separate address for the metrics server.
	// If empty, metrics are served on the main server.
	MetricsAddress string

	// NetworkTransport is the transport protocol used ("pipe" for stdio, "tcp" for HTTP).
	NetworkTransport mcpconv.NetworkTransportAttr

	// ServerName is the service name for OTel resource identification (e.g. "mcp-grafana").
	ServerName string

	// ServerVersion is the service version for OTel resource identification.
	ServerVersion string

	// SlowRequestThreshold, if positive, enables slow-request logging: any
	// MCP request whose duration exceeds this threshold is emitted via slog
	// at SlowRequestLogLevel. A zero or negative value disables slow-request
	// logging.
	SlowRequestThreshold time.Duration

	// SlowRequestLogLevel is the slog.Level at which slow-request events
	// are emitted. Accepts slog.LevelInfo or slog.LevelWarn.
	//
	// IMPORTANT: This field's zero value is slog.LevelInfo (not WARN),
	// because slog.LevelInfo == 0. The "warn default" advertised on the
	// CLI flag is applied by flag parsing in main.go — anyone constructing
	// observability.Config{} programmatically without going through CLI
	// parsing (tests, future library consumers) will get INFO unless they
	// set this field explicitly. Setup() does NOT apply a WARN default,
	// because doing so would prevent callers from ever selecting INFO
	// (zero-value is the only way to say "INFO" for an int-backed level).
	// Set this field explicitly in every non-CLI construction.
	SlowRequestLogLevel slog.Level

	// Logger is the *slog.Logger used for slow-request events. If nil,
	// slog.Default() is used. Primarily exists to allow tests to inject
	// a scoped, buffer-backed logger without mutating the process-global
	// default (which is race-prone when hooks run from goroutines).
	Logger *slog.Logger
}

// Observability manages the OpenTelemetry providers and Prometheus handler.
type Observability struct {
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
	loggerProvider *sdklog.LoggerProvider
	promHandler    http.Handler

	// Semconv MCP metrics
	operationDuration mcpconv.ServerOperationDuration

	// Network transport for attribute enrichment
	networkTransport mcpconv.NetworkTransportAttr

	// Slow-request logging configuration. When slowRequestThreshold > 0,
	// the middleware emits a slog event at slowRequestLogLevel whenever a
	// request duration exceeds the threshold. logger is always non-nil after
	// Setup (falls back to a dedicated handler at slowRequestLogLevel if
	// Config.Logger was nil, so slow-request events are not filtered by the
	// process-global handler's level).
	slowRequestThreshold time.Duration
	slowRequestLogLevel  slog.Level
	logger               *slog.Logger
}

// newSlowRequestLogger returns a *slog.Logger whose handler emits to stderr
// at exactly the given level. It is used by Setup when Config.Logger is nil
// so slow-request events are not silently dropped by the process-global
// handler's level filter (installed by main.go's slog.SetDefault).
func newSlowRequestLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
}

// Setup initializes the observability providers based on the configuration.
// When metrics are enabled, it creates a Prometheus exporter and registers
// a global MeterProvider. The otelhttp instrumentation will automatically
// use this provider for HTTP metrics.
//
// Trace export is gated on OTLPTracesEndpoint and log export on
// OTLPLogsEndpoint; use LoggerProvider() to retrieve the log provider for
// wiring into an slog.Handler (e.g., via the otelslog bridge). Other tracing
// behaviour is configured via standard OTEL_* environment variables
// (e.g., OTEL_TRACES_SAMPLER, OTEL_PROPAGATORS).
//
// Setup also installs the global TextMapPropagator used for trace-context
// propagation over HTTP. This happens regardless of whether trace export is
// enabled, so it must be called before serving requests.
func Setup(cfg Config) (_ *Observability, err error) {
	// Ensure OTel SDK internal errors (async export failures, queue drops, etc.)
	// surface through slog instead of the stdlib log package where operators
	// would never see them. Done once per process — sync.Once guards against
	// tests that construct multiple Observability instances clobbering each
	// other's expected handlers, and against replacing a user-installed handler.
	otelErrHandlerOnce.Do(func() {
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			fmt.Fprintf(os.Stderr, "otel sdk error: %v\n", err)
		}))
	})

	// Install a global TextMapPropagator so the server takes part in W3C
	// trace-context propagation over the HTTP transports.
	otel.SetTextMapPropagator(autoprop.NewTextMapPropagator())

	logger := cfg.Logger
	if logger == nil {
		logger = newSlowRequestLogger(cfg.SlowRequestLogLevel)
	}
	obs := &Observability{
		networkTransport:     cfg.NetworkTransport,
		slowRequestThreshold: cfg.SlowRequestThreshold,
		slowRequestLogLevel:  cfg.SlowRequestLogLevel,
		logger:               logger,
	}

	defer func() {
		if err == nil {
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = obs.Shutdown(shutdownCtx)
	}()

	res, err := sdkresource.Merge(
		sdkresource.Default(),
		sdkresource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.ServerName),
			semconv.ServiceVersion(cfg.ServerVersion),
		),
	)
	if err != nil {
		return nil, err
	}

	if OTLPTracesEndpoint() != "" {
		traceExporter, traceErr := otlptracegrpc.New(context.Background())
		if traceErr != nil {
			return nil, traceErr
		}
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
		obs.tracerProvider = tp
	}

	lp, err := setupLogging(context.Background(), res)
	if err != nil {
		return nil, err
	}
	obs.loggerProvider = lp

	if !cfg.MetricsEnabled {
		return obs, nil
	}

	exporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(res),
		sdkmetric.WithView(MetricNamesHistogramView()),
	)

	otel.SetMeterProvider(provider)

	obs.meterProvider = provider
	obs.promHandler = promhttp.HandlerFor(
		promclient.DefaultGatherer,
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	)

	meter := provider.Meter("mcp-grafana")

	obs.operationDuration, err = mcpconv.NewServerOperationDuration(meter, mcpHistogramBuckets)
	if err != nil {
		return nil, err
	}

	return obs, nil
}

// Shutdown gracefully shuts down the observability providers. Errors from all
// providers are collected so one provider's failure doesn't mask another's.
func (o *Observability) Shutdown(ctx context.Context) error {
	var wg sync.WaitGroup
	errs := make([]error, 3)

	if o.tracerProvider != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := o.tracerProvider.Shutdown(ctx); err != nil {
				errs[0] = fmt.Errorf("tracer provider shutdown: %w", err)
			}
		}()
	}
	if o.loggerProvider != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := o.loggerProvider.Shutdown(ctx); err != nil {
				errs[1] = fmt.Errorf("logger provider shutdown: %w", err)
			}
		}()
	}
	if o.meterProvider != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := o.meterProvider.Shutdown(ctx); err != nil {
				errs[2] = fmt.Errorf("meter provider shutdown: %w", err)
			}
		}()
	}

	wg.Wait()
	return errors.Join(errs...)
}

// MetricsHandler returns the Prometheus HTTP handler for serving metrics.
// Returns nil if metrics are not enabled.
func (o *Observability) MetricsHandler() http.Handler {
	return o.promHandler
}

// WrapHandler wraps an http.Handler with OpenTelemetry instrumentation.
// This adds automatic tracing and metrics for HTTP requests including:
//   - http.server.request.duration (histogram)
//   - http.server.request.body.size (histogram)
//   - http.server.response.body.size (histogram)
//
// The operation parameter is used as the span name.
//
// The server span is parented to any trace context carried by the incoming
// request headers, as read by the global TextMapPropagator installed by Setup;
// with no propagator installed, inbound trace context is ignored and the span
// starts a new trace.
func WrapHandler(h http.Handler, operation string) http.Handler {
	return otelhttp.NewHandler(h, operation)
}

// metricsEnabled returns true if semconv metrics have been initialized.
func (o *Observability) metricsEnabled() bool {
	return o.operationDuration.Inst() != nil
}

// toolNameFromRequest extracts the tool name from a tools/call request.
// Returns (name, true) when the method is "tools/call" and the request carries
// a valid tool name; ("", false) otherwise.
func toolNameFromRequest(method string, req mcp.Request) (string, bool) {
	if method != "tools/call" {
		return "", false
	}
	callReq, ok := req.(*mcp.CallToolRequest)
	if !ok || callReq == nil || callReq.Params == nil {
		return "", false
	}
	return callReq.Params.Name, true
}

// toolArgsFromRequest unmarshals the arguments from a tools/call request into
// a map. Returns nil for non-tools/call methods or when unmarshal fails.
func toolArgsFromRequest(method string, req mcp.Request) map[string]any {
	if method != "tools/call" {
		return nil
	}
	callReq, ok := req.(*mcp.CallToolRequest)
	if !ok || callReq == nil || callReq.Params == nil || len(callReq.Params.Arguments) == 0 {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(callReq.Params.Arguments, &args); err != nil {
		return nil
	}
	return args
}

// buildOperationAttrs assembles semconv attributes for an operation duration recording.
// toolName and args are pre-extracted by the middleware to avoid double-parsing.
func (o *Observability) buildOperationAttrs(ctx context.Context, method string, toolName string, toolNameOK bool, args map[string]any, req mcp.Request, result mcp.Result, err error) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	if toolNameOK {
		attrs = append(attrs, o.operationDuration.AttrGenAIToolName(toolName))
	}

	name := toolName
	md := ToolMetricDimensions(name, args, result)
	if md.Operation != "" {
		attrs = append(attrs, attribute.String(attrKeyToolOperation, md.Operation))
	}
	if md.ResourceType != "" {
		attrs = append(attrs, attribute.String(attrKeyToolResourceType, md.ResourceType))
	}
	if md.Phase != "" {
		attrs = append(attrs, attribute.String(attrKeyToolPhase, md.Phase))
	}

	if err != nil {
		attrs = append(attrs, o.operationDuration.AttrErrorType(mcpconv.ErrorTypeAttr(errorTypeName(err))))
	}

	if o.networkTransport != "" {
		attrs = append(attrs, o.operationDuration.AttrNetworkTransport(o.networkTransport))
	}

	if req != nil {
		if pv := protocolVersionFromRequest(req); pv != "" {
			attrs = append(attrs, o.operationDuration.AttrProtocolVersion(pv))
		}
	}

	return attrs
}

// protocolVersionFromRequest extracts the negotiated protocol version from a
// server request, if available. ServerRequest.ProtocolVersion() returns the
// version the server negotiated (not the client's requested version).
func protocolVersionFromRequest(req mcp.Request) string {
	type protocolVersioner interface {
		ProtocolVersion() string
	}
	if pv, ok := req.(protocolVersioner); ok {
		return pv.ProtocolVersion()
	}
	return ""
}

// Attribute keys for tool telemetry dimensions. operation, resourceType and
// phase become metric labels once bounded by the toolMetricDims value
// allowlist; target is high-cardinality and span-only. Spans carry the raw,
// unbounded values for all of them.
const (
	attrKeyToolOperation    = "mcp.tool.operation"
	attrKeyToolResourceType = "mcp.tool.resource_type"
	attrKeyToolTarget       = "mcp.tool.target"
	attrKeyToolPhase        = "mcp.tool.phase"
)

// ToolPhaseMetaKey is the result _meta key a tool sets to declare which phase of
// a multi-call flow a given call represents — e.g. create_datasource's
// schema-guidance response vs the actual creation.
const ToolPhaseMetaKey = attrKeyToolPhase

// ValueOther is the single bucket every non-allowlisted value collapses
// into. Exported because the usage-statistics reporter clamps its own
// vocabularies with the same sentinel: two spellings of "not allowlisted"
// would let one of them drift and start carrying unbounded values.
const ValueOther = "other"

type metricDimSet struct {
	operations    map[string]struct{}
	resourceTypes map[string]struct{}
	phases        map[string]struct{}
}

// toolMetricDims is the opt-in allowlist of which dimensions each tool may emit
// as metric labels, and which values those labels may carry.
// A label is emitted only for a listed tool, only for a dimension that tool
// opts into, and only with a value in that dimension's set - everything else becomes ValueOther
var toolMetricDims = map[string]metricDimSet{
	"alerting_rules_read":  {operations: ValueSet("list", "get", "versions")},
	"alerting_rules_write": {operations: ValueSet("list", "get", "versions", "create", "update", "delete")},
	"alerting_manage_routing": {operations: ValueSet(
		"get_notification_policies", "get_contact_points", "get_contact_point",
		"get_time_intervals", "get_time_interval",
	)},
	"agento11y_manage_conversations": {operations: ValueSet("list", "search", "get")},
	"agento11y_manage_generations":   {operations: ValueSet("get", "scores")},
	"agento11y_manage_experiments": {operations: ValueSet(
		"list", "get", "get_report", "list_trials", "list_scores",
		"get_trial", "list_trial_scores", "list_trial_artifacts", "list_facets",
		"update", "cancel",
	)},
	"agento11y_manage_test_suites": {operations: ValueSet(
		"list_suites", "get_suite", "list_test_cases", "get_test_case",
		"create_suite", "update_suite", "create_draft_version", "publish_version",
		"upsert_test_case", "delete_test_case",
	)},
	"create_datasource": {
		resourceTypes: datasourcePluginTypes,
		// Mirrors dsPhaseSchema/dsPhaseCreated in tools/datasources.go, which
		// sets these on the result _meta. Duplicated rather than shared because
		// tools imports this package, so the constants cannot be imported back.
		phases: ValueSet("schema", "created"),
	},
}

// datasourcePluginTypes bounds create_datasource's mcp.tool.resource_type label
// to the plugin types we ship a schema for. Types outside the set remain
// creatable — they just report as ValueOther rather than each becoming
// its own series.
var datasourcePluginTypes = ValueSet(datasourceschemas.KnownPluginTypes()...)

// ValueSet builds a membership set for an allowlist, for use with BoundedValue.
func ValueSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return set
}

// BoundedValue maps v onto the allowed value set: v itself when allowed,
// ValueOther when not, and "" when absent — an unset argument keeps the
// label off the series entirely rather than counting as "other".
//
// This is the one clamp for every bounded vocabulary in the server, metric
// label or usage-statistics field alike. A second copy is a privacy bug
// waiting to happen, not untidiness: the copy that stops being updated is the
// one that starts emitting user-supplied strings.
func BoundedValue(v string, allowed map[string]struct{}) string {
	if v == "" {
		return ""
	}
	if _, ok := allowed[v]; ok {
		return v
	}
	return ValueOther
}

type toolArgDims struct {
	operation    string
	resourceType string
	target       string
}

// toolArgDimensionsFromArgs extracts telemetry dimensions from a tool call's
// argument map and tool name.
func toolArgDimensionsFromArgs(args map[string]any) toolArgDims {
	dims := toolArgDims{
		operation:    stringArg(args, "operation"),
		resourceType: stringArg(args, "type"),
		target:       stringArg(args, "uid"),
	}
	if dims.target == "" {
		dims.target = stringArg(args, "name")
	}
	return dims
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// toolPhaseFromResult reads the phase a tool declared on its result via the
// ToolPhaseMetaKey _meta field. In the go-sdk, Meta is map[string]any.
func toolPhaseFromResult(result any) string {
	res, ok := result.(*mcp.CallToolResult)
	if !ok || res == nil || len(res.Meta) == 0 {
		return ""
	}
	if v, ok := res.Meta[ToolPhaseMetaKey].(string); ok {
		return v
	}
	return ""
}

// ToolMetricDims are the bounded, allowlist-approved dimensions safe to attach
// as metric labels. Target is excluded (span-only). A field is "" when the tool
// is not allowlisted for it or the value is absent, and "other" when the tool is
// allowlisted for it but the caller's value is not.
type ToolMetricDims struct {
	Operation    string
	ResourceType string
	Phase        string
}

// ToolMetricDimensions returns the metric-safe label dimensions for a tools/call,
// applying the same tool-and-value allowlist the server's own metrics use.
// args is the raw argument map; result is the *mcp.CallToolResult for the phase,
// and may be nil on the error path.
func ToolMetricDimensions(toolName string, args map[string]any, result any) ToolMetricDims {
	allowed := toolMetricDims[toolName]
	var d ToolMetricDims
	if allowed.operations != nil {
		d.Operation = BoundedValue(stringArg(args, "operation"), allowed.operations)
	}
	if allowed.resourceTypes != nil {
		d.ResourceType = BoundedValue(stringArg(args, "type"), allowed.resourceTypes)
	}
	if allowed.phases != nil {
		d.Phase = BoundedValue(toolPhaseFromResult(result), allowed.phases)
	}
	return d
}

// enrichSpanWithToolDims attaches the tool-argument dimensions to the active
// span when one is recording, including the high-cardinality target that is
// kept off metrics.
// enrichSpanWithToolDims attaches tool-argument dimensions to the active span.
// args is pre-extracted by the middleware to avoid double-parsing.
func (o *Observability) enrichSpanWithToolDims(ctx context.Context, method string, args map[string]any, result mcp.Result) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	if method != "tools/call" {
		return
	}
	dims := toolArgDimensionsFromArgs(args)
	var spanAttrs []attribute.KeyValue
	if dims.operation != "" {
		spanAttrs = append(spanAttrs, attribute.String(attrKeyToolOperation, dims.operation))
	}
	if dims.resourceType != "" {
		spanAttrs = append(spanAttrs, attribute.String(attrKeyToolResourceType, dims.resourceType))
	}
	if dims.target != "" {
		spanAttrs = append(spanAttrs, attribute.String(attrKeyToolTarget, dims.target))
	}
	if phase := toolPhaseFromResult(result); phase != "" {
		spanAttrs = append(spanAttrs, attribute.String(attrKeyToolPhase, phase))
	}
	if len(spanAttrs) > 0 {
		span.SetAttributes(spanAttrs...)
	}
}

// maybeLogSlowRequest emits a slog event at o.slowRequestLogLevel when the
// request duration exceeds o.slowRequestThreshold.
func (o *Observability) maybeLogSlowRequest(ctx context.Context, method string, toolName string, duration time.Duration, err error) {
	if o.slowRequestThreshold <= 0 || duration <= o.slowRequestThreshold {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	attrs := []slog.Attr{
		slog.String("mcp.method", method),
		slog.Duration("duration", duration),
		slog.Duration("threshold", o.slowRequestThreshold),
	}
	if toolName != "" {
		attrs = append(attrs, slog.String("tool", toolName))
	}
	if err != nil {
		attrs = append(attrs,
			slog.Any("error", err),
			slog.String("error.type", errorTypeName(err)),
		)
	}
	o.logger.LogAttrs(ctx, o.slowRequestLogLevel, "Slow request", attrs...)
}

func errorTypeName(err error) string {
	type errorTyper interface {
		ErrorType() string
	}
	if et, ok := err.(errorTyper); ok {
		return et.ErrorType()
	}
	return "_OTHER"
}

// MCPMiddleware returns an mcp.Middleware that records MCP metrics, enriches the
// active trace span with tool dimensions, and/or emits slow-request logs, per
// configuration.
//
// Gate (metrics, slow-log, tracing):
//   - all off → identity middleware (zero-overhead path)
//   - any on → timing wrapper registered; span enrichment runs whenever a span
//     is recording, while metric recording and slow-log emission are each
//     guarded in the body.
func (o *Observability) MCPMiddleware() mcp.Middleware {
	metricsOn := o.metricsEnabled()
	slowLogOn := o.slowRequestThreshold > 0
	tracingOn := o.tracerProvider != nil

	if !metricsOn && !slowLogOn && !tracingOn {
		return func(next mcp.MethodHandler) mcp.MethodHandler { return next }
	}

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			start := time.Now()
			result, err := next(ctx, method, req)
			duration := time.Since(start)

			// Parse tool name and arguments once, shared by metrics, spans, and slow-log.
			toolName, toolNameOK := toolNameFromRequest(method, req)
			args := toolArgsFromRequest(method, req)

			o.enrichSpanWithToolDims(ctx, method, args, result)
			if metricsOn {
				attrs := o.buildOperationAttrs(ctx, method, toolName, toolNameOK, args, req, result, err)
				o.operationDuration.Record(ctx, duration.Seconds(), mcpconv.MethodNameAttr(method), attrs...)
			}
			o.maybeLogSlowRequest(ctx, method, toolName, duration, err)
			return result, err
		}
	}
}

// LoggerProvider returns the OTLP log provider, or nil if OTLP logging is
// not configured (OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
// not set).
func (o *Observability) LoggerProvider() *sdklog.LoggerProvider {
	return o.loggerProvider
}

// TracerProvider returns the OTLP tracer provider, or nil if OTLP tracing is
// not configured (OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
// not set).
func (o *Observability) TracerProvider() *sdktrace.TracerProvider {
	return o.tracerProvider
}

// MeterProvider returns the metric.MeterProvider backing this Observability
// instance's metrics, or nil if metrics are not enabled (Config.MetricsEnabled
// was false).
func (o *Observability) MeterProvider() metric.MeterProvider {
	if o.meterProvider == nil {
		return nil
	}
	return o.meterProvider
}
