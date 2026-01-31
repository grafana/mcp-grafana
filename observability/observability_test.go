//go:build unit
// +build unit

package observability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func TestSetup(t *testing.T) {
	t.Run("metrics disabled", func(t *testing.T) {
		cfg := Config{
			MetricsEnabled: false,
		}

		obs, err := Setup(cfg)
		require.NoError(t, err)
		require.NotNil(t, obs)

		// Should return nil handler when metrics disabled
		assert.Nil(t, obs.MetricsHandler())

		// Shutdown should work without error
		err = obs.Shutdown(context.Background())
		assert.NoError(t, err)
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
	assert.Len(t, hooks.OnBeforeAny, 1)
	assert.Len(t, hooks.OnSuccess, 1)
	assert.Len(t, hooks.OnError, 1)
	assert.Len(t, hooks.OnBeforeCallTool, 1)
	assert.Len(t, hooks.OnAfterCallTool, 1)
}

// mockClientSession implements server.ClientSession for testing
type mockClientSession struct{}

func (m *mockClientSession) SessionID() string                                   { return "test-session" }
func (m *mockClientSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return nil }
func (m *mockClientSession) Initialize()                                         {}
func (m *mockClientSession) Initialized() bool                                   { return true }

func TestMCPHooks_SessionTracking(t *testing.T) {
	cfg := Config{
		MetricsEnabled: true,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	hooks := obs.MCPHooks()
	ctx := context.Background()
	session := &mockClientSession{}

	// Test session registration - should not panic
	hooks.OnRegisterSession[0](ctx, session)

	// Test session unregistration - should not panic
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

		// Verify start time was cleaned up by checking the map is empty
		// (We can't directly access the map, but we can verify no panic on double-call)
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

func TestMCPHooks_ToolCallTracking(t *testing.T) {
	cfg := Config{
		MetricsEnabled: true,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	hooks := obs.MCPHooks()
	ctx := context.Background()

	t.Run("successful tool call", func(t *testing.T) {
		requestID := "tool-req-1"
		toolRequest := &mcp.CallToolRequest{}
		toolRequest.Params.Name = "test_tool"

		// Call OnBeforeCallTool
		hooks.OnBeforeCallTool[0](ctx, requestID, toolRequest)

		// Small delay
		time.Sleep(1 * time.Millisecond)

		// Call OnAfterCallTool with success
		result := &mcp.CallToolResult{IsError: false}
		hooks.OnAfterCallTool[0](ctx, requestID, toolRequest, result)
	})

	t.Run("error tool call", func(t *testing.T) {
		requestID := "tool-req-2"
		toolRequest := &mcp.CallToolRequest{}
		toolRequest.Params.Name = "failing_tool"

		// Call OnBeforeCallTool
		hooks.OnBeforeCallTool[0](ctx, requestID, toolRequest)

		// Call OnAfterCallTool with error
		result := &mcp.CallToolResult{IsError: true}
		hooks.OnAfterCallTool[0](ctx, requestID, toolRequest, result)
	})

	t.Run("tool call with nil message", func(t *testing.T) {
		requestID := "tool-req-3"

		// Call with nil message - should not panic
		hooks.OnBeforeCallTool[0](ctx, requestID, nil)
		hooks.OnAfterCallTool[0](ctx, requestID, nil, &mcp.CallToolResult{})
	})

	t.Run("tool call without start time", func(t *testing.T) {
		// Calling OnAfterCallTool without OnBeforeCallTool should not panic
		hooks.OnAfterCallTool[0](ctx, "unknown-tool-id", &mcp.CallToolRequest{}, &mcp.CallToolResult{})
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
			OnAfterCallTool:       []server.OnAfterCallToolFunc{func(ctx context.Context, id any, message *mcp.CallToolRequest, result *mcp.CallToolResult) {}},
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

func TestNativeHistogramAggregationSelector(t *testing.T) {
	t.Run("histogram instrument returns exponential aggregation", func(t *testing.T) {
		agg := nativeHistogramAggregationSelector(sdkmetric.InstrumentKindHistogram)
		_, ok := agg.(sdkmetric.AggregationBase2ExponentialHistogram)
		assert.True(t, ok, "should return AggregationBase2ExponentialHistogram for histogram instruments")
	})

	t.Run("counter instrument returns default aggregation", func(t *testing.T) {
		agg := nativeHistogramAggregationSelector(sdkmetric.InstrumentKindCounter)
		expected := sdkmetric.DefaultAggregationSelector(sdkmetric.InstrumentKindCounter)
		assert.Equal(t, expected, agg)
	})

	t.Run("gauge instrument returns default aggregation", func(t *testing.T) {
		agg := nativeHistogramAggregationSelector(sdkmetric.InstrumentKindGauge)
		expected := sdkmetric.DefaultAggregationSelector(sdkmetric.InstrumentKindGauge)
		assert.Equal(t, expected, agg)
	})

	t.Run("updown counter instrument returns default aggregation", func(t *testing.T) {
		agg := nativeHistogramAggregationSelector(sdkmetric.InstrumentKindUpDownCounter)
		expected := sdkmetric.DefaultAggregationSelector(sdkmetric.InstrumentKindUpDownCounter)
		assert.Equal(t, expected, agg)
	})
}

func TestMetricsEndpointContent(t *testing.T) {
	cfg := Config{
		MetricsEnabled: true,
	}

	obs, err := Setup(cfg)
	require.NoError(t, err)
	defer obs.Shutdown(context.Background())

	// Trigger some metrics by calling hooks
	hooks := obs.MCPHooks()
	ctx := context.Background()

	// Simulate a session registration (to trigger sessions_active metric)
	session := &mockClientSession{}
	hooks.OnRegisterSession[0](ctx, session)

	// Simulate a request
	hooks.OnBeforeAny[0](ctx, "test-id", mcp.MCPMethod("tools/list"), nil)
	hooks.OnSuccess[0](ctx, "test-id", mcp.MCPMethod("tools/list"), nil, nil)

	// Simulate a tool call
	toolReq := &mcp.CallToolRequest{}
	toolReq.Params.Name = "test_tool"
	hooks.OnBeforeCallTool[0](ctx, "tool-id", toolReq)
	hooks.OnAfterCallTool[0](ctx, "tool-id", toolReq, &mcp.CallToolResult{IsError: false})

	// Fetch metrics
	handler := obs.MetricsHandler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()

	// Check for MCP metrics
	assert.True(t, strings.Contains(body, "mcp_requests"), "should contain mcp_requests metric")
	assert.True(t, strings.Contains(body, "mcp_request_duration_seconds"), "should contain mcp_request_duration_seconds metric")
	assert.True(t, strings.Contains(body, "mcp_tool_calls"), "should contain mcp_tool_calls metric")
	assert.True(t, strings.Contains(body, "mcp_tool_call_duration_seconds"), "should contain mcp_tool_call_duration_seconds metric")
	assert.True(t, strings.Contains(body, "mcp_sessions_active"), "should contain mcp_sessions_active metric")

	// Check for expected labels
	assert.True(t, strings.Contains(body, `method="tools/list"`), "should contain method label")
	assert.True(t, strings.Contains(body, `tool="test_tool"`), "should contain tool label")
}

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
