//go:build unit
// +build unit

package observability

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	datasourceschemas "github.com/grafana/mcp-grafana/tools/datasource_schemas"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/semconv/v1.40.0/mcpconv"
)

// mustMarshalArgs marshals a map to json.RawMessage for test CallToolRequest construction.
func mustMarshalArgs(args map[string]any) json.RawMessage {
	if args == nil {
		return nil
	}
	b, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return b
}

// newTestCallToolRequest creates a *mcp.CallToolRequest with the given name and arguments.
func newTestCallToolRequest(name string, args map[string]any) *mcp.CallToolRequest {
	req := &mcp.CallToolRequest{}
	req.Params = &mcp.CallToolParamsRaw{
		Name:      name,
		Arguments: mustMarshalArgs(args),
	}
	return req
}

// newTestCallToolResult creates a *mcp.CallToolResult with optional meta.
func newTestCallToolResult(meta mcp.Meta) *mcp.CallToolResult {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "{}"}},
	}
	if meta != nil {
		res.Meta = meta
	}
	return res
}

func TestSetup(t *testing.T) {
	t.Run("metrics disabled", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
		cfg := Config{
			MetricsEnabled: false,
		}

		obs, err := Setup(cfg)
		require.NoError(t, err)
		require.NotNil(t, obs)

		assert.Nil(t, obs.MetricsHandler())
		assert.Nil(t, obs.LoggerProvider())

		err = obs.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("traces endpoint enables tracing but not log export", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://localhost:4317")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_INSECURE", "true")
		cfg := Config{MetricsEnabled: false}

		obs, err := Setup(cfg)
		require.NoError(t, err)
		require.NotNil(t, obs)
		assert.NotNil(t, obs.tracerProvider, "tracer provider should be set when traces endpoint is configured")
		assert.Nil(t, obs.LoggerProvider(), "log export must stay off when only the traces endpoint is set")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		assert.NoError(t, obs.Shutdown(shutdownCtx))
	})

	t.Run("generic endpoint enables both tracing and log export", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
		t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
		cfg := Config{MetricsEnabled: false}

		obs, err := Setup(cfg)
		require.NoError(t, err)
		require.NotNil(t, obs)
		assert.NotNil(t, obs.tracerProvider, "generic endpoint should enable trace export")
		assert.NotNil(t, obs.LoggerProvider(), "generic endpoint should enable log export")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		assert.NoError(t, obs.Shutdown(shutdownCtx))
	})

	t.Run("logs endpoint enables log export but not tracing", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://localhost:4317")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE", "true")
		cfg := Config{MetricsEnabled: false}

		obs, err := Setup(cfg)
		require.NoError(t, err)
		require.NotNil(t, obs)
		require.NotNil(t, obs.LoggerProvider())
		assert.Nil(t, obs.tracerProvider, "trace export must stay off when only the logs endpoint is set")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		assert.NoError(t, obs.Shutdown(shutdownCtx))
	})

	t.Run("metrics enabled", func(t *testing.T) {
		cfg := Config{
			MetricsEnabled: true,
		}

		obs, err := Setup(cfg)
		require.NoError(t, err)
		require.NotNil(t, obs)

		assert.NotNil(t, obs.MetricsHandler())

		err = obs.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("metrics address configured", func(t *testing.T) {
		cfg := Config{
			MetricsEnabled: true,
			MetricsAddress: ":9090",
		}

		obs, err := Setup(cfg)
		require.NoError(t, err)
		require.NotNil(t, obs)

		assert.NotNil(t, obs.MetricsHandler())

		err = obs.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("network transport stored from config", func(t *testing.T) {
		cfg := Config{
			MetricsEnabled:   true,
			NetworkTransport: mcpconv.NetworkTransportTCP,
		}

		obs, err := Setup(cfg)
		require.NoError(t, err)
		require.NotNil(t, obs)
		assert.Equal(t, mcpconv.NetworkTransportTCP, obs.networkTransport)

		err = obs.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("combined: OTLP logs endpoint + metrics enabled", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://localhost:4317")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE", "true")
		cfg := Config{MetricsEnabled: true}

		obs, err := Setup(cfg)
		require.NoError(t, err)
		require.NotNil(t, obs)
		require.NotNil(t, obs.MetricsHandler())
		require.NotNil(t, obs.LoggerProvider())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		assert.NoError(t, obs.Shutdown(shutdownCtx))
	})
}

type errorTraceExporter struct{}

func (e *errorTraceExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}
func (e *errorTraceExporter) Shutdown(context.Context) error {
	return errors.New("tracer boom")
}

type errorLogExporter struct{}

func (e *errorLogExporter) Export(context.Context, []sdklog.Record) error { return nil }
func (e *errorLogExporter) ForceFlush(context.Context) error              { return nil }
func (e *errorLogExporter) Shutdown(context.Context) error                { return errors.New("logger boom") }

type errorMetricExporter struct{}

func (e *errorMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}
func (e *errorMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}
func (e *errorMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	return nil
}
func (e *errorMetricExporter) ForceFlush(context.Context) error { return nil }
func (e *errorMetricExporter) Shutdown(context.Context) error   { return errors.New("meter boom") }

func TestObservability_ShutdownAggregatesErrors(t *testing.T) {
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(&errorTraceExporter{}))
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(&errorLogExporter{})))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(&errorMetricExporter{})))

	obs := &Observability{
		tracerProvider: tp,
		loggerProvider: lp,
		meterProvider:  mp,
	}

	err := obs.Shutdown(context.Background())
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "tracer boom", "tracer provider error should be aggregated")
	assert.Contains(t, msg, "logger boom", "logger provider error should be aggregated")
	assert.Contains(t, msg, "meter boom", "meter provider error should be aggregated")
}

