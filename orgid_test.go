package mcpgrafana

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaProperties unmarshals a RawInputSchema and returns its properties.
func schemaProperties(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var schema map[string]any
	require.NoError(t, json.Unmarshal(raw, &schema))
	props, _ := schema["properties"].(map[string]any)
	return props
}

func TestInjectOrgIDProperty(t *testing.T) {
	t.Run("adds an integer orgId to a property set", func(t *testing.T) {
		props := map[string]any{"foo": &jsonschema.Schema{Type: "string"}}
		injectOrgIDProperty(props)
		require.Contains(t, props, "foo", "existing properties must be preserved")
		schema, ok := props[OrgIDArgument].(*jsonschema.Schema)
		require.True(t, ok)
		assert.Equal(t, "integer", schema.Type)
		assert.Equal(t, orgIDArgumentDescription, schema.Description)
	})

	t.Run("does not overwrite a property set that already declares orgId", func(t *testing.T) {
		existing := &jsonschema.Schema{Type: "integer", Description: "custom org arg"}
		props := map[string]any{OrgIDArgument: existing}
		injectOrgIDProperty(props)
		assert.Same(t, existing, props[OrgIDArgument], "a tool's own orgId definition must win")
	})
}

// resolveTool injects orgId into a registered tool's schema only when dynamic
// multi-org is enabled.
func TestResolveToolInjectsOrgID(t *testing.T) {
	type fooParams struct {
		Foo string `json:"foo,omitempty" jsonschema:"description=a foo"`
	}
	tool := MustTool("demo_tool", "demo", func(_ context.Context, _ fooParams) (string, error) { return "", nil })

	t.Run("absent when disabled", func(t *testing.T) {
		DynamicMultiOrgEnabled = false
		props := schemaProperties(t, tool.resolveTool().RawInputSchema)
		require.Contains(t, props, "foo")
		assert.NotContains(t, props, OrgIDArgument, "orgId must not be advertised when dynamic multi-org is off")
	})

	t.Run("injected when enabled", func(t *testing.T) {
		DynamicMultiOrgEnabled = true
		t.Cleanup(func() { DynamicMultiOrgEnabled = false })
		props := schemaProperties(t, tool.resolveTool().RawInputSchema)
		require.Contains(t, props, "foo", "the handler's own arguments are preserved")
		orgID, ok := props[OrgIDArgument].(map[string]any)
		require.True(t, ok, "orgId should be advertised when enabled")
		assert.Equal(t, "integer", orgID["type"])
	})
}

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

	t.Run("override applies and orgId is stripped from args", func(t *testing.T) {
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{OrgID: 1})
		args := map[string]any{"orgId": float64(2), "other": "keep"}
		call(ctx, args)
		assert.Equal(t, int64(2), seen)
		assert.NotContains(t, args, OrgIDArgument, "orgId must be stripped so it never reaches the handler / proxied upstream")
		assert.Contains(t, args, "other", "other arguments are preserved")
	})

	t.Run("connection org is kept when orgId is absent", func(t *testing.T) {
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{OrgID: 7})
		call(ctx, map[string]any{})
		assert.Equal(t, int64(7), seen)
	})

	t.Run("invalid orgId is ignored but still stripped", func(t *testing.T) {
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{OrgID: 7})
		args := map[string]any{"orgId": float64(0)}
		call(ctx, args)
		assert.Equal(t, int64(7), seen)
		assert.NotContains(t, args, OrgIDArgument, "even an invalid orgId must not propagate downstream")
	})
}
