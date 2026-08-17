// Package observability provides OpenTelemetry-based metrics, tracing, and
// log export for the MCP Grafana server.
//
// Metrics follow the OTel MCP semantic conventions using the mcpconv package.
// Tracing and log export are configured via standard OTEL_* environment variables.
package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	datasourceschemas "github.com/grafana/mcp-grafana/tools/datasource_schemas"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/mcpconv"
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

// sessionMeta holds per-session metadata for enriching metrics.
// The protocolVersion field is set atomically via sync.Map.Store to avoid
// data races between OnAfterInitialize (writer) and OnSuccess/OnError (readers).
type sessionMeta struct {
	startTime       time.Time
	protocolVersion atomic.Value // stores string
}

// Observability manages the OpenTelemetry providers and Prometheus handler.
type Observability struct {
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
	loggerProvider *sdklog.LoggerProvider
	promHandler    http.Handler

	// Semconv MCP metrics
	operationDuration mcpconv.ServerOperationDuration
	sessionDuration   mcpconv.ServerSessionDuration

	// Network transport for attribute enrichment
	networkTransport mcpconv.NetworkTransportAttr

	// Track request start times for duration calculation
	requestStartTimes sync.Map // map[any]time.Time keyed by request ID

	// Per-session metadata (protocol version, start time)
	sessions sync.Map // map[string]*sessionMeta keyed by session ID

	// Slow-request logging configuration. When slowRequestThreshold > 0,
	// the hooks emit a slog event at slowRequestLogLevel whenever a request
	// duration exceeds the threshold. logger is always non-nil after Setup
	// (falls back to a dedicated handler at slowRequestLogLevel if
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
// (e.g., OTEL_TRACES_SAMPLER).
func Setup(cfg Config) (_ *Observability, err error) {
	// Ensure OTel SDK internal errors (async export failures, queue drops, etc.)
	// surface through slog instead of the stdlib log package where operators
	// would never see them. Done once per process — sync.Once guards against
	// tests that construct multiple Observability instances clobbering each
	// other's expected handlers, and against replacing a user-installed handler.
	otelErrHandlerOnce.Do(func() {
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			// Write directly to stderr rather than slog.Default(). If the
			// default handler routes to OTLP (via the fanout), emitting an
			// SDK error through slog would re-enter the SDK (otelslog bridge
			// → processor → otel.Handle) and either churn on every failed
			// batch or, with a synchronous processor, recurse unbounded.
			fmt.Fprintf(os.Stderr, "otel sdk error: %v\n", err)
		}))
	})

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

	// Shut down any providers already populated if Setup fails mid-way. This
	// is the symmetric counterpart to Shutdown() on the success path: callers
	// who never receive a non-nil Observability can't call Shutdown themselves,
	// so their background goroutines and gRPC connections would otherwise leak
	// for the process lifetime.
	defer func() {
		if err == nil {
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = obs.Shutdown(shutdownCtx)
	}()

	// Build OTel resource with service identity.
	// This is shared by both tracing and metrics providers.
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

	// Set up OTLP trace exporter when a traces endpoint is configured.
	// The gRPC exporter respects standard OTEL_* env vars for endpoint, headers,
	// TLS (OTEL_EXPORTER_OTLP_INSECURE), etc.
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

	// Set up OTLP log exporter when OTEL_EXPORTER_OTLP_ENDPOINT or
	// OTEL_EXPORTER_OTLP_LOGS_ENDPOINT is set; see setupLogging for the gating
	// logic. On error, the deferred rollback above shuts down any tracer
	// provider already populated.
	lp, err := setupLogging(context.Background(), res)
	if err != nil {
		return nil, err
	}
	obs.loggerProvider = lp

	if !cfg.MetricsEnabled {
		return obs, nil
	}

	// Create Prometheus exporter using default aggregation so that
	// explicit bucket boundaries from the semconv spec are preserved.
	exporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}

	// Create MeterProvider with the Prometheus exporter and resource
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(res),
	)

	// Register as global MeterProvider so otelhttp instrumentation uses it
	otel.SetMeterProvider(provider)

	obs.meterProvider = provider
	// Use HandlerFor with EnableOpenMetrics to properly expose native histograms
	obs.promHandler = promhttp.HandlerFor(
		promclient.DefaultGatherer,
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	)

	// Create MCP protocol metrics using semconv types with explicit bucket boundaries
	meter := provider.Meter("mcp-grafana")

	obs.operationDuration, err = mcpconv.NewServerOperationDuration(meter, mcpHistogramBuckets)
	if err != nil {
		return nil, err
	}

	obs.sessionDuration, err = mcpconv.NewServerSessionDuration(meter, mcpHistogramBuckets)
	if err != nil {
		return nil, err
	}

	return obs, nil
}