func TestMetricsHandler(t *testing.T) {
	cfg := Config{
		MetricsEnabled: true,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	handler := obs.MetricsHandler()
	require.NotNil(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)

	assert.Contains(t, string(body), "go_")
}

func TestWrapHandler(t *testing.T) {
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrapped := WrapHandler(testHandler, "test-operation")
	require.NotNil(t, wrapped)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OK", rec.Body.String())
}

func TestMCPMiddleware_Disabled(t *testing.T) {
	cfg := Config{
		MetricsEnabled: false,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)

	mw := obs.MCPMiddleware()
	require.NotNil(t, mw)

	// When all features are off, the middleware should be identity (pass-through).
	called := false
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		called = true
		return nil, nil
	}
	handler := mw(next)
	_, err = handler(context.Background(), "tools/list", nil)
	require.NoError(t, err)
	assert.True(t, called, "identity middleware should call next")
}

// A tracing-only deployment (metrics off, no slow-log, OTLP tracing on) must
// still register the middleware so span enrichment runs.
func TestMCPMiddleware_TracingOnly(t *testing.T) {
	obs := &Observability{tracerProvider: sdktrace.NewTracerProvider()}
	require.False(t, obs.metricsEnabled())

	mw := obs.MCPMiddleware()
	require.NotNil(t, mw)

	// The middleware should wrap (not identity) to enable span enrichment.
	called := false
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		called = true
		return nil, nil
	}
	handler := mw(next)
	_, err := handler(context.Background(), "tools/call", newTestCallToolRequest("test_tool", nil))
	require.NoError(t, err)
	assert.True(t, called)
}

// End-to-end: with a recording span in context, the middleware attaches the
// tool dimensions — including the high-cardinality target — to that span.
func TestMCPMiddleware_TracingOnlyEnrichesSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	obs := &Observability{tracerProvider: tp}

	mw := obs.MCPMiddleware()
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return nil, nil
	}
	handler := mw(next)

	ctx, span := tp.Tracer("test").Start(context.Background(), "tools/call")
	req := newTestCallToolRequest("update_datasource", map[string]any{"uid": "abc123"})
	_, err := handler(ctx, "tools/call", req)
	require.NoError(t, err)
	span.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	got := map[string]string{}
	for _, a := range ended[0].Attributes() {
		got[string(a.Key)] = a.Value.AsString()
	}
	assert.Equal(t, "abc123", got[attrKeyToolTarget], "target must be attached to the span in a tracing-only deployment")
}

func TestMCPMiddleware_MetricsEnabled(t *testing.T) {
	cfg := Config{
		MetricsEnabled: true,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	mw := obs.MCPMiddleware()
	require.NotNil(t, mw)

	// The middleware should wrap and call next.
	called := false
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		called = true
		return nil, nil
	}
	handler := mw(next)
	_, err = handler(context.Background(), "tools/list", nil)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestMCPMiddleware_RequestTracking(t *testing.T) {
	cfg := Config{
		MetricsEnabled: true,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	mw := obs.MCPMiddleware()

	t.Run("successful request", func(t *testing.T) {
		next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			time.Sleep(1 * time.Millisecond)
			return nil, nil
		}
		handler := mw(next)
		_, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)
	})

	t.Run("error request", func(t *testing.T) {
		next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			time.Sleep(1 * time.Millisecond)
			return nil, errors.New("test error")
		}
		handler := mw(next)
		_, err := handler(context.Background(), "tools/call", newTestCallToolRequest("test", nil))
		require.Error(t, err)
	})
}

func TestMetricsEndpointContent(t *testing.T) {
	cfg := Config{
		MetricsEnabled:   true,
		NetworkTransport: mcpconv.NetworkTransportTCP,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	mw := obs.MCPMiddleware()
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return nil, nil
	}
	handler := mw(next)

	// Simulate a request through the middleware.
	_, err = handler(context.Background(), "tools/list", nil)
	require.NoError(t, err)

	// Fetch metrics
	metricsHandler := obs.MetricsHandler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	metricsHandler.ServeHTTP(rec, req)

	body := rec.Body.String()

	assert.True(t, strings.Contains(body, "mcp_server_operation_duration"), "should contain mcp_server_operation_duration metric")
	assert.True(t, strings.Contains(body, `mcp_method_name="tools/list"`), "should contain mcp.method.name label")
}

