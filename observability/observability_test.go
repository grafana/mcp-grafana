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

	"go.opentelemetry.io/otel/semconv/v1.39.0/mcpconv"
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

func (e *typedError) Error() string      { return e.msg }
func (e *typedError) ErrorType() string   { return e.errType }

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
