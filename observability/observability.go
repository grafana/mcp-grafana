// Package observability provides OpenTelemetry-based metrics and tracing
// for the MCP Grafana server.
//
// Metrics follow the OTel MCP semantic conventions using the mcpconv package.
// Tracing is configured via standard OTEL_* environment variables.
package observability

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/semconv/v1.40.0/mcpconv"
)

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
	// (falls back to slog.Default() if Config.Logger was nil).
	slowRequestThreshold time.Duration
	slowRequestLogLevel  slog.Level
	logger               *slog.Logger
}

// Setup initializes the observability providers based on the configuration.
// When metrics are enabled, it creates a Prometheus exporter and registers
// a global MeterProvider. The otelhttp instrumentation will automatically
// use this provider for HTTP metrics.
//
// Tracing configuration is handled via standard OTEL_* environment variables
// (e.g., OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_TRACES_SAMPLER).
func Setup(cfg Config) (*Observability, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	obs := &Observability{
		networkTransport:     cfg.NetworkTransport,
		slowRequestThreshold: cfg.SlowRequestThreshold,
		slowRequestLogLevel:  cfg.SlowRequestLogLevel,
		logger:               logger,
	}

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

	// Set up OTLP trace exporter when OTEL_EXPORTER_OTLP_ENDPOINT is configured.
	// The gRPC exporter respects standard OTEL_* env vars for endpoint, headers,
	// TLS (OTEL_EXPORTER_OTLP_INSECURE), etc.
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
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

// Shutdown gracefully shuts down the observability providers.
func (o *Observability) Shutdown(ctx context.Context) error {
	if o.tracerProvider != nil {
		if err := o.tracerProvider.Shutdown(ctx); err != nil {
			return err
		}
	}
	if o.meterProvider != nil {
		return o.meterProvider.Shutdown(ctx)
	}
	return nil
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
func (o *Observability) buildOperationAttrs(ctx context.Context, method mcp.MCPMethod, message any, err error) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	// gen_ai.tool.name for tools/call method
	if method == "tools/call" {
		if req, ok := message.(*mcp.CallToolRequest); ok && req != nil {
			attrs = append(attrs, o.operationDuration.AttrGenAIToolName(req.Params.Name))
		}
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
// *mcp.CallToolRequest. Returns the empty string for any other method,
// a nil message, or a wrong-type message. Centralises the type assertion
// so the slow-log helper doesn't need to know about mcp-go request types.
func toolNameFromMessage(method mcp.MCPMethod, message any) string {
	if method != "tools/call" {
		return ""
	}
	req, ok := message.(*mcp.CallToolRequest)
	if !ok || req == nil {
		return ""
	}
	return req.Params.Name
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

// MCPHooks returns server.Hooks that record MCP protocol metrics and/or
// emit slow-request logs, depending on configuration. These hooks should
// be merged with any existing hooks using MergeHooks.
//
// The gate truth table (metrics × slow-log):
//   - both disabled → empty hooks (zero-overhead path preserved)
//   - metrics on, slow-log off → full hook set (session + request)
//   - metrics off, slow-log on → request hooks only (no session hooks;
//     operationDuration.Record is NOT called — it would nil-deref the
//     uninitialised instrument)
//   - both on → full hook set, both actions fire inside each hook body
//
// Every operationDuration.Record call is guarded by o.metricsEnabled()
// inside the hook body so that enabling slow-log without metrics stays safe.
func (o *Observability) MCPHooks() *server.Hooks {
	metricsOn := o.metricsEnabled()
	slowLogOn := o.slowRequestThreshold > 0

	if !metricsOn && !slowLogOn {
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
				if o.metricsEnabled() {
					attrs := o.buildOperationAttrs(ctx, method, message, nil)
					o.operationDuration.Record(ctx, duration.Seconds(), mcpconv.MethodNameAttr(method), attrs...)
				}
				o.maybeLogSlowRequest(ctx, method, toolNameFromMessage(method, message), duration, nil)
			},
		},
		OnError: []server.OnErrorHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
				startTime, ok := o.requestStartTimes.LoadAndDelete(id)
				if !ok {
					return
				}
				duration := time.Since(startTime.(time.Time))
				if o.metricsEnabled() {
					attrs := o.buildOperationAttrs(ctx, method, message, err)
					o.operationDuration.Record(ctx, duration.Seconds(), mcpconv.MethodNameAttr(method), attrs...)
				}
				o.maybeLogSlowRequest(ctx, method, toolNameFromMessage(method, message), duration, err)
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