func TestBuildOperationAttrs(t *testing.T) {
	cfg := Config{
		MetricsEnabled:   true,
		NetworkTransport: mcpconv.NetworkTransportPipe,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	t.Run("basic method attrs", func(t *testing.T) {
		ctx := context.Background()
		attrs := obs.buildOperationAttrs(ctx, "tools/list", nil, nil, nil)

		found := false
		for _, a := range attrs {
			if string(a.Key) == "network.transport" {
				assert.Equal(t, "pipe", a.Value.AsString())
				found = true
			}
		}
		assert.True(t, found, "should have network.transport attribute")
	})

	t.Run("tools/call includes gen_ai.tool.name", func(t *testing.T) {
		ctx := context.Background()
		req := newTestCallToolRequest("search_dashboards", nil)

		attrs := obs.buildOperationAttrs(ctx, "tools/call", req, nil, nil)

		found := false
		for _, a := range attrs {
			if string(a.Key) == "gen_ai.tool.name" {
				assert.Equal(t, "search_dashboards", a.Value.AsString())
				found = true
			}
		}
		assert.True(t, found, "should have gen_ai.tool.name attribute for tools/call")
	})

	t.Run("error includes error.type", func(t *testing.T) {
		ctx := context.Background()
		testErr := errors.New("something failed")
		attrs := obs.buildOperationAttrs(ctx, "tools/call", nil, nil, testErr)

		found := false
		for _, a := range attrs {
			if string(a.Key) == "error.type" {
				found = true
				assert.Equal(t, "_OTHER", a.Value.AsString())
			}
		}
		assert.True(t, found, "should have error.type attribute when error is present")
	})

	t.Run("tools/call with empty Params.Name still emits gen_ai.tool.name", func(t *testing.T) {
		ctx := context.Background()
		req := newTestCallToolRequest("", nil)

		attrs := obs.buildOperationAttrs(ctx, "tools/call", req, nil, nil)

		var foundEmpty bool
		for _, a := range attrs {
			if string(a.Key) == "gen_ai.tool.name" {
				assert.Equal(t, "", a.Value.AsString(),
					"empty-Name CallToolRequest must emit gen_ai.tool.name with empty value")
				foundEmpty = true
			}
		}
		assert.True(t, foundEmpty,
			"empty-Name CallToolRequest must emit gen_ai.tool.name attribute (preservation)")
	})

	t.Run("tools/call with wrong-type request does NOT emit gen_ai.tool.name", func(t *testing.T) {
		ctx := context.Background()
		// Pass nil (not a *CallToolRequest) — toolNameFromRequest returns false.
		attrs := obs.buildOperationAttrs(ctx, "tools/call", nil, nil, nil)
		for _, a := range attrs {
			assert.NotEqual(t, "gen_ai.tool.name", string(a.Key),
				"nil request must not emit gen_ai.tool.name")
		}
	})

	t.Run("non-tools/call method with valid CallToolRequest does NOT emit gen_ai.tool.name", func(t *testing.T) {
		ctx := context.Background()
		req := newTestCallToolRequest("query_prometheus", nil)
		attrs := obs.buildOperationAttrs(ctx, "tools/list", req, nil, nil)
		for _, a := range attrs {
			assert.NotEqual(t, "gen_ai.tool.name", string(a.Key),
				"non-tools/call method must not emit gen_ai.tool.name")
		}
	})

	t.Run("allowlisted tool emits only its allowed dim, never target", func(t *testing.T) {
		ctx := context.Background()
		req := newTestCallToolRequest("create_datasource", map[string]any{"operation": "create", "type": "prometheus", "name": "prod"})

		attrs := obs.buildOperationAttrs(ctx, "tools/call", req, nil, nil)

		got := map[string]string{}
		for _, a := range attrs {
			got[string(a.Key)] = a.Value.AsString()
		}
		assert.Equal(t, "prometheus", got[attrKeyToolResourceType])
		_, hasOperation := got[attrKeyToolOperation]
		assert.False(t, hasOperation, "create_datasource is not allowlisted for operation; injected operation must be dropped")
		_, hasTarget := got[attrKeyToolTarget]
		assert.False(t, hasTarget, "target is high-cardinality and must not be a metric attribute")
	})

	t.Run("allowlisted operation tool emits operation", func(t *testing.T) {
		ctx := context.Background()
		req := newTestCallToolRequest("alerting_manage_rules", map[string]any{"operation": "list"})

		attrs := obs.buildOperationAttrs(ctx, "tools/call", req, nil, nil)

		got := map[string]string{}
		for _, a := range attrs {
			got[string(a.Key)] = a.Value.AsString()
		}
		assert.Equal(t, "list", got[attrKeyToolOperation])
	})

	t.Run("non-allowlisted tool drops caller-injected arg dims", func(t *testing.T) {
		ctx := context.Background()
		req := newTestCallToolRequest("list_datasources", map[string]any{"operation": "evil", "type": "arbitrary-free-text"})

		attrs := obs.buildOperationAttrs(ctx, "tools/call", req, nil, nil)

		for _, a := range attrs {
			assert.NotEqual(t, attrKeyToolOperation, string(a.Key))
			assert.NotEqual(t, attrKeyToolResourceType, string(a.Key))
		}
	})

	t.Run("tools/call includes phase declared on the result", func(t *testing.T) {
		ctx := context.Background()
		req := newTestCallToolRequest("create_datasource", nil)
		res := newTestCallToolResult(mcp.Meta{ToolPhaseMetaKey: "created"})

		attrs := obs.buildOperationAttrs(ctx, "tools/call", req, res, nil)

		var found bool
		for _, a := range attrs {
			if string(a.Key) == attrKeyToolPhase {
				assert.Equal(t, "created", a.Value.AsString())
				found = true
			}
		}
		assert.True(t, found, "should emit mcp.tool.phase from result meta")
	})

	t.Run("tools/call with nil result omits phase", func(t *testing.T) {
		ctx := context.Background()
		req := newTestCallToolRequest("create_datasource", nil)

		attrs := obs.buildOperationAttrs(ctx, "tools/call", req, nil, nil)

		for _, a := range attrs {
			assert.NotEqual(t, attrKeyToolPhase, string(a.Key))
		}
	})
}

func TestToolPhaseFromResult(t *testing.T) {
	mkResult := func(meta mcp.Meta) *mcp.CallToolResult {
		return newTestCallToolResult(meta)
	}

	tests := []struct {
		name   string
		result any
		want   string
	}{
		{"phase created", mkResult(mcp.Meta{ToolPhaseMetaKey: "created"}), "created"},
		{"phase schema", mkResult(mcp.Meta{ToolPhaseMetaKey: "schema"}), "schema"},
		{"meta without phase key", mkResult(mcp.Meta{"other": "x"}), ""},
		{"non-string phase is ignored", mkResult(mcp.Meta{ToolPhaseMetaKey: 42}), ""},
		{"result without meta", mkResult(nil), ""},
		{"nil result", nil, ""},
		{"wrong-type result", "not-a-CallToolResult", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, toolPhaseFromResult(tt.result))
		})
	}
}