// Shutdown gracefully shuts down the observability providers. Errors from all
// providers are collected so one provider's failure doesn't mask another's.
func (o *Observability) Shutdown(ctx context.Context) error {
	var errs []error
	if o.tracerProvider != nil {
		if err := o.tracerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer provider shutdown: %w", err))
		}
	}
	if o.loggerProvider != nil {
		if err := o.loggerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("logger provider shutdown: %w", err))
		}
	}
	if o.meterProvider != nil {
		if err := o.meterProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider shutdown: %w", err))
		}
	}
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
func WrapHandler(h http.Handler, operation string) http.Handler {
	return otelhttp.NewHandler(h, operation)
}

// metricsEnabled returns true if semconv metrics have been initialized.
func (o *Observability) metricsEnabled() bool {
	return o.operationDuration.Inst() != nil
}

// buildOperationAttrs assembles semconv attributes for an operation duration recording.
func (o *Observability) buildOperationAttrs(ctx context.Context, method mcp.MCPMethod, message any, result any, err error) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	// Centralised through toolNameFromMessage so metrics and slow-log share
	// one definition of "tools/call request reached." The bool distinguishes
	// "no valid request" (skip emission) from "valid request, empty Name"
	// (emit ""), preserving pre-existing OTel attribute presence semantics.
	name, ok := toolNameFromMessage(method, message)
	if ok {
		attrs = append(attrs, o.operationDuration.AttrGenAIToolName(name))
	}

	// Argument-derived and phase dimensions for slicing per-call durations, via
	// the same allowlist-gated helper external consumers use. Reading args into
	// locals is free of cardinality risk; only allowlisted dimensions become
	// labels, and only with allowlisted values (others collapse to "other"). The
	// high-cardinality target is span-only (see enrichSpanWithToolDims).
	var args map[string]any
	if req, ok := message.(*mcp.CallToolRequest); ok && req != nil {
		args = req.GetArguments()
	}
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

	// error.type when there's an error
	if err != nil {
		attrs = append(attrs, o.operationDuration.AttrErrorType(mcpconv.ErrorTypeAttr(errorTypeName(err))))
	}

	// network.transport
	if o.networkTransport != "" {
		attrs = append(attrs, o.operationDuration.AttrNetworkTransport(o.networkTransport))
	}

	// mcp.protocol.version from session context
	// Note: mcp.session.id is a span-only attribute (not on metrics) to avoid cardinality explosion.
	if session := server.ClientSessionFromContext(ctx); session != nil {
		if meta, ok := o.sessions.Load(session.SessionID()); ok {
			sm := meta.(*sessionMeta)
			if pv, ok := sm.protocolVersion.Load().(string); ok && pv != "" {
				attrs = append(attrs, o.operationDuration.AttrProtocolVersion(pv))
			}
		}
	}

	return attrs
}

// toolNameFromMessage extracts the tool name from an MCP hook's message
// argument when the method is tools/call and the message is a non-nil
// *mcp.CallToolRequest. Returns (name, true) when the assertion succeeds,
// including when the name is the empty string. Returns ("", false) for
// wrong method, nil message, or wrong-type message.
//
// The bool lets callers distinguish "no valid tools/call request reached"
// (don't emit attributes) from "valid request reached, Name happens to be
// empty" (emit with empty value). buildOperationAttrs uses the bool to
// preserve pre-existing metric series identity for zero-value Params.Name.
func toolNameFromMessage(method mcp.MCPMethod, message any) (string, bool) {
	if method != "tools/call" {
		return "", false
	}
	req, ok := message.(*mcp.CallToolRequest)
	if !ok || req == nil {
		return "", false
	}
	return req.Params.Name, true
}

