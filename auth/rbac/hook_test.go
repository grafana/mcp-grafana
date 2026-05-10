package rbac

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
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