func TestToolMetricDimensions(t *testing.T) {
	mkResult := func(phase string) *mcp.CallToolResult {
		if phase == "" {
			return newTestCallToolResult(nil)
		}
		return newTestCallToolResult(mcp.Meta{ToolPhaseMetaKey: phase})
	}

	t.Run("allowlisted operation tool returns operation only", func(t *testing.T) {
		got := ToolMetricDimensions("alerting_manage_rules",
			map[string]any{"operation": "list", "type": "should-be-ignored"}, nil)
		assert.Equal(t, ToolMetricDims{Operation: "list"}, got)
	})

	t.Run("allowlisted resource_type tool returns resource_type only", func(t *testing.T) {
		got := ToolMetricDimensions("create_datasource",
			map[string]any{"operation": "should-be-ignored", "type": "prometheus"}, nil)
		assert.Equal(t, ToolMetricDims{ResourceType: "prometheus"}, got)
	})

	t.Run("un-allowlisted operation value collapses to other", func(t *testing.T) {
		got := ToolMetricDimensions("alerting_manage_rules",
			map[string]any{"operation": "attacker-chosen-" + strings.Repeat("x", 32)}, nil)
		assert.Equal(t, ToolMetricDims{Operation: metricDimValueOther}, got)
	})

	t.Run("un-allowlisted datasource type collapses to other", func(t *testing.T) {
		got := ToolMetricDimensions("create_datasource",
			map[string]any{"type": "not-a-real-plugin-2f8c"}, nil)
		assert.Equal(t, ToolMetricDims{ResourceType: metricDimValueOther}, got)
	})

	t.Run("absent argument stays empty rather than other", func(t *testing.T) {
		got := ToolMetricDimensions("alerting_manage_rules", map[string]any{}, nil)
		assert.Equal(t, ToolMetricDims{}, got)
	})

	t.Run("every shipped plugin type is an allowed resource_type", func(t *testing.T) {
		for _, pluginType := range datasourceschemas.KnownPluginTypes() {
			got := ToolMetricDimensions("create_datasource", map[string]any{"type": pluginType}, nil)
			assert.Equal(t, pluginType, got.ResourceType)
		}
	})

	t.Run("resource_type cardinality is bounded regardless of input", func(t *testing.T) {
		seen := map[string]struct{}{}
		for _, injected := range []string{"a", "b", "c", "prometheus", "loki", "loki'; DROP", ""} {
			d := ToolMetricDimensions("create_datasource", map[string]any{"type": injected}, nil)
			seen[d.ResourceType] = struct{}{}
		}
		assert.Len(t, seen, 4)
	})

	t.Run("non-allowlisted tool drops caller-injected dims", func(t *testing.T) {
		got := ToolMetricDimensions("list_datasources",
			map[string]any{"operation": "evil", "type": "arbitrary-free-text"}, nil)
		assert.Equal(t, ToolMetricDims{}, got)
	})

	t.Run("allowlisted phase is read from the result", func(t *testing.T) {
		got := ToolMetricDimensions("create_datasource",
			map[string]any{"type": "loki"}, mkResult("created"))
		assert.Equal(t, ToolMetricDims{ResourceType: "loki", Phase: "created"}, got)
	})

	t.Run("nil result yields empty phase", func(t *testing.T) {
		got := ToolMetricDimensions("alerting_manage_rules", map[string]any{"operation": "get"}, nil)
		assert.Equal(t, "", got.Phase)
	})

	t.Run("parity with buildOperationAttrs for an allowlisted tool", func(t *testing.T) {
		obs := &Observability{}
		req := newTestCallToolRequest("create_datasource", map[string]any{"operation": "inject", "type": "tempo", "name": "n"})
		res := mkResult("created")

		attrs := obs.buildOperationAttrs(context.Background(), "tools/call", req, res, nil)
		fromAttrs := map[string]string{}
		for _, a := range attrs {
			switch string(a.Key) {
			case attrKeyToolOperation, attrKeyToolResourceType, attrKeyToolPhase:
				fromAttrs[string(a.Key)] = a.Value.AsString()
			}
		}

		args := toolArgsFromRequest("tools/call", req)
		d := ToolMetricDimensions("create_datasource", args, res)
		facade := map[string]string{}
		if d.Operation != "" {
			facade[attrKeyToolOperation] = d.Operation
		}
		if d.ResourceType != "" {
			facade[attrKeyToolResourceType] = d.ResourceType
		}
		if d.Phase != "" {
			facade[attrKeyToolPhase] = d.Phase
		}
		assert.Equal(t, fromAttrs, facade)
	})
}

