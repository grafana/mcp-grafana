//go:build unit
// +build unit

package observability

import (
	"context"
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

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.40.0/mcpconv"
)

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

		// Should return nil handler when metrics disabled
		assert.Nil(t, obs.MetricsHandler())

		// LoggerProvider should be nil when OTLP log export is not configured.
		assert.Nil(t, obs.LoggerProvider())

		// Shutdown should work without error
		err = obs.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("logger provider populated when OTLP logs endpoint set", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://localhost:4317")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE", "true")
		cfg := Config{MetricsEnabled: false}

		obs, err := Setup(cfg)
		require.NoError(t, err)
		require.NotNil(t, obs)
		require.NotNil(t, obs.LoggerProvider())

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

		// Should return a handler when metrics enabled
		assert.NotNil(t, obs.MetricsHandler())

		// Shutdown should work
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

		// MetricsAddress is just stored in config, doesn't affect Setup
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

	// Exercises the realistic production config where both OTLP log export
	// and Prometheus metrics are simultaneously active. Also provides
	// incidental coverage that multi-provider Shutdown succeeds on the
	// happy path when two providers (logger + meter) are live.
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

// errorTraceExporter returns a sentinel error from Shutdown so a test can
// assert that Observability.Shutdown aggregates provider errors rather than
// returning on the first one.
type errorTraceExporter struct{}

func (e *errorTraceExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}
func (e *errorTraceExporter) Shutdown(context.Context) error {
	return errors.New("tracer boom")
}

// errorLogExporter returns a sentinel error from Shutdown for the same
// purpose on the log signal.
type errorLogExporter struct{}

func (e *errorLogExporter) Export(context.Context, []sdklog.Record) error { return nil }
func (e *errorLogExporter) ForceFlush(context.Context) error              { return nil }
func (e *errorLogExporter) Shutdown(context.Context) error                { return errors.New("logger boom") }

// errorMetricExporter returns a sentinel error from Shutdown; wired into a
// PeriodicReader which surfaces the error through MeterProvider.Shutdown.
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

// TestObservability_ShutdownAggregatesErrors verifies that Shutdown returns
// errors from ALL failing providers via errors.Join, rather than short-
// circuiting on the first error. A regression to early-return behaviour
// would cause at least one of the "boom" substrings to be missing.
//
// The Observability struct holds concrete provider pointers with no
// injection seams, so we construct it directly and wire real providers
// configured with exporters whose Shutdown returns sentinel errors.
func TestObservability_ShutdownAggregatesErrors(t *testing.T) {
	// WithSyncer uses SimpleSpanProcessor which propagates the exporter's
	// Shutdown error synchronously.
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

// Note: we do not directly cover Setup's deferred rollback path (Setup fails
// mid-way → already-populated providers get shut down). The failure points
// (setupLogging, prometheus.New, mcpconv.New*) are package-level functions
// with no injection seam; exercising them would require a test-only build
// tag or a package variable. Given the small blast radius and that Shutdown
// itself is well-tested above, the rollback is left uncovered and the
// guardrail is code review.

func TestMetricsHandler(t *testing.T) {
	cfg := Config{
		MetricsEnabled: true,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	handler := obs.MetricsHandler()
	require.NotNil(t, handler)

	// Test that the handler responds to requests
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)

	// Should contain some standard Go metrics
	assert.Contains(t, string(body), "go_")
}

func TestWrapHandler(t *testing.T) {
	// Create a simple test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrapped := WrapHandler(testHandler, "test-operation")
	require.NotNil(t, wrapped)

	// Test that the wrapped handler still works
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OK", rec.Body.String())
}

func TestMCPHooks_MetricsDisabled(t *testing.T) {
	cfg := Config{
		MetricsEnabled: false,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)

	hooks := obs.MCPHooks()
	require.NotNil(t, hooks)

	// Hooks should be empty when metrics disabled
	assert.Empty(t, hooks.OnRegisterSession)
	assert.Empty(t, hooks.OnUnregisterSession)
	assert.Empty(t, hooks.OnAfterInitialize)
	assert.Empty(t, hooks.OnBeforeAny)
	assert.Empty(t, hooks.OnSuccess)
	assert.Empty(t, hooks.OnError)
	assert.Empty(t, hooks.OnBeforeCallTool)
	assert.Empty(t, hooks.OnAfterCallTool)
}

func TestMCPHooks_MetricsEnabled(t *testing.T) {
	cfg := Config{
		MetricsEnabled: true,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	hooks := obs.MCPHooks()
	require.NotNil(t, hooks)

	// Hooks should be populated when metrics enabled
	assert.Len(t, hooks.OnRegisterSession, 1)
	assert.Len(t, hooks.OnUnregisterSession, 1)
	assert.Len(t, hooks.OnAfterInitialize, 1)
	assert.Len(t, hooks.OnBeforeAny, 1)
	assert.Len(t, hooks.OnSuccess, 1)
	assert.Len(t, hooks.OnError, 1)

	// Tool-specific hooks removed (absorbed into operation duration)
	assert.Empty(t, hooks.OnBeforeCallTool)
	assert.Empty(t, hooks.OnAfterCallTool)
}

// mockClientSession implements server.ClientSession for testing
type mockClientSession struct{}

func (m *mockClientSession) SessionID() string                                   { return "test-session" }
func (m *mockClientSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return nil }
func (m *mockClientSession) Initialize()                                         {}
func (m *mockClientSession) Initialized() bool                                   { return true }

func TestMCPHooks_SessionTracking(t *testing.T) {
	cfg := Config{
		MetricsEnabled:   true,
		NetworkTransport: mcpconv.NetworkTransportTCP,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	hooks := obs.MCPHooks()
	ctx := context.Background()
	session := &mockClientSession{}

	// Test session registration stores metadata
	hooks.OnRegisterSession[0](ctx, session)

	meta, ok := obs.sessions.Load("test-session")
	require.True(t, ok)
	sm := meta.(*sessionMeta)
	assert.False(t, sm.startTime.IsZero())

	// Test session unregistration records duration and cleans up
	hooks.OnUnregisterSession[0](ctx, session)

	_, ok = obs.sessions.Load("test-session")
	assert.False(t, ok, "session should be cleaned up after unregister")
}

func TestMCPHooks_SessionDuration(t *testing.T) {
	cfg := Config{
		MetricsEnabled:   true,
		NetworkTransport: mcpconv.NetworkTransportPipe,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	hooks := obs.MCPHooks()
	ctx := context.Background()
	session := &mockClientSession{}

	// Register session
	hooks.OnRegisterSession[0](ctx, session)

	// Simulate OnAfterInitialize to set protocol version
	initResult := &mcp.InitializeResult{
		ProtocolVersion: "2024-11-05",
	}
	// Create context with session using MCPServer.WithContext
	mcpServer := server.NewMCPServer("test", "1.0.0")
	sessionCtx := mcpServer.WithContext(ctx, session)
	hooks.OnAfterInitialize[0](sessionCtx, "init-1", nil, initResult)

	// Verify protocol version was stored
	meta, _ := obs.sessions.Load("test-session")
	sm := meta.(*sessionMeta)
	assert.Equal(t, "2024-11-05", sm.protocolVersion.Load().(string))

	// Small delay to ensure measurable duration
	time.Sleep(1 * time.Millisecond)

	// Unregister session (records session duration)
	hooks.OnUnregisterSession[0](ctx, session)
}

func TestMCPHooks_RequestTracking(t *testing.T) {
	cfg := Config{
		MetricsEnabled: true,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	hooks := obs.MCPHooks()
	ctx := context.Background()

	t.Run("successful request", func(t *testing.T) {
		requestID := "req-1"
		method := mcp.MCPMethod("tools/list")

		// Call OnBeforeAny to store start time
		hooks.OnBeforeAny[0](ctx, requestID, method, nil)

		// Small delay to ensure measurable duration
		time.Sleep(1 * time.Millisecond)

		// Call OnSuccess - should not panic and should clean up start time
		hooks.OnSuccess[0](ctx, requestID, method, nil, nil)
	})

	t.Run("error request", func(t *testing.T) {
		requestID := "req-2"
		method := mcp.MCPMethod("tools/call")

		// Call OnBeforeAny to store start time
		hooks.OnBeforeAny[0](ctx, requestID, method, nil)

		// Small delay
		time.Sleep(1 * time.Millisecond)

		// Call OnError - should not panic
		hooks.OnError[0](ctx, requestID, method, nil, errors.New("test error"))
	})

	t.Run("request without start time", func(t *testing.T) {
		// Calling OnSuccess without OnBeforeAny should not panic
		hooks.OnSuccess[0](ctx, "unknown-id", mcp.MCPMethod("test"), nil, nil)
		hooks.OnError[0](ctx, "unknown-id-2", mcp.MCPMethod("test"), nil, errors.New("error"))
	})
}

func TestMergeHooks(t *testing.T) {
	t.Run("merge nil hooks", func(t *testing.T) {
		merged := MergeHooks(nil, nil)
		require.NotNil(t, merged)
		assert.Empty(t, merged.OnBeforeAny)
	})

	t.Run("merge single hooks", func(t *testing.T) {
		hooks1 := &server.Hooks{
			OnBeforeAny: []server.BeforeAnyHookFunc{
				func(ctx context.Context, id any, method mcp.MCPMethod, message any) {},
			},
		}

		merged := MergeHooks(hooks1)
		require.NotNil(t, merged)
		assert.Len(t, merged.OnBeforeAny, 1)
	})

	t.Run("merge multiple hooks", func(t *testing.T) {
		var called []string

		hooks1 := &server.Hooks{
			OnBeforeAny: []server.BeforeAnyHookFunc{
				func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
					called = append(called, "hook1")
				},
			},
			OnSuccess: []server.OnSuccessHookFunc{
				func(ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {
					called = append(called, "success1")
				},
			},
		}

		hooks2 := &server.Hooks{
			OnBeforeAny: []server.BeforeAnyHookFunc{
				func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
					called = append(called, "hook2")
				},
			},
			OnError: []server.OnErrorHookFunc{
				func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
					called = append(called, "error2")
				},
			},
		}

		merged := MergeHooks(hooks1, hooks2)
		require.NotNil(t, merged)

		// Check merged counts
		assert.Len(t, merged.OnBeforeAny, 2)
		assert.Len(t, merged.OnSuccess, 1)
		assert.Len(t, merged.OnError, 1)

		// Execute hooks to verify order
		ctx := context.Background()
		for _, hook := range merged.OnBeforeAny {
			hook(ctx, nil, "", nil)
		}

		assert.Equal(t, []string{"hook1", "hook2"}, called)
	})

	t.Run("merge with nil in middle", func(t *testing.T) {
		hooks1 := &server.Hooks{
			OnBeforeAny: []server.BeforeAnyHookFunc{
				func(ctx context.Context, id any, method mcp.MCPMethod, message any) {},
			},
		}

		hooks3 := &server.Hooks{
			OnBeforeAny: []server.BeforeAnyHookFunc{
				func(ctx context.Context, id any, method mcp.MCPMethod, message any) {},
			},
		}

		merged := MergeHooks(hooks1, nil, hooks3)
		require.NotNil(t, merged)
		assert.Len(t, merged.OnBeforeAny, 2)
	})

	t.Run("merge all hook types", func(t *testing.T) {
		hooks := &server.Hooks{
			OnRegisterSession:     []server.OnRegisterSessionHookFunc{func(ctx context.Context, session server.ClientSession) {}},
			OnUnregisterSession:   []server.OnUnregisterSessionHookFunc{func(ctx context.Context, session server.ClientSession) {}},
			OnBeforeAny:           []server.BeforeAnyHookFunc{func(ctx context.Context, id any, method mcp.MCPMethod, message any) {}},
			OnSuccess:             []server.OnSuccessHookFunc{func(ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {}},
			OnError:               []server.OnErrorHookFunc{func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {}},
			OnBeforeInitialize:    []server.OnBeforeInitializeFunc{func(ctx context.Context, id any, message *mcp.InitializeRequest) {}},
			OnAfterInitialize:     []server.OnAfterInitializeFunc{func(ctx context.Context, id any, message *mcp.InitializeRequest, result *mcp.InitializeResult) {}},
			OnBeforeCallTool:      []server.OnBeforeCallToolFunc{func(ctx context.Context, id any, message *mcp.CallToolRequest) {}},
			OnAfterCallTool:       []server.OnAfterCallToolFunc{func(ctx context.Context, id any, message *mcp.CallToolRequest, result any) {}},
			OnBeforeListTools:     []server.OnBeforeListToolsFunc{func(ctx context.Context, id any, message *mcp.ListToolsRequest) {}},
			OnAfterListTools:      []server.OnAfterListToolsFunc{func(ctx context.Context, id any, message *mcp.ListToolsRequest, result *mcp.ListToolsResult) {}},
			OnBeforeListResources: []server.OnBeforeListResourcesFunc{func(ctx context.Context, id any, message *mcp.ListResourcesRequest) {}},
			OnAfterListResources: []server.OnAfterListResourcesFunc{func(ctx context.Context, id any, message *mcp.ListResourcesRequest, result *mcp.ListResourcesResult) {
			}},
			OnBeforeListResourceTemplates: []server.OnBeforeListResourceTemplatesFunc{func(ctx context.Context, id any, message *mcp.ListResourceTemplatesRequest) {}},
			OnAfterListResourceTemplates: []server.OnAfterListResourceTemplatesFunc{func(ctx context.Context, id any, message *mcp.ListResourceTemplatesRequest, result *mcp.ListResourceTemplatesResult) {
			}},
			OnBeforeReadResource: []server.OnBeforeReadResourceFunc{func(ctx context.Context, id any, message *mcp.ReadResourceRequest) {}},
			OnAfterReadResource:  []server.OnAfterReadResourceFunc{func(ctx context.Context, id any, message *mcp.ReadResourceRequest, result *mcp.ReadResourceResult) {}},
			OnBeforeListPrompts:  []server.OnBeforeListPromptsFunc{func(ctx context.Context, id any, message *mcp.ListPromptsRequest) {}},
			OnAfterListPrompts:   []server.OnAfterListPromptsFunc{func(ctx context.Context, id any, message *mcp.ListPromptsRequest, result *mcp.ListPromptsResult) {}},
			OnBeforeGetPrompt:    []server.OnBeforeGetPromptFunc{func(ctx context.Context, id any, message *mcp.GetPromptRequest) {}},
			OnAfterGetPrompt:     []server.OnAfterGetPromptFunc{func(ctx context.Context, id any, message *mcp.GetPromptRequest, result *mcp.GetPromptResult) {}},
			OnBeforePing:         []server.OnBeforePingFunc{func(ctx context.Context, id any, message *mcp.PingRequest) {}},
			OnAfterPing:          []server.OnAfterPingFunc{func(ctx context.Context, id any, message *mcp.PingRequest, result *mcp.EmptyResult) {}},
		}

		merged := MergeHooks(hooks, hooks)
		require.NotNil(t, merged)

		// Each hook type should have 2 entries
		assert.Len(t, merged.OnRegisterSession, 2)
		assert.Len(t, merged.OnUnregisterSession, 2)
		assert.Len(t, merged.OnBeforeAny, 2)
		assert.Len(t, merged.OnSuccess, 2)
		assert.Len(t, merged.OnError, 2)
		assert.Len(t, merged.OnBeforeInitialize, 2)
		assert.Len(t, merged.OnAfterInitialize, 2)
		assert.Len(t, merged.OnBeforeCallTool, 2)
		assert.Len(t, merged.OnAfterCallTool, 2)
		assert.Len(t, merged.OnBeforeListTools, 2)
		assert.Len(t, merged.OnAfterListTools, 2)
		assert.Len(t, merged.OnBeforeListResources, 2)
		assert.Len(t, merged.OnAfterListResources, 2)
		assert.Len(t, merged.OnBeforeListResourceTemplates, 2)
		assert.Len(t, merged.OnAfterListResourceTemplates, 2)
		assert.Len(t, merged.OnBeforeReadResource, 2)
		assert.Len(t, merged.OnAfterReadResource, 2)
		assert.Len(t, merged.OnBeforeListPrompts, 2)
		assert.Len(t, merged.OnAfterListPrompts, 2)
		assert.Len(t, merged.OnBeforeGetPrompt, 2)
		assert.Len(t, merged.OnAfterGetPrompt, 2)
		assert.Len(t, merged.OnBeforePing, 2)
		assert.Len(t, merged.OnAfterPing, 2)
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

	// Trigger some metrics by calling hooks
	hooks := obs.MCPHooks()
	ctx := context.Background()

	// Simulate a session lifecycle (register -> unregister to record session duration)
	session := &mockClientSession{}
	hooks.OnRegisterSession[0](ctx, session)
	hooks.OnUnregisterSession[0](ctx, session)

	// Simulate a request
	hooks.OnBeforeAny[0](ctx, "test-id", mcp.MCPMethod("tools/list"), nil)
	hooks.OnSuccess[0](ctx, "test-id", mcp.MCPMethod("tools/list"), nil, nil)

	// Fetch metrics
	handler := obs.MetricsHandler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()

	// Check for semconv MCP metrics
	assert.True(t, strings.Contains(body, "mcp_server_operation_duration"), "should contain mcp_server_operation_duration metric")
	assert.True(t, strings.Contains(body, "mcp_server_session_duration"), "should contain mcp_server_session_duration metric")

	// Check for semconv attribute names
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
		attrs := obs.buildOperationAttrs(ctx, "tools/list", nil, nil)

		// Should have network.transport
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
		req := &mcp.CallToolRequest{}
		req.Params.Name = "search_dashboards"

		attrs := obs.buildOperationAttrs(ctx, "tools/call", req, nil)

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
		attrs := obs.buildOperationAttrs(ctx, "tools/call", nil, testErr)

		found := false
		for _, a := range attrs {
			if string(a.Key) == "error.type" {
				found = true
				assert.Equal(t, "_OTHER", a.Value.AsString())
			}
		}
		assert.True(t, found, "should have error.type attribute when error is present")
	})

	// Locks in the dedup's preservation of pre-existing metric emission. A
	// valid *CallToolRequest with empty Params.Name MUST still emit
	// gen_ai.tool.name="" so external OTel consumers that key off attribute
	// presence (dashboards, alerts using absent() semantics, malformed-request
	// buckets) see the same series identity post-dedup.
	t.Run("tools/call with empty Params.Name still emits gen_ai.tool.name", func(t *testing.T) {
		ctx := context.Background()
		req := &mcp.CallToolRequest{} // zero value: Params.Name == ""

		attrs := obs.buildOperationAttrs(ctx, "tools/call", req, nil)

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

	// Locks in the dedup's preservation of the short-circuit. A
	// non-*CallToolRequest message under tools/call must not emit any
	// tool-name attribute.
	t.Run("tools/call with wrong-type message does NOT emit gen_ai.tool.name", func(t *testing.T) {
		ctx := context.Background()
		attrs := obs.buildOperationAttrs(ctx, "tools/call", "not-a-CallToolRequest", nil)
		for _, a := range attrs {
			assert.NotEqual(t, "gen_ai.tool.name", string(a.Key),
				"wrong-type message must not emit gen_ai.tool.name")
		}
	})

	// Locks in the dedup's preservation of method-gating. A valid request
	// under a non-tools/call method must not emit a tool-name attribute.
	t.Run("non-tools/call method with valid CallToolRequest does NOT emit gen_ai.tool.name", func(t *testing.T) {
		ctx := context.Background()
		req := &mcp.CallToolRequest{}
		req.Params.Name = "query_prometheus"
		attrs := obs.buildOperationAttrs(ctx, "tools/list", req, nil)
		for _, a := range attrs {
			assert.NotEqual(t, "gen_ai.tool.name", string(a.Key),
				"non-tools/call method must not emit gen_ai.tool.name")
		}
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

		// Should still attempt shutdown even with cancelled context
		err = obs.Shutdown(ctx)
		// May or may not error depending on provider implementation
		_ = err
	})
}

// ---------------------------------------------------------------------------
// Slow-request log: test infrastructure + cases (issue #679).
//
// The tests below avoid slog.SetDefault (global-mutation, race-prone under
// concurrent hook execution) by injecting a custom Handler via Config.Logger.
// recordingHandler captures slog.Record values by value and resolves
// LogValuer attrs up front so assertions see final values.
// ---------------------------------------------------------------------------

// recordingHandler captures slog.Record values for inspection.
// Safe for concurrent use via mu.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Rebuild the record with pre-resolved attr values so tests observe
	// final values (e.g., LogValuer-dereferenced values for slog.Any).
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

// findAttr returns the first attr with the given key and whether it was found.
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

// newSlowLogObs constructs an Observability wired to a recordingHandler
// so tests can inspect emitted slog records.
func newSlowLogObs(t *testing.T, cfg Config) (*Observability, *recordingHandler) {
	t.Helper()
	h := &recordingHandler{}
	cfg.Logger = slog.New(h)
	obs, err := Setup(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })
	return obs, h
}

// Test 1.
func TestMaybeLogSlowRequest_Disabled(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: 0,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	obs.maybeLogSlowRequest(context.Background(), "tools/call", "foo", time.Second, nil)

	assert.Empty(t, h.all(), "expected no log records when threshold is 0")
}

// Test 2.
func TestMaybeLogSlowRequest_Below(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: 5 * time.Second,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	obs.maybeLogSlowRequest(context.Background(), "tools/call", "foo", 100*time.Millisecond, nil)

	assert.Empty(t, h.all(), "expected no log records when duration is below threshold")
}

// Test 3.
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

// Test 4.
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

// Test 5.
func TestMCPHooks_SlowRequestOnly(t *testing.T) {
	obs, _ := newSlowLogObs(t, Config{
		MetricsEnabled:       false,
		SlowRequestThreshold: 10 * time.Millisecond,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	hooks := obs.MCPHooks()
	assert.Len(t, hooks.OnBeforeAny, 1, "OnBeforeAny should fire for slow-log only")
	assert.Len(t, hooks.OnSuccess, 1, "OnSuccess should fire for slow-log only")
	assert.Len(t, hooks.OnError, 1, "OnError should fire for slow-log only")
	assert.Empty(t, hooks.OnRegisterSession, "session hooks are metrics-only")
	assert.Empty(t, hooks.OnUnregisterSession, "session hooks are metrics-only")
	assert.Empty(t, hooks.OnAfterInitialize, "session hooks are metrics-only")
}

// Test 7.
func TestMCPHooks_SlowRequestAndMetrics(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		MetricsEnabled:       true,
		SlowRequestThreshold: 1 * time.Nanosecond,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	hooks := obs.MCPHooks()
	assert.Len(t, hooks.OnRegisterSession, 1, "session hooks populated when metrics on")
	assert.Len(t, hooks.OnBeforeAny, 1)
	assert.Len(t, hooks.OnSuccess, 1)
	assert.Len(t, hooks.OnError, 1)

	// Exercise a full request lifecycle and assert slow-log fired.
	ctx := context.Background()
	id := "req-combo-1"
	method := mcp.MCPMethod("tools/list")
	hooks.OnBeforeAny[0](ctx, id, method, nil)
	time.Sleep(2 * time.Millisecond) // ensure duration > threshold
	hooks.OnSuccess[0](ctx, id, method, nil, nil)

	recs := h.all()
	require.Len(t, recs, 1, "expected slow-log to fire when threshold exceeded with both metrics and slow-log on")
	assert.Equal(t, slog.LevelWarn, recs[0].Level)
}

// Test 8. Covers propagation of both slow-request fields from Config to the
// Observability struct, plus the zero-value regression guard described in
// the Config doc-comment (SlowRequestLogLevel zero value is slog.LevelInfo).
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
		// slog.LevelInfo == 0, so unset SlowRequestLogLevel produces INFO,
		// not WARN. Regression-guards against someone silently defaulting to
		// WARN in Setup() — which would make INFO unselectable.
		assert.Equal(t, slog.LevelInfo, obs.slowRequestLogLevel)
	})

	t.Run("nil Logger falls back to slog.Default()", func(t *testing.T) {
		obs, err := Setup(Config{})
		require.NoError(t, err)
		t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })
		assert.NotNil(t, obs.logger)
	})
}

// Test 9.
func TestMaybeLogSlowRequest_NegativeThreshold(t *testing.T) {
	obs, h := newSlowLogObs(t, Config{
		SlowRequestThreshold: -1 * time.Second,
		SlowRequestLogLevel:  slog.LevelWarn,
	})

	obs.maybeLogSlowRequest(context.Background(), "tools/call", "foo", time.Minute, nil)

	assert.Empty(t, h.all(), "negative threshold should silently disable slow-log")
}

// Test 10.
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

// Test 11. The (string, bool) return shape lets callers distinguish "no
// valid tools/call request reached" (ok=false, skip emission) from "valid
// request reached, Name happens to be empty" (ok=true, emit empty value).
// buildOperationAttrs uses the bool to preserve metric-attribute presence
// semantics for zero-value Params.Name.
func TestToolNameFromMessage(t *testing.T) {
	t.Run("tools/call with valid request", func(t *testing.T) {
		req := &mcp.CallToolRequest{}
		req.Params.Name = "query_prometheus"
		name, ok := toolNameFromMessage("tools/call", req)
		assert.Equal(t, "query_prometheus", name)
		assert.True(t, ok)
	})
	t.Run("tools/call with valid request and empty Name", func(t *testing.T) {
		req := &mcp.CallToolRequest{} // zero value: Params.Name == ""
		name, ok := toolNameFromMessage("tools/call", req)
		assert.Equal(t, "", name)
		assert.True(t, ok, "valid *CallToolRequest must report ok=true even with empty Name")
	})
	t.Run("tools/call with nil message", func(t *testing.T) {
		name, ok := toolNameFromMessage("tools/call", nil)
		assert.Equal(t, "", name)
		assert.False(t, ok)
	})
	t.Run("tools/list returns empty and ok=false", func(t *testing.T) {
		name, ok := toolNameFromMessage("tools/list", nil)
		assert.Equal(t, "", name)
		assert.False(t, ok)
	})
	t.Run("tools/call with wrong message type", func(t *testing.T) {
		name, ok := toolNameFromMessage("tools/call", "not-a-CallToolRequest")
		assert.Equal(t, "", name)
		assert.False(t, ok)
	})
	// Regression guard: the method gate must be checked before the type
	// assertion. A future refactor that reversed the order would still pass
	// the "tools/list with nil" subtest, but this subtest (valid
	// CallToolRequest paired with a non-tools/call method) would catch it.
	t.Run("tools/list with valid CallToolRequest still returns empty and ok=false", func(t *testing.T) {
		req := &mcp.CallToolRequest{}
		req.Params.Name = "query_prometheus"
		name, ok := toolNameFromMessage("tools/list", req)
		assert.Equal(t, "", name)
		assert.False(t, ok, "non-tools/call method must report ok=false regardless of message validity")
	})
}

// Test 12. Slow-log only + panic regression: the refactored hook bodies
// must skip operationDuration.Record() when metrics are disabled. Without
// the if-gate inside OnSuccess/OnError, the uninitialised instrument
// would nil-deref.
func TestMCPHooks_NoMetricsNoPanic(t *testing.T) {
	obs, _ := newSlowLogObs(t, Config{
		MetricsEnabled:       false,
		SlowRequestThreshold: 1 * time.Nanosecond,
		SlowRequestLogLevel:  slog.LevelWarn,
	})
	hooks := obs.MCPHooks()

	ctx := context.Background()
	id1 := "req-success"
	id2 := "req-error"
	method := mcp.MCPMethod("tools/list")

	assert.NotPanics(t, func() {
		hooks.OnBeforeAny[0](ctx, id1, method, nil)
		hooks.OnSuccess[0](ctx, id1, method, nil, nil)
	}, "OnSuccess must not panic when metrics disabled + slow-log enabled")

	assert.NotPanics(t, func() {
		hooks.OnBeforeAny[0](ctx, id2, method, nil)
		hooks.OnError[0](ctx, id2, method, nil, errors.New("boom"))
	}, "OnError must not panic when metrics disabled + slow-log enabled")
}

// Test 13.
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

// Test 14.
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

// logValuerError implements slog.LogValuer so tests can distinguish
// slog.Any(err) (which resolves to the LogValue) from slog.String(err.Error())
// (which would serialise the plain Error() string).
type logValuerError struct {
	errText  string
	logValue string
}

func (e *logValuerError) Error() string        { return e.errText }
func (e *logValuerError) LogValue() slog.Value { return slog.StringValue(e.logValue) }
func (e *logValuerError) ErrorType() string    { return "LogValuerError" }

// Test 15. Two orthogonal assertions, each as its own subtest:
//
// (a) API surface: uses slog.Any("error", err), not slog.String("error", err.Error()).
//
//	Verified via a LogValuer sentinel — slog.Any triggers LogValue() resolution.
//
// (b) Bounded attribute presence: error.type is emitted with errorTypeName(err),
//
//	using both a typed error and a plain error to cover both the typed path
//	and the "_OTHER" fallback.
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

		// slog.Any resolves LogValuer, so the attr value should be the
		// LogValue, not the raw Error() text. If code used
		// slog.String("error", err.Error()), we'd see "raw-error-text".
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

// TestNewSlowRequestLogger verifies the helper in isolation: a logger
// constructed at a given slog.Level enables events at or above that level
// and filters events below. Proves the helper itself works before the
// end-to-end test asserts it is wired into Setup correctly.
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

// TestSetup_SlowLogSurvivesStrictGlobal proves the Bugbot regression is
// fixed: even when the process-global slog handler is installed at ERROR
// level (as main.go's --log-level=error would do), a Setup call with
// Config.Logger == nil and SlowRequestLogLevel == WARN MUST produce a
// dedicated logger whose handler admits WARN-level events. Pre-fix, Setup
// assigned slog.Default() and this assertion fails. Post-fix, Setup calls
// newSlowRequestLogger and this assertion passes.
//
// Race safety: this test mutates slog.Default() with t.Cleanup restore.
// It does NOT call t.Parallel() and no other test in this file calls
// t.Parallel() either (verified prior to implementation). Do not add
// t.Parallel() to this test or its neighbors without reworking the
// isolation strategy.
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
