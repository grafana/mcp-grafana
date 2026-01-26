// Package observability provides OpenTelemetry-based metrics and tracing
// for the MCP Grafana server.
//
// Metrics are exposed via a Prometheus endpoint at /metrics when enabled.
// Tracing is configured via standard OTEL_* environment variables.
package observability

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Config holds configuration for observability features.
type Config struct {
	// MetricsEnabled enables Prometheus metrics at /metrics.
	MetricsEnabled bool

	// MetricsAddress is an optional separate address for the metrics server.
	// If empty, metrics are served on the main server.
	MetricsAddress string
}

// Observability manages the OpenTelemetry providers and Prometheus handler.
type Observability struct {
	meterProvider *sdkmetric.MeterProvider
	promHandler   http.Handler

	// MCP protocol metrics
	mcpRequestsTotal    metric.Int64Counter
	mcpRequestDuration  metric.Float64Histogram
	mcpToolCallsTotal   metric.Int64Counter
	mcpToolCallDuration metric.Float64Histogram
	mcpSessionsActive   metric.Int64UpDownCounter

	// Track request start times for duration calculation
	requestStartTimes sync.Map // map[any]time.Time keyed by request ID
	toolStartTimes    sync.Map // map[any]time.Time keyed by request ID for tool calls
}

// nativeHistogramAggregationSelector returns an AggregationSelector that uses
// native (exponential) histograms for all histogram instruments.
func nativeHistogramAggregationSelector(ik sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	switch ik {
	case sdkmetric.InstrumentKindHistogram:
		return sdkmetric.AggregationBase2ExponentialHistogram{
			MaxSize:  160,
			MaxScale: 20,
		}
	default:
		return sdkmetric.DefaultAggregationSelector(ik)
	}
}

// Setup initializes the observability providers based on the configuration.
// When metrics are enabled, it creates a Prometheus exporter and registers
// a global MeterProvider. The otelhttp instrumentation will automatically
// use this provider for HTTP metrics.
//
// Tracing configuration is handled via standard OTEL_* environment variables
// (e.g., OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_TRACES_SAMPLER).
func Setup(cfg Config) (*Observability, error) {
	obs := &Observability{}

	if !cfg.MetricsEnabled {
		return obs, nil
	}

	// Create Prometheus exporter with native histogram support
	exporter, err := prometheus.New(
		prometheus.WithAggregationSelector(nativeHistogramAggregationSelector),
	)
	if err != nil {
		return nil, err
	}

	// Create MeterProvider with the Prometheus exporter
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))

	// Register as global MeterProvider so otelhttp instrumentation uses it
	otel.SetMeterProvider(provider)

	obs.meterProvider = provider
	// Use HandlerFor with EnableOpenMetrics to properly expose native histograms
	obs.promHandler = promhttp.HandlerFor(
		promclient.DefaultGatherer,
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	)

	// Create MCP protocol metrics
	meter := provider.Meter("mcp-grafana")

	obs.mcpRequestsTotal, err = meter.Int64Counter("mcp_requests_total",
		metric.WithDescription("Total number of MCP requests"),
		metric.WithUnit("{request}"))
	if err != nil {
		return nil, err
	}

	obs.mcpRequestDuration, err = meter.Float64Histogram("mcp_request_duration_seconds",
		metric.WithDescription("Duration of MCP requests in seconds"),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}

	obs.mcpToolCallsTotal, err = meter.Int64Counter("mcp_tool_calls_total",
		metric.WithDescription("Total number of MCP tool calls"),
		metric.WithUnit("{call}"))
	if err != nil {
		return nil, err
	}

	obs.mcpToolCallDuration, err = meter.Float64Histogram("mcp_tool_call_duration_seconds",
		metric.WithDescription("Duration of MCP tool calls in seconds"),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}

	obs.mcpSessionsActive, err = meter.Int64UpDownCounter("mcp_sessions_active",
		metric.WithDescription("Number of active MCP sessions"),
		metric.WithUnit("{session}"))
	if err != nil {
		return nil, err
	}

	return obs, nil
}

// Shutdown gracefully shuts down the observability providers.
func (o *Observability) Shutdown(ctx context.Context) error {
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

// MCPHooks returns server.Hooks that record MCP protocol metrics.
// These hooks should be merged with any existing hooks using MergeHooks.
func (o *Observability) MCPHooks() *server.Hooks {
	if o.mcpRequestsTotal == nil {
		// Metrics not enabled, return empty hooks
		return &server.Hooks{}
	}

	return &server.Hooks{
		OnRegisterSession: []server.OnRegisterSessionHookFunc{
			func(ctx context.Context, session server.ClientSession) {
				o.mcpSessionsActive.Add(ctx, 1)
			},
		},
		OnUnregisterSession: []server.OnUnregisterSessionHookFunc{
			func(ctx context.Context, session server.ClientSession) {
				o.mcpSessionsActive.Add(ctx, -1)
			},
		},
		OnBeforeAny: []server.BeforeAnyHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
				// Store start time for duration calculation
				o.requestStartTimes.Store(id, time.Now())
			},
		},
		OnSuccess: []server.OnSuccessHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {
				attrs := metric.WithAttributes(
					attribute.String("method", string(method)),
					attribute.String("status", "success"),
				)
				o.mcpRequestsTotal.Add(ctx, 1, attrs)

				if startTime, ok := o.requestStartTimes.LoadAndDelete(id); ok {
					duration := time.Since(startTime.(time.Time)).Seconds()
					o.mcpRequestDuration.Record(ctx, duration, metric.WithAttributes(
						attribute.String("method", string(method)),
					))
				}
			},
		},
		OnError: []server.OnErrorHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
				attrs := metric.WithAttributes(
					attribute.String("method", string(method)),
					attribute.String("status", "error"),
				)
				o.mcpRequestsTotal.Add(ctx, 1, attrs)

				if startTime, ok := o.requestStartTimes.LoadAndDelete(id); ok {
					duration := time.Since(startTime.(time.Time)).Seconds()
					o.mcpRequestDuration.Record(ctx, duration, metric.WithAttributes(
						attribute.String("method", string(method)),
					))
				}
			},
		},
		OnBeforeCallTool: []server.OnBeforeCallToolFunc{
			func(ctx context.Context, id any, message *mcp.CallToolRequest) {
				// Store start time for tool duration calculation
				o.toolStartTimes.Store(id, time.Now())
			},
		},
		OnAfterCallTool: []server.OnAfterCallToolFunc{
			func(ctx context.Context, id any, message *mcp.CallToolRequest, result *mcp.CallToolResult) {
				toolName := ""
				if message != nil {
					toolName = message.Params.Name
				}
				isError := result != nil && result.IsError

				o.mcpToolCallsTotal.Add(ctx, 1, metric.WithAttributes(
					attribute.String("tool", toolName),
					attribute.Bool("error", isError),
				))

				// Record tool-specific duration
				if startTime, ok := o.toolStartTimes.LoadAndDelete(id); ok {
					duration := time.Since(startTime.(time.Time)).Seconds()
					o.mcpToolCallDuration.Record(ctx, duration, metric.WithAttributes(
						attribute.String("tool", toolName),
						attribute.Bool("error", isError),
					))
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