func TestToolMetricDimensionsPhaseIsBounded(t *testing.T) {
	mkResult := func(meta mcp.Meta) *mcp.CallToolResult {
		return newTestCallToolResult(meta)
	}

	tests := []struct {
		name     string
		toolName string
		result   any
		want     string
	}{
		{
			name:     "tool absent from the allowlist contributes no phase",
			toolName: "search_tempo_traces",
			result:   mkResult(mcp.Meta{ToolPhaseMetaKey: "attacker-chosen-" + strings.Repeat("x", 32)}),
			want:     "",
		},
		{
			name:     "allowlisted tool that does not opt into phases contributes no phase",
			toolName: "alerting_manage_rules",
			result:   mkResult(mcp.Meta{ToolPhaseMetaKey: "created"}),
			want:     "",
		},
		{
			name:     "opted-in tool passes through an allowed phase (schema)",
			toolName: "create_datasource",
			result:   mkResult(mcp.Meta{ToolPhaseMetaKey: "schema"}),
			want:     "schema",
		},
		{
			name:     "opted-in tool passes through an allowed phase (created)",
			toolName: "create_datasource",
			result:   mkResult(mcp.Meta{ToolPhaseMetaKey: "created"}),
			want:     "created",
		},
		{
			name:     "opted-in tool collapses an unexpected phase to other",
			toolName: "create_datasource",
			result:   mkResult(mcp.Meta{ToolPhaseMetaKey: "not-a-real-phase-7b1e"}),
			want:     metricDimValueOther,
		},
		{
			name:     "meta without a phase key stays empty",
			toolName: "create_datasource",
			result:   mkResult(mcp.Meta{"other": "x"}),
			want:     "",
		},
		{
			name:     "result without meta stays empty",
			toolName: "create_datasource",
			result:   mkResult(nil),
			want:     "",
		},
		{
			name:     "non-string phase stays empty",
			toolName: "create_datasource",
			result:   mkResult(mcp.Meta{ToolPhaseMetaKey: 42}),
			want:     "",
		},
		{
			name:     "nil result stays empty",
			toolName: "create_datasource",
			result:   nil,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolMetricDimensions(tt.toolName, nil, tt.result)
			assert.Equal(t, tt.want, got.Phase)
		})
	}

	t.Run("phase cardinality is bounded regardless of remote input", func(t *testing.T) {
		seen := map[string]struct{}{}
		for _, injected := range []string{"a", "b", "c", "schema", "created", "created'; DROP", ""} {
			d := ToolMetricDimensions("create_datasource", nil,
				mkResult(mcp.Meta{ToolPhaseMetaKey: injected}))
			seen[d.Phase] = struct{}{}
		}
		assert.Len(t, seen, 4)
	})

	t.Run("every phase create_datasource declares is allowlisted", func(t *testing.T) {
		for _, phase := range []string{"schema", "created"} {
			got := ToolMetricDimensions("create_datasource", nil,
				mkResult(mcp.Meta{ToolPhaseMetaKey: phase}))
			assert.Equal(t, phase, got.Phase)
		}
	})

	t.Run("buildOperationAttrs drops an un-allowlisted tool's phase", func(t *testing.T) {
		obs := &Observability{}
		req := newTestCallToolRequest("search_tempo_traces", nil)

		attrs := obs.buildOperationAttrs(context.Background(), "tools/call", req,
			mkResult(mcp.Meta{ToolPhaseMetaKey: "remote-chosen"}), nil)

		for _, a := range attrs {
			assert.NotEqual(t, attrKeyToolPhase, string(a.Key))
		}
	})
}

func TestToolArgDimensionsFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want toolArgDims
	}{
		{
			name: "create: type + name -> resource_type + target(name)",
			args: map[string]any{"type": "prometheus", "name": "prod"},
			want: toolArgDims{resourceType: "prometheus", target: "prod"},
		},
		{
			name: "operation + uid -> operation + target(uid)",
			args: map[string]any{"operation": "update", "uid": "abc"},
			want: toolArgDims{operation: "update", target: "abc"},
		},
		{
			name: "uid preferred over name for target",
			args: map[string]any{"uid": "u1", "name": "n1"},
			want: toolArgDims{target: "u1"},
		},
		{
			name: "non-string arg is ignored",
			args: map[string]any{"type": 42, "name": "prod"},
			want: toolArgDims{target: "prod"},
		},
		{
			name: "nil args -> zero value",
			args: nil,
			want: toolArgDims{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, toolArgDimensionsFromArgs(tt.args))
		})
	}
}

func TestToolArgsFromRequest(t *testing.T) {
	t.Run("tools/call with args", func(t *testing.T) {
		req := newTestCallToolRequest("test", map[string]any{"uid": "abc"})
		args := toolArgsFromRequest("tools/call", req)
		assert.Equal(t, "abc", args["uid"])
	})

	t.Run("non-tools/call returns nil", func(t *testing.T) {
		req := newTestCallToolRequest("test", map[string]any{"uid": "abc"})
		assert.Nil(t, toolArgsFromRequest("tools/list", req))
	})

	t.Run("nil request returns nil", func(t *testing.T) {
		assert.Nil(t, toolArgsFromRequest("tools/call", nil))
	})
}