// Attribute keys for tool-argument-derived telemetry dimensions. operation and
// resourceType become metric labels once bounded by the toolMetricDims value
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
// schema-guidance response vs the actual creation. When present, its string
// value is surfaced as the bounded mcp.tool.phase metric label (and span
// attribute). Because it is read from the result, it reflects what the tool
// actually did (e.g. a no-schema type that creates directly) rather than what
// the request asked for. Tools MUST keep the value set bounded/low-cardinality,
// since it becomes a Prometheus label.
//
// Living in result _meta, it is also transmitted to the client — intentional: a
// non-sensitive flow-control signal for telling schema guidance from an actual
// creation.
const ToolPhaseMetaKey = attrKeyToolPhase

// metricDimValueOther is the single bucket every non-allowlisted value collapses
// into.
const metricDimValueOther = "other"

// metricDimSet declares which tool-argument-derived dimensions a given tool may
// contribute as BOUNDED metric labels, and for each one the exact set of values
// permitted. A nil set means the tool contributes nothing for that dimension.
type metricDimSet struct {
	operations    map[string]struct{}
	resourceTypes map[string]struct{}
}

// toolMetricDims is the opt-in allowlist of which argument-derived dimensions
// each tool may emit as metric labels, and which values those labels may carry.
// A label is emitted only for a listed tool, only for a dimension that tool
// opts into, and only with a value in that dimension's set - everything else becomes metricDimValueOther
var toolMetricDims = map[string]metricDimSet{
	"alerting_manage_rules": {operations: valueSet("list", "get", "versions", "create", "update", "delete")},
	"alerting_manage_routing": {operations: valueSet(
		"get_notification_policies", "get_contact_points", "get_contact_point",
		"get_time_intervals", "get_time_interval",
	)},
	"agento11y_manage_conversations": {operations: valueSet("list", "search", "get")},
	"agento11y_manage_generations":   {operations: valueSet("get", "scores")},
	"agento11y_manage_experiments": {operations: valueSet(
		"list", "get", "get_report", "list_trials", "list_scores",
		"get_trial", "list_trial_scores", "list_trial_artifacts", "list_facets",
		"update", "cancel",
	)},
	"agento11y_manage_test_suites": {operations: valueSet(
		"list_suites", "get_suite", "list_test_cases", "get_test_case",
		"create_suite", "update_suite", "create_draft_version", "publish_version",
		"upsert_test_case", "delete_test_case",
	)},
	"create_datasource": {resourceTypes: datasourcePluginTypes},
}

// datasourcePluginTypes bounds create_datasource's mcp.tool.resource_type label
// to the plugin types we ship a schema for. Types outside the set remain
// creatable — they just report as metricDimValueOther rather than each becoming
// its own series.
var datasourcePluginTypes = valueSet(datasourceschemas.KnownPluginTypes()...)

// valueSet builds a membership set for metricDimSet.
func valueSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return set
}

// boundedMetricValue maps v onto the allowed value set: v itself when allowed,
// metricDimValueOther when not, and "" when absent — an unset argument keeps the
// label off the series entirely rather than counting as "other".
func boundedMetricValue(v string, allowed map[string]struct{}) string {
	if v == "" {
		return ""
	}
	if _, ok := allowed[v]; ok {
		return v
	}
	return metricDimValueOther
}

// toolArgDims holds the tool-call argument dimensions used to enrich telemetry.
type toolArgDims struct {
	// operation is the sub-action discriminator taken from the "operation"
	// argument (e.g. alerting_manage_rules "list"/"create"/"update"). Empty for
	// tools without an operation argument.
	operation string
	// resourceType is a resource discriminator taken from the "type" argument
	// (e.g. the datasource plugin type on create_datasource). Empty when absent.
	resourceType string
	// target identifies the specific entity acted on: the "uid" argument when
	// present, else "name". High-cardinality — span-only, never a metric label.
	// Empty whenever the call names no entity, which includes create_datasource's
	// first (phase=schema) call: the datasource does not exist yet, so there is
	// nothing to key on. Correlating that call with the later phase=created one
	// therefore relies on the shared trace when the client propagates context,
	// else on mcp.session.id plus resource_type — not on target.
	target string
}

