package mcpgrafana

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrgIDFromArguments(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want int64
		ok   bool
	}{
		{"absent", map[string]any{}, 0, false},
		{"nil map", nil, 0, false},
		{"json number", map[string]any{"orgId": float64(2)}, 2, true},
		{"numeric string", map[string]any{"orgId": "3"}, 3, true},
		{"int", map[string]any{"orgId": 4}, 4, true},
		{"int64", map[string]any{"orgId": int64(5)}, 5, true},
		{"zero", map[string]any{"orgId": float64(0)}, 0, false},
		{"negative", map[string]any{"orgId": float64(-1)}, 0, false},
		{"empty string", map[string]any{"orgId": ""}, 0, false},
		{"non-numeric string", map[string]any{"orgId": "abc"}, 0, false},
		{"wrong type", map[string]any{"orgId": true}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := orgIDFromArguments(tc.args)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestOrgIDOverrideMiddleware(t *testing.T) {
	// Capture the OrgID the wrapped handler observes in its context.
	var seen int64
	next := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		seen = GrafanaConfigFromContext(ctx).OrgID
		return mcp.NewToolResultText("ok"), nil
	}
	handler := OrgIDOverrideMiddleware(next)

	call := func(ctx context.Context, args map[string]any) {
		seen = 0
		req := mcp.CallToolRequest{}
		req.Params.Name = "some_tool"
		req.Params.Arguments = args
		_, err := handler(ctx, req)
		require.NoError(t, err)
	}

	t.Run("override applies when orgId is provided", func(t *testing.T) {
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{OrgID: 1})
		call(ctx, map[string]any{"orgId": float64(2)})
		assert.Equal(t, int64(2), seen)
	})

	t.Run("connection org is kept when orgId is absent", func(t *testing.T) {
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{OrgID: 7})
		call(ctx, map[string]any{})
		assert.Equal(t, int64(7), seen)
	})

	t.Run("invalid orgId leaves connection org untouched", func(t *testing.T) {
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{OrgID: 7})
		call(ctx, map[string]any{"orgId": float64(0)})
		assert.Equal(t, int64(7), seen)
	})
}
