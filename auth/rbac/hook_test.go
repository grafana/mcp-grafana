package rbac

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type fakeFetcher struct {
	snap Snapshot
	err  error
}

func (f fakeFetcher) Fetch(ctx context.Context, key string) (Snapshot, error) {
	return f.snap, f.err
}

func TestEngine_Hook_Filters(t *testing.T) {
	e := NewEngine(EngineConfig{
		Mode: ModeEnterprise,
		Cache: NewCache(0, func(ctx context.Context, key string) (Snapshot, error) {
			return Snapshot{Permissions: PermissionSet{"datasources:read": {"datasources:*"}}}, nil
		}),
		Gate: NewGate(map[string]ToolGate{
			"datasources_read":  {Permissions: []Permission{{"datasources:read", "datasources:*"}}},
			"datasources_write": {Permissions: []Permission{{"datasources:write", "datasources:*"}}},
		}),
		KeyFromContext: func(ctx context.Context) (string, bool) {
			if v, ok := ctx.Value(testKey{}).(string); ok {
				return v, true
			}
			return "", false
		},
	})

	hook := e.HookOnAfterListTools()
	result := &mcp.ListToolsResult{
		Tools: []mcp.Tool{
			{Name: "datasources_read"},
			{Name: "datasources_write"},
		},
	}
	ctx := context.WithValue(context.Background(), testKey{}, "session-1")
	hook(ctx, "id-1", &mcp.ListToolsRequest{}, result)

	if len(result.Tools) != 1 || result.Tools[0].Name != "datasources_read" {
		t.Errorf("unexpected tools: %+v", result.Tools)
	}
}

func TestEngine_Hook_NoSession_PassThrough(t *testing.T) {
	e := NewEngine(EngineConfig{
		Mode:           ModeEnterprise,
		Cache:          NewCache(0, func(ctx context.Context, key string) (Snapshot, error) { return Snapshot{}, nil }),
		Gate:           NewGate(map[string]ToolGate{"x": {Permissions: []Permission{{"a", ""}}}}),
		KeyFromContext: func(ctx context.Context) (string, bool) { return "", false },
	})
	hook := e.HookOnAfterListTools()
	r := &mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "x"}, {Name: "y"}}}
	hook(context.Background(), "id", &mcp.ListToolsRequest{}, r)
	if len(r.Tools) != 2 {
		t.Errorf("no session should pass through unchanged, got %v", r.Tools)
	}
}

func TestEngine_Hook_FetchError_FailsOpen(t *testing.T) {
	e := NewEngine(EngineConfig{
		Mode: ModeEnterprise,
		Cache: NewCache(0, func(ctx context.Context, key string) (Snapshot, error) {
			return Snapshot{}, ErrFetchFailed
		}),
		Gate:           NewGate(map[string]ToolGate{"x": {Permissions: []Permission{{"a", ""}}}}),
		KeyFromContext: func(ctx context.Context) (string, bool) { return "session", true },
	})
	hook := e.HookOnAfterListTools()
	r := &mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "x"}, {Name: "y"}}}
	hook(context.Background(), "id", &mcp.ListToolsRequest{}, r)
	if len(r.Tools) != 2 {
		t.Errorf("fetch error should fail open, got %v", r.Tools)
	}
}

type testKey struct{}

// TestEngine_Hook_AutoModeMetricUsesResolvedMode confirms that when the
// engine starts in ModeAuto and resolves to ModeEnterprise on first
// permission fetch, the filter-duration histogram is recorded with
// mode="enterprise" (not "auto"). Closes a regression where Stopwatch
// captured the pre-resolution value.
func TestEngine_Hook_AutoModeMetricUsesResolvedMode(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	original := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(original) })

	e := NewEngine(EngineConfig{
		Mode: ModeAuto, // not yet resolved
		Cache: NewCache(0, func(ctx context.Context, key string) (Snapshot, error) {
			// Non-empty perms → ResolveAuto picks enterprise.
			return Snapshot{Permissions: PermissionSet{"datasources:read": {"datasources:*"}}}, nil
		}),
		Gate:           NewGate(map[string]ToolGate{"x": {Permissions: []Permission{{"datasources:read", "datasources:*"}}}}),
		KeyFromContext: func(ctx context.Context) (string, bool) { return "session", true },
		Metrics:        NewMetrics(),
	})

	r := &mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "x"}, {Name: "y"}}}
	e.HookOnAfterListTools()(context.Background(), "id", &mcp.ListToolsRequest{}, r)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	mode := readHistogramMode(t, &rm, "mcp_auth_rbac_filter_duration_seconds")
	if mode != string(ModeEnterprise) {
		t.Errorf("filter-duration metric recorded under mode=%q, want %q", mode, ModeEnterprise)
	}
}

// readHistogramMode returns the value of the "mode" attribute on the first
// data point of the named histogram. Fails the test if the metric or
// attribute isn't present.
func readHistogramMode(t *testing.T, rm *metricdata.ResourceMetrics, name string) string {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			h, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("metric %s has unexpected data type %T", name, m.Data)
			}
			if len(h.DataPoints) == 0 {
				t.Fatalf("metric %s has no data points", name)
			}
			v, _ := h.DataPoints[0].Attributes.Value("mode")
			return v.AsString()
		}
	}
	t.Fatalf("metric %s not found", name)
	return ""
}