func TestEnrichSpanWithToolDims(t *testing.T) {
	obs := &Observability{}

	t.Run("sets operation, resource_type, target, and phase on a recording span", func(t *testing.T) {
		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
		ctx, span := tp.Tracer("test").Start(context.Background(), "op")

		req := newTestCallToolRequest("update_datasource", map[string]any{"operation": "update", "type": "loki", "uid": "abc123"})
		res := newTestCallToolResult(mcp.Meta{ToolPhaseMetaKey: "created"})

		obs.enrichSpanWithToolDims(ctx, "tools/call", req, res)
		span.End()

		ended := sr.Ended()
		require.Len(t, ended, 1)
		got := map[string]string{}
		for _, a := range ended[0].Attributes() {
			got[string(a.Key)] = a.Value.AsString()
		}
		assert.Equal(t, "update", got[attrKeyToolOperation])
		assert.Equal(t, "loki", got[attrKeyToolResourceType])
		assert.Equal(t, "abc123", got[attrKeyToolTarget], "target (high-cardinality) belongs on the span")
		assert.Equal(t, "created", got[attrKeyToolPhase])
	})

	t.Run("no-op when span is not recording", func(t *testing.T) {
		req := newTestCallToolRequest("test", map[string]any{"uid": "abc"})
		assert.NotPanics(t, func() {
			obs.enrichSpanWithToolDims(context.Background(), "tools/call", req, nil)
		})
	})
}

func TestErrorTypeName(t *testing.T) {
	t.Run("plain error returns _OTHER", func(t *testing.T) {
		assert.Equal(t, "_OTHER", errorTypeName(errors.New("generic")))
	})

	t.Run("error with ErrorType method", func(t *testing.T) {
		e := &typedError{msg: "bad request", errType: "BadRequest"}
		assert.Equal(t, "BadRequest", errorTypeName(e))
	})
}

type typedError struct {
	msg     string
	errType string
}

func (e *typedError) Error() string     { return e.msg }
func (e *typedError) ErrorType() string { return e.errType }

func TestShutdown(t *testing.T) {
	t.Run("shutdown with metrics enabled", func(t *testing.T) {
		cfg := Config{MetricsEnabled: true}
		obs, err := Setup(cfg)
		require.NoError(t, err)

		err = obs.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("shutdown with metrics disabled", func(t *testing.T) {
		cfg := Config{MetricsEnabled: false}
		obs, err := Setup(cfg)
		require.NoError(t, err)

		err = obs.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("shutdown with cancelled context", func(t *testing.T) {
		cfg := Config{MetricsEnabled: true}
		obs, err := Setup(cfg)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err = obs.Shutdown(ctx)
		_ = err
	})
}

// ---------------------------------------------------------------------------
// Slow-request log: test infrastructure + cases
// ---------------------------------------------------------------------------

type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	resolved := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		a.Value = a.Value.Resolve()
		resolved.AddAttrs(a)
		return true
	})
	h.records = append(h.records, resolved)
	return nil
}

func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *recordingHandler) all() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

func findAttr(r slog.Record, key string) (slog.Value, bool) {
	var found slog.Value
	ok := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = a.Value
			ok = true
			return false
		}
		return true
	})
	return found, ok
}

func newSlowLogObs(t *testing.T, cfg Config) (*Observability, *recordingHandler) {
	t.Helper()
	h := &recordingHandler{}
	cfg.Logger = slog.New(h)
	obs, err := Setup(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })
	return obs, h
}

func TestMaybeLogSlowRequest_Disabled(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: 0,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	obs.maybeLogSlowRequest(context.Background(), "tools/call", "foo", time.Second, nil)

	assert.Empty(t, h.all(), "expected no log records when threshold is 0")
}

func TestMaybeLogSlowRequest_Below(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: 5 * time.Second,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	obs.maybeLogSlowRequest(context.Background(), "tools/call", "foo", 100*time.Millisecond, nil)

	assert.Empty(t, h.all(), "expected no log records when duration is below threshold")
}

func TestMaybeLogSlowRequest_Above_Success(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: 10 * time.Millisecond,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	obs.maybeLogSlowRequest(context.Background(), "tools/call", "search_dashboards", 50*time.Millisecond, nil)

	recs := h.all()
	require.Len(t, recs, 1, "expected exactly one slow-request log record")
	r := recs[0]
	assert.Equal(t, slog.LevelWarn, r.Level)
	assert.Equal(t, "Slow request", r.Message)

	if v, ok := findAttr(r, "mcp.method"); assert.True(t, ok, "mcp.method attr missing") {
		assert.Equal(t, "tools/call", v.String())
	}
	if v, ok := findAttr(r, "duration"); assert.True(t, ok, "duration attr missing") {
		assert.Equal(t, 50*time.Millisecond, v.Duration())
	}
	if v, ok := findAttr(r, "threshold"); assert.True(t, ok, "threshold attr missing") {
		assert.Equal(t, 10*time.Millisecond, v.Duration())
	}
	if v, ok := findAttr(r, "tool"); assert.True(t, ok, "tool attr missing") {
		assert.Equal(t, "search_dashboards", v.String())
	}
	_, hasErr := findAttr(r, "error")
	assert.False(t, hasErr, "error attr should be absent on success path")
}