// toolArgDimensions extracts telemetry dimensions from a tools/call request's
// arguments. It returns the zero value for non-tools/call methods, a nil or
// wrong-type message, or arguments that are not a JSON object — mirroring
// toolNameFromMessage's gating so metrics and spans agree on when "a tool call
// reached."
func toolArgDimensions(method mcp.MCPMethod, message any) toolArgDims {
	if method != "tools/call" {
		return toolArgDims{}
	}
	req, ok := message.(*mcp.CallToolRequest)
	if !ok || req == nil {
		return toolArgDims{}
	}
	args := req.GetArguments()
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

// stringArg returns args[key] when it holds a string, else "".
func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// toolPhaseFromResult reads the phase a tool declared on its result via the
// ToolPhaseMetaKey _meta field. Returns "" when result is not a non-nil
// *mcp.CallToolResult, carries no _meta, or the value is absent/not a string.
// This is authoritative — it reflects what the tool actually did — as opposed
// to phase inferred from the request arguments.
func toolPhaseFromResult(result any) string {
	res, ok := result.(*mcp.CallToolResult)
	if !ok || res == nil || res.Meta == nil {
		return ""
	}
	if v, ok := res.Meta.AdditionalFields[ToolPhaseMetaKey].(string); ok {
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
// applying the same tool-and-value allowlist the server's own metrics use:
// argument-derived values outside the allowed set come back as "other", so the
// returned Operation/ResourceType are safe to use as labels for any input. It
// lets external consumers (e.g. the hosted Cloud MCP server, which records tool
// metrics via its own middleware) stay in lockstep with the allowlist instead of
// re-deriving it. args is the raw argument map (request.GetArguments()); result
// is the *mcp.CallToolResult for the phase, and may be nil on the error path.
func ToolMetricDimensions(toolName string, args map[string]any, result any) ToolMetricDims {
	allowed := toolMetricDims[toolName]
	var d ToolMetricDims
	if allowed.operations != nil {
		d.Operation = boundedMetricValue(stringArg(args, "operation"), allowed.operations)
	}
	if allowed.resourceTypes != nil {
		d.ResourceType = boundedMetricValue(stringArg(args, "type"), allowed.resourceTypes)
	}
	d.Phase = toolPhaseFromResult(result)
	return d
}

// enrichSpanWithToolDims attaches the tool-argument dimensions to the active
// span when one is recording, including the high-cardinality target that is
// kept off metrics. It is a no-op when no span is recording (e.g. stdio
// transport, or tracing not configured) or the message is not a tools/call
// request. Attributes land on whatever span is active for the request
// (typically the otelhttp server span), enabling trace-level filtering and —
// for calls that name an entity — task-span correlation by target.
func (o *Observability) enrichSpanWithToolDims(ctx context.Context, method mcp.MCPMethod, message any, result any) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	dims := toolArgDimensions(method, message)
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
// request duration exceeds o.slowRequestThreshold. It is a no-op when the
// threshold is zero or negative, or when the duration is at or below the
// threshold. The toolName argument should already be extracted via
// toolNameFromMessage at the call site; pass "" when unknown or not
// applicable.
//
// A nil ctx is defensively coerced to context.Background() because some MCP
// transports (notably stdio) may invoke hooks without a request-scoped
// context, and slog.LogAttrs on a nil ctx can panic.
func (o *Observability) maybeLogSlowRequest(ctx context.Context, method mcp.MCPMethod, toolName string, duration time.Duration, err error) {
	if o.slowRequestThreshold <= 0 || duration <= o.slowRequestThreshold {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	attrs := []slog.Attr{
		slog.String("mcp.method", string(method)),
		slog.Duration("duration", duration),
		slog.Duration("threshold", o.slowRequestThreshold),
	}
	if toolName != "" {
		attrs = append(attrs, slog.String("tool", toolName))
	}
	if err != nil {
		// error: best-effort human-readable context (content bound is
		// controlled by upstream error-wrapping, not by slog).
		// error.type: bounded cardinality via errorTypeName, same helper
		// used by the metrics path in buildOperationAttrs.
		attrs = append(attrs,
			slog.Any("error", err),
			slog.String("error.type", errorTypeName(err)),
		)
	}
	o.logger.LogAttrs(ctx, o.slowRequestLogLevel, "Slow request", attrs...)
}

// errorTypeName returns a descriptive error type string from an error.
func errorTypeName(err error) string {
	type errorTyper interface {
		ErrorType() string
	}
	if et, ok := err.(errorTyper); ok {
		return et.ErrorType()
	}
	return "_OTHER"
}

// MCPHooks returns server.Hooks that record MCP metrics, enrich the active trace
// span with tool dimensions, and/or emit slow-request logs, per configuration.
// Merge with existing hooks via MergeHooks.
//
// Gate (metrics, slow-log, tracing):
//   - all off → empty hooks (zero-overhead path)
//   - any on → request hooks registered; span enrichment runs whenever a span is
//     recording, while metric recording and slow-log emission are each guarded
//     in the hook body. Tracing must gate here too, else a tracing-only
//     deployment (traces on, --metrics off, no slow-log) would register no hooks
//     and never enrich its spans.
//   - session hooks only when metrics are on (session duration is a metric).
func (o *Observability) MCPHooks() *server.Hooks {
	metricsOn := o.metricsEnabled()
	slowLogOn := o.slowRequestThreshold > 0
	tracingOn := o.tracerProvider != nil

	if !metricsOn && !slowLogOn && !tracingOn {
		// Nothing to do, return empty hooks
		return &server.Hooks{}
	}

	hooks := &server.Hooks{
		OnBeforeAny: []server.BeforeAnyHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
				o.requestStartTimes.Store(id, time.Now())
			},
		},
		OnSuccess: []server.OnSuccessHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {
				startTime, ok := o.requestStartTimes.LoadAndDelete(id)
				if !ok {
					return
				}
				duration := time.Since(startTime.(time.Time))
				o.enrichSpanWithToolDims(ctx, method, message, result)
				if o.metricsEnabled() {
					attrs := o.buildOperationAttrs(ctx, method, message, result, nil)
					o.operationDuration.Record(ctx, duration.Seconds(), mcpconv.MethodNameAttr(method), attrs...)
				}
				toolName, _ := toolNameFromMessage(method, message)
				o.maybeLogSlowRequest(ctx, method, toolName, duration, nil)
			},
		},
		OnError: []server.OnErrorHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
				startTime, ok := o.requestStartTimes.LoadAndDelete(id)
				if !ok {
					return
				}
				duration := time.Since(startTime.(time.Time))
				o.enrichSpanWithToolDims(ctx, method, message, nil)
				if o.metricsEnabled() {
					attrs := o.buildOperationAttrs(ctx, method, message, nil, err)
					o.operationDuration.Record(ctx, duration.Seconds(), mcpconv.MethodNameAttr(method), attrs...)
				}
				toolName, _ := toolNameFromMessage(method, message)
				o.maybeLogSlowRequest(ctx, method, toolName, duration, err)
			},
		},
	}

	// Session-tracking hooks only populate when metrics are enabled —
	// slow-log does not need session metadata.
	if metricsOn {
		hooks.OnRegisterSession = []server.OnRegisterSessionHookFunc{
			func(ctx context.Context, session server.ClientSession) {
				o.sessions.Store(session.SessionID(), &sessionMeta{
					startTime: time.Now(),
				})
			},
		}
		hooks.OnUnregisterSession = []server.OnUnregisterSessionHookFunc{
			func(ctx context.Context, session server.ClientSession) {
				sid := session.SessionID()
				if meta, ok := o.sessions.LoadAndDelete(sid); ok {
					sm := meta.(*sessionMeta)
					duration := time.Since(sm.startTime).Seconds()
					var attrs []attribute.KeyValue
					if o.networkTransport != "" {
						attrs = append(attrs, o.sessionDuration.AttrNetworkTransport(o.networkTransport))
					}
					if pv, ok := sm.protocolVersion.Load().(string); ok && pv != "" {
						attrs = append(attrs, o.sessionDuration.AttrProtocolVersion(pv))
					}
					o.sessionDuration.Record(ctx, duration, attrs...)
				}
			},
		}
		hooks.OnAfterInitialize = []server.OnAfterInitializeFunc{
			func(ctx context.Context, id any, message *mcp.InitializeRequest, result *mcp.InitializeResult) {
				if result == nil {
					return
				}
				if session := server.ClientSessionFromContext(ctx); session != nil {
					if meta, ok := o.sessions.Load(session.SessionID()); ok {
						meta.(*sessionMeta).protocolVersion.Store(result.ProtocolVersion)
					}
				}
			},
		}
	}

	return hooks
}