// TestEngine_Hook_FetchError_DoesNotRecordFilterDuration confirms that the
// filter-duration metric is NOT recorded on the fail-open path. The fetch
// failed, no filter ran — recording a duration would conflate failed
// network call timings with actual filter timings on dashboards.
func TestEngine_Hook_FetchError_DoesNotRecordFilterDuration(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	original := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(original) })

	e := NewEngine(EngineConfig{
		Mode: ModeEnterprise,
		Cache: NewCache(0, func(_ context.Context, _ string) (Snapshot, error) {
			return Snapshot{}, ErrFetchFailed
		}),
		Gate:           NewGate(map[string]ToolGate{"x": {Permissions: []Permission{{"a", ""}}}}),
		KeyFromContext: func(_ context.Context) (string, bool) { return "session", true },
		Metrics:        NewMetrics(),
	})
	r := &mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "x"}}}
	e.HookOnAfterListTools()(context.Background(), "id", &mcp.ListToolsRequest{}, r)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "mcp_auth_rbac_filter_duration_seconds" {
				h := m.Data.(metricdata.Histogram[float64])
				if len(h.DataPoints) > 0 {
					t.Errorf("filter-duration metric was recorded on fail-open path: %d data points", len(h.DataPoints))
				}
			}
		}
	}
}

// TestEngine_ToolMiddleware_DeniesUngrantedTool exercises the call-time
// gate: a session that lacks the required permission gets a permission-
// denied result instead of the underlying handler being invoked.
func TestEngine_ToolMiddleware_DeniesUngrantedTool(t *testing.T) {
	e := NewEngine(EngineConfig{
		Mode: ModeEnterprise,
		Cache: NewCache(0, func(ctx context.Context, key string) (Snapshot, error) {
			return Snapshot{Permissions: PermissionSet{"datasources:read": {"datasources:*"}}}, nil
		}),
		Gate: NewGate(map[string]ToolGate{
			"datasources_write": {Permissions: []Permission{{"datasources:write", "datasources:*"}}},
		}),
		KeyFromContext: func(ctx context.Context) (string, bool) {
			if v, ok := ctx.Value(testKey{}).(string); ok {
				return v, true
			}
			return "", false
		},
	})

	called := false
	next := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return &mcp.CallToolResult{}, nil
	}
	wrapped := e.ToolMiddleware()(next)

	req := mcp.CallToolRequest{}
	req.Params.Name = "datasources_write"
	ctx := context.WithValue(context.Background(), testKey{}, "session-1")

	res, err := wrapped(ctx, req)
	if err != nil {
		t.Fatalf("middleware should not return a transport error on deny: %v", err)
	}
	if called {
		t.Errorf("downstream handler must not run when permission is denied")
	}
	if res == nil || !res.IsError {
		t.Errorf("expected permission-denied tool result, got %+v", res)
	}
}

// TestEngine_ToolMiddleware_AllowsGrantedTool — the inverse: a session
// that has the required permission flows through to the handler.
func TestEngine_ToolMiddleware_AllowsGrantedTool(t *testing.T) {
	e := NewEngine(EngineConfig{
		Mode: ModeEnterprise,
		Cache: NewCache(0, func(ctx context.Context, key string) (Snapshot, error) {
			return Snapshot{Permissions: PermissionSet{"datasources:read": {"datasources:*"}}}, nil
		}),
		Gate: NewGate(map[string]ToolGate{
			"datasources_read": {Permissions: []Permission{{"datasources:read", "datasources:*"}}},
		}),
		KeyFromContext: func(ctx context.Context) (string, bool) {
			if v, ok := ctx.Value(testKey{}).(string); ok {
				return v, true
			}
			return "", false
		},
	})

	called := false
	next := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return &mcp.CallToolResult{}, nil
	}
	wrapped := e.ToolMiddleware()(next)

	req := mcp.CallToolRequest{}
	req.Params.Name = "datasources_read"
	ctx := context.WithValue(context.Background(), testKey{}, "session-1")

	if _, err := wrapped(ctx, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Errorf("handler should run when permission is granted")
	}
}

// TestEngine_ToolMiddleware_NoSessionPassThrough — legacy / no-auth callers
// (no session key on context) flow through without a permission check.
func TestEngine_ToolMiddleware_NoSessionPassThrough(t *testing.T) {
	e := NewEngine(EngineConfig{
		Mode:           ModeEnterprise,
		Cache:          NewCache(0, func(ctx context.Context, key string) (Snapshot, error) { return Snapshot{}, nil }),
		Gate:           NewGate(map[string]ToolGate{"x": {Permissions: []Permission{{"a", ""}}}}),
		KeyFromContext: func(ctx context.Context) (string, bool) { return "", false },
	})

	called := false
	wrapped := e.ToolMiddleware()(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return &mcp.CallToolResult{}, nil
	})
	req := mcp.CallToolRequest{}
	req.Params.Name = "x"
	if _, err := wrapped(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Errorf("no-session call should pass through to handler")
	}
}

// TestEngine_ToolMiddleware_FetchError_FailsOpen — an RBAC fetch failure
// shouldn't lock everyone out of every tool.
func TestEngine_ToolMiddleware_FetchError_FailsOpen(t *testing.T) {
	e := NewEngine(EngineConfig{
		Mode: ModeEnterprise,
		Cache: NewCache(0, func(ctx context.Context, key string) (Snapshot, error) {
			return Snapshot{}, ErrFetchFailed
		}),
		Gate:           NewGate(map[string]ToolGate{"x": {Permissions: []Permission{{"a", ""}}}}),
		KeyFromContext: func(ctx context.Context) (string, bool) { return "s", true },
	})
	called := false
	wrapped := e.ToolMiddleware()(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return &mcp.CallToolResult{}, nil
	})
	req := mcp.CallToolRequest{}
	req.Params.Name = "x"
	if _, err := wrapped(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Errorf("fetch error should fail open at call time")
	}
}