func TestMaybeLogSlowRequest_Above_Error(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: 10 * time.Millisecond,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	obs.maybeLogSlowRequest(context.Background(), "tools/call", "search_dashboards", 50*time.Millisecond, errors.New("boom"))

	recs := h.all()
	require.Len(t, recs, 1)
	r := recs[0]
	_, hasErr := findAttr(r, "error")
	assert.True(t, hasErr, "error attr should be present on error path")
	if v, ok := findAttr(r, "error.type"); assert.True(t, ok, "error.type attr missing") {
		assert.Equal(t, "_OTHER", v.String())
	}
}

func TestMCPMiddleware_SlowRequestOnly(t *testing.T) {
	obs, _ := newSlowLogObs(t, Config{
		MetricsEnabled:       false,
		SlowRequestThreshold: 10 * time.Millisecond,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	mw := obs.MCPMiddleware()

	// The middleware should wrap (not identity) when slow-log is on.
	called := false
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		called = true
		return nil, nil
	}
	handler := mw(next)
	_, err := handler(context.Background(), "tools/list", nil)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestMCPMiddleware_SlowRequestAndMetrics(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		MetricsEnabled:       true,
		SlowRequestThreshold: 1 * time.Nanosecond,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	mw := obs.MCPMiddleware()
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		time.Sleep(2 * time.Millisecond)
		return nil, nil
	}
	handler := mw(next)

	_, err := handler(context.Background(), "tools/list", nil)
	require.NoError(t, err)

	recs := h.all()
	require.Len(t, recs, 1, "expected slow-log to fire when threshold exceeded with both metrics and slow-log on")
	assert.Equal(t, slog.LevelWarn, recs[0].Level)
}

func TestSetup_SlowRequestFields(t *testing.T) {
	t.Run("propagation", func(t *testing.T) {
		obs, err := Setup(Config{
			SlowRequestThreshold: 750 * time.Millisecond,
			SlowRequestLogLevel:  slog.LevelWarn,
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })
		assert.Equal(t, 750*time.Millisecond, obs.slowRequestThreshold)
		assert.Equal(t, slog.LevelWarn, obs.slowRequestLogLevel)
	})

	t.Run("zero-value SlowRequestLogLevel is LevelInfo (documented gotcha)", func(t *testing.T) {
		obs, err := Setup(Config{SlowRequestThreshold: 500 * time.Millisecond})
		require.NoError(t, err)
		t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })
		assert.Equal(t, slog.LevelInfo, obs.slowRequestLogLevel)
	})

	t.Run("nil Logger falls back to slog.Default()", func(t *testing.T) {
		obs, err := Setup(Config{})
		require.NoError(t, err)
		t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })
		assert.NotNil(t, obs.logger)
	})
}

func TestMaybeLogSlowRequest_NegativeThreshold(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: -1 * time.Second,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	obs.maybeLogSlowRequest(context.Background(), "tools/call", "foo", time.Minute, nil)

	assert.Empty(t, h.all(), "negative threshold should silently disable slow-log")
}

func TestMaybeLogSlowRequest_NoToolName(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: 10 * time.Millisecond,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	obs.maybeLogSlowRequest(context.Background(), "tools/list", "", 50*time.Millisecond, nil)

	recs := h.all()
	require.Len(t, recs, 1)
	_, hasTool := findAttr(recs[0], "tool")
	assert.False(t, hasTool, "tool attr should be absent when toolName is empty")
}

func TestToolNameFromRequest(t *testing.T) {
	t.Run("tools/call with valid request", func(t *testing.T) {
		req := newTestCallToolRequest("query_prometheus", nil)
		name, ok := toolNameFromRequest("tools/call", req)
		assert.Equal(t, "query_prometheus", name)
		assert.True(t, ok)
	})
	t.Run("tools/call with valid request and empty Name", func(t *testing.T) {
		req := newTestCallToolRequest("", nil)
		name, ok := toolNameFromRequest("tools/call", req)
		assert.Equal(t, "", name)
		assert.True(t, ok, "valid *CallToolRequest must report ok=true even with empty Name")
	})
	t.Run("tools/call with nil request", func(t *testing.T) {
		name, ok := toolNameFromRequest("tools/call", nil)
		assert.Equal(t, "", name)
		assert.False(t, ok)
	})
	t.Run("tools/list returns empty and ok=false", func(t *testing.T) {
		name, ok := toolNameFromRequest("tools/list", nil)
		assert.Equal(t, "", name)
		assert.False(t, ok)
	})
	t.Run("tools/list with valid CallToolRequest still returns empty and ok=false", func(t *testing.T) {
		req := newTestCallToolRequest("query_prometheus", nil)
		name, ok := toolNameFromRequest("tools/list", req)
		assert.Equal(t, "", name)
		assert.False(t, ok, "non-tools/call method must report ok=false regardless of request validity")
	})
}

func TestMCPMiddleware_NoMetricsNoPanic(t *testing.T) {
	obs, _ := newSlowLogObs(t, Config{
		MetricsEnabled:       false,
		SlowRequestThreshold: 1 * time.Nanosecond,
		SlowRequestLogLevel:  slog.LevelWarn,
	})
	mw := obs.MCPMiddleware()

	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return nil, nil
	}
	nextErr := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return nil, errors.New("boom")
	}

	handler := mw(next)
	handlerErr := mw(nextErr)

	assert.NotPanics(t, func() {
		_, _ = handler(context.Background(), "tools/list", nil)
	}, "success must not panic when metrics disabled + slow-log enabled")

	assert.NotPanics(t, func() {
		_, _ = handlerErr(context.Background(), "tools/list", nil)
	}, "error must not panic when metrics disabled + slow-log enabled")
}