// MergeHooks combines multiple Hooks into one, preserving all hook functions.
func MergeHooks(hooks ...*server.Hooks) *server.Hooks {
	merged := &server.Hooks{}
	for _, h := range hooks {
		if h == nil {
			continue
		}
		merged.OnRegisterSession = append(merged.OnRegisterSession, h.OnRegisterSession...)
		merged.OnUnregisterSession = append(merged.OnUnregisterSession, h.OnUnregisterSession...)
		merged.OnBeforeAny = append(merged.OnBeforeAny, h.OnBeforeAny...)
		merged.OnSuccess = append(merged.OnSuccess, h.OnSuccess...)
		merged.OnError = append(merged.OnError, h.OnError...)
		merged.OnRequestInitialization = append(merged.OnRequestInitialization, h.OnRequestInitialization...)
		merged.OnBeforeInitialize = append(merged.OnBeforeInitialize, h.OnBeforeInitialize...)
		merged.OnAfterInitialize = append(merged.OnAfterInitialize, h.OnAfterInitialize...)
		merged.OnBeforePing = append(merged.OnBeforePing, h.OnBeforePing...)
		merged.OnAfterPing = append(merged.OnAfterPing, h.OnAfterPing...)
		merged.OnBeforeSetLevel = append(merged.OnBeforeSetLevel, h.OnBeforeSetLevel...)
		merged.OnAfterSetLevel = append(merged.OnAfterSetLevel, h.OnAfterSetLevel...)
		merged.OnBeforeListResources = append(merged.OnBeforeListResources, h.OnBeforeListResources...)
		merged.OnAfterListResources = append(merged.OnAfterListResources, h.OnAfterListResources...)
		merged.OnBeforeListResourceTemplates = append(merged.OnBeforeListResourceTemplates, h.OnBeforeListResourceTemplates...)
		merged.OnAfterListResourceTemplates = append(merged.OnAfterListResourceTemplates, h.OnAfterListResourceTemplates...)
		merged.OnBeforeReadResource = append(merged.OnBeforeReadResource, h.OnBeforeReadResource...)
		merged.OnAfterReadResource = append(merged.OnAfterReadResource, h.OnAfterReadResource...)
		merged.OnBeforeListPrompts = append(merged.OnBeforeListPrompts, h.OnBeforeListPrompts...)
		merged.OnAfterListPrompts = append(merged.OnAfterListPrompts, h.OnAfterListPrompts...)
		merged.OnBeforeGetPrompt = append(merged.OnBeforeGetPrompt, h.OnBeforeGetPrompt...)
		merged.OnAfterGetPrompt = append(merged.OnAfterGetPrompt, h.OnAfterGetPrompt...)
		merged.OnBeforeListTools = append(merged.OnBeforeListTools, h.OnBeforeListTools...)
		merged.OnAfterListTools = append(merged.OnAfterListTools, h.OnAfterListTools...)
		merged.OnBeforeCallTool = append(merged.OnBeforeCallTool, h.OnBeforeCallTool...)
		merged.OnAfterCallTool = append(merged.OnAfterCallTool, h.OnAfterCallTool...)
	}
	return merged
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
