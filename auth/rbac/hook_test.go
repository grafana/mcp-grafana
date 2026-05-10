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