func TestMaybeLogSlowRequest_NilContext(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: 10 * time.Millisecond,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	//nolint:staticcheck // intentional: verifying nil-ctx defense
	assert.NotPanics(t, func() {
		obs.maybeLogSlowRequest(nil, "tools/list", "", 50*time.Millisecond, nil)
	}, "maybeLogSlowRequest must not panic on nil ctx")

	assert.Len(t, h.all(), 1, "slow-log should still fire with nil ctx coerced to Background")
}

func TestMaybeLogSlowRequest_LogLevelInfo(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: 10 * time.Millisecond,
		SlowRequestLogLevel:  slog.LevelInfo,
	})

	obs.maybeLogSlowRequest(context.Background(), "tools/list", "", 50*time.Millisecond, nil)

	recs := h.all()
	require.Len(t, recs, 1)
	assert.Equal(t, slog.LevelInfo, recs[0].Level, "log level should be INFO when SlowRequestLogLevel is LevelInfo")
}

type logValuerError struct {
	errText  string
	logValue string
}

func (e *logValuerError) Error() string        { return e.errText }
func (e *logValuerError) LogValue() slog.Value { return slog.StringValue(e.logValue) }
func (e *logValuerError) ErrorType() string    { return "LogValuerError" }

func TestMaybeLogSlowRequest_ErrorAttrs(t *testing.T) {
	t.Run("API surface: slog.Any resolves LogValuer", func(t *testing.T) {
		obs, h := newSlowLogObs(t, Config{
			SlowRequestThreshold: 10 * time.Millisecond,
			SlowRequestLogLevel:  slog.LevelWarn,
		})

		sentinel := &logValuerError{errText: "raw-error-text", logValue: "REDACTED_VIA_LOGVALUER"}
		obs.maybeLogSlowRequest(context.Background(), "tools/call", "foo", 50*time.Millisecond, sentinel)

		recs := h.all()
		require.Len(t, recs, 1)

		if v, ok := findAttr(recs[0], "error"); assert.True(t, ok, "error attr missing") {
			assert.Equal(t, "REDACTED_VIA_LOGVALUER", v.String(),
				"error attr should resolve via LogValuer (use slog.Any, not slog.String with err.Error())")
		}
	})

	t.Run("error.type carries typed error's ErrorType", func(t *testing.T) {
		obs, h := newSlowLogObs(t, Config{
			SlowRequestThreshold: 10 * time.Millisecond,
			SlowRequestLogLevel:  slog.LevelWarn,
		})

		sentinel := &logValuerError{errText: "boom", logValue: "x"}
		obs.maybeLogSlowRequest(context.Background(), "tools/call", "foo", 50*time.Millisecond, sentinel)

		recs := h.all()
		require.Len(t, recs, 1)
		if v, ok := findAttr(recs[0], "error.type"); assert.True(t, ok, "error.type attr missing") {
			assert.Equal(t, "LogValuerError", v.String())
		}
	})

	t.Run("error.type falls back to _OTHER for plain errors", func(t *testing.T) {
		obs, h := newSlowLogObs(t, Config{
			SlowRequestThreshold: 10 * time.Millisecond,
			SlowRequestLogLevel:  slog.LevelWarn,
		})

		obs.maybeLogSlowRequest(context.Background(), "tools/call", "foo", 50*time.Millisecond, errors.New("plain"))

		recs := h.all()
		require.Len(t, recs, 1)
		if v, ok := findAttr(recs[0], "error.type"); assert.True(t, ok) {
			assert.Equal(t, "_OTHER", v.String(), "plain errors should yield error.type = _OTHER")
		}
	})
}

func TestNewSlowRequestLogger(t *testing.T) {
	ctx := context.Background()

	t.Run("WARN level enables WARN, filters INFO", func(t *testing.T) {
		logger := newSlowRequestLogger(slog.LevelWarn)
		assert.True(t, logger.Handler().Enabled(ctx, slog.LevelWarn))
		assert.False(t, logger.Handler().Enabled(ctx, slog.LevelInfo))
	})

	t.Run("INFO level enables both INFO and WARN", func(t *testing.T) {
		logger := newSlowRequestLogger(slog.LevelInfo)
		assert.True(t, logger.Handler().Enabled(ctx, slog.LevelInfo))
		assert.True(t, logger.Handler().Enabled(ctx, slog.LevelWarn))
	})
}

func TestSetup_SlowLogSurvivesStrictGlobal(t *testing.T) {
	prevDefault := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(prevDefault)
	})
	strictGlobal := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))
	slog.SetDefault(strictGlobal)

	obs, err := Setup(Config{
		SlowRequestThreshold: 1 * time.Millisecond,
		SlowRequestLogLevel:  slog.LevelWarn,
		Logger:               nil,
	})
	require.NoError(t, err)
	require.NotNil(t, obs)

	ctx := context.Background()
	assert.True(t, obs.logger.Handler().Enabled(ctx, slog.LevelWarn),
		"obs.logger must admit WARN events even when slog.Default() is at ERROR")
	assert.False(t, strictGlobal.Handler().Enabled(ctx, slog.LevelWarn),
		"sanity check: the installed global handler must reject WARN")
}
