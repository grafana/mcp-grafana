// Package observability provides OpenTelemetry-based metrics and tracing
// for the MCP Grafana server.
//
// Metrics follow the OTel MCP semantic conventions using the mcpconv package.
// Tracing is configured via standard OTEL_* environment variables.
package observability

import (
	"context"
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
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/semconv/v1.39.0/mcpconv"
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
}

// Setup initializes the observability providers based on the configuration.
// When metrics are enabled, it creates a Prometheus exporter and registers
// a global MeterProvider. The otelhttp instrumentation will automatically
// use this provider for HTTP metrics.
//
// Tracing configuration is handled via standard OTEL_* environment variables
// (e.g., OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_TRACES_SAMPLER).
func Setup(cfg Config) (*Observability, error) {
	obs := &Observability{
		networkTransport: cfg.NetworkTransport,
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

// MCPHooks returns server.Hooks that record MCP protocol metrics.
// These hooks should be merged with any existing hooks using MergeHooks.
func (o *Observability) MCPHooks() *server.Hooks {
	if !o.metricsEnabled() {
		// Metrics not enabled, return empty hooks
		return &server.Hooks{}
	}

	return &server.Hooks{
		OnRegisterSession: []server.OnRegisterSessionHookFunc{
			func(ctx context.Context, session server.ClientSession) {
				o.sessions.Store(session.SessionID(), &sessionMeta{
					startTime: time.Now(),
				})
			},
		},
		OnUnregisterSession: []server.OnUnregisterSessionHookFunc{
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
		},
		OnAfterInitialize: []server.OnAfterInitializeFunc{
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
		},
		OnBeforeAny: []server.BeforeAnyHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
				o.requestStartTimes.Store(id, time.Now())
			},
		},
		OnSuccess: []server.OnSuccessHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {
				if startTime, ok := o.requestStartTimes.LoadAndDelete(id); ok {
					duration := time.Since(startTime.(time.Time)).Seconds()
					attrs := o.buildOperationAttrs(ctx, method, message, nil)
					o.operationDuration.Record(ctx, duration, mcpconv.MethodNameAttr(method), attrs...)
				}
			},
		},
		OnError: []server.OnErrorHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
				if startTime, ok := o.requestStartTimes.LoadAndDelete(id); ok {
					duration := time.Since(startTime.(time.Time)).Seconds()
					attrs := o.buildOperationAttrs(ctx, method, message, err)
					o.operationDuration.Record(ctx, duration, mcpconv.MethodNameAttr(method), attrs...)
				}
			},
		},
	}
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
