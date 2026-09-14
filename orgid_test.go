package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inputSchemaBytes extracts the InputSchema from a tool as raw bytes.
func inputSchemaBytes(t *testing.T, tool *mcp.Tool) []byte {
	t.Helper()
	switch v := tool.InputSchema.(type) {
	case json.RawMessage:
		return []byte(v)
	case []byte:
		return v
	default:
		b, err := json.Marshal(v)
		require.NoError(t, err)
		return b
	}
}

// schemaProperties unmarshals an InputSchema and returns its properties.
func schemaProperties(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	raw := inputSchemaBytes(t, tool)
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

func TestResolveToolNotOrgScoped(t *testing.T) {
	type fooParams struct {
		Foo string `json:"foo,omitempty" jsonschema:"description=a foo"`
	}
	handler := func(_ context.Context, _ fooParams) (string, error) { return "", nil }
	scoped := MustTool("scoped_tool", "demo", handler)
	unscoped := MustTool("unscoped_tool", "demo", handler).NotOrgScoped()

	DynamicMultiOrgEnabled = true
	t.Cleanup(func() { DynamicMultiOrgEnabled = false })

	props := schemaProperties(t, unscoped.resolveTool())
	require.Contains(t, props, "foo", "the handler's own arguments are preserved")
	assert.NotContains(t, props, OrgIDArgument)

	assert.Contains(t, schemaProperties(t, scoped.resolveTool()), OrgIDArgument)
}

func TestResolveToolInjectsOrgID(t *testing.T) {
	type fooParams struct {
		Foo string `json:"foo,omitempty" jsonschema:"description=a foo"`
	}
	tool := MustTool("demo_tool", "demo", func(_ context.Context, _ fooParams) (string, error) { return "", nil })

	t.Run("absent when disabled", func(t *testing.T) {
		DynamicMultiOrgEnabled = false
		props := schemaProperties(t, tool.resolveTool())
		require.Contains(t, props, "foo")
		assert.NotContains(t, props, OrgIDArgument, "orgId must not be advertised when dynamic multi-org is off")
	})

	t.Run("injected when enabled", func(t *testing.T) {
		DynamicMultiOrgEnabled = true
		t.Cleanup(func() { DynamicMultiOrgEnabled = false })
		props := schemaProperties(t, tool.resolveTool())
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
		{"fractional json number", map[string]any{"orgId": 2.9}, 0, false},
		{"fractional just below an org", map[string]any{"orgId": 2.0000001}, 0, false},
		{"negative fractional", map[string]any{"orgId": -2.5}, 0, false},
		{"fractional string", map[string]any{"orgId": "2.9"}, 0, false},
		{"whole float is still accepted", map[string]any{"orgId": 2.0}, 2, true},
		{"NaN", map[string]any{"orgId": math.NaN()}, 0, false},
		{"positive infinity", map[string]any{"orgId": math.Inf(1)}, 0, false},
		{"negative infinity", map[string]any{"orgId": math.Inf(-1)}, 0, false},
		{"beyond int64", map[string]any{"orgId": 1e300}, 0, false},
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
	// OrgIDOverrideMiddleware is now an mcp.Middleware. We test it by wrapping
	// a MethodHandler that captures the context's OrgID.
	var seen int64
	inner := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		seen = GrafanaConfigFromContext(ctx).OrgID
		return NewToolResultText("ok"), nil
	}
	handler := OrgIDOverrideMiddleware()(inner)

	call := func(ctx context.Context, args map[string]any) {
		seen = 0
		argBytes, _ := json.Marshal(args)
		req := &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Name:      "some_tool",
				Arguments: argBytes,
			},
		}
		_, err := handler(ctx, "tools/call", req)
		require.NoError(t, err)
	}

	t.Run("override applies and orgId is stripped from args", func(t *testing.T) {
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{OrgID: 1})
		call(ctx, map[string]any{"orgId": float64(2), "other": "keep"})
		assert.Equal(t, int64(2), seen)
	})

	t.Run("connection org is kept when orgId is absent", func(t *testing.T) {
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{OrgID: 7})
		call(ctx, map[string]any{})
		assert.Equal(t, int64(7), seen)
	})

	t.Run("invalid orgId is ignored but still stripped", func(t *testing.T) {
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{OrgID: 7})
		call(ctx, map[string]any{"orgId": float64(0)})
		assert.Equal(t, int64(7), seen)
	})
}

func TestCurrentUserInfoOrgIsRequestScoped(t *testing.T) {
	server := func(t *testing.T, persistedOrg, requestOrg int64) context.Context {
		t.Helper()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/user":
				_, _ = fmt.Fprintf(w, `{"login":"admin","orgId":%d}`, persistedOrg)
			case "/api/org":
				if requestOrg == 0 {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = fmt.Fprintf(w, `{"id":%d}`, requestOrg)
			case "/api/user/orgs":
				_, _ = fmt.Fprintf(w, `[{"orgId":%d},{"orgId":%d}]`, persistedOrg, requestOrg)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		t.Cleanup(ts.Close)
		return WithGrafanaConfig(context.Background(), GrafanaConfig{URL: ts.URL})
	}

	t.Run("request org wins over the persisted org", func(t *testing.T) {
		info, err := CurrentUserInfo(server(t, 1, 2))
		require.NoError(t, err)
		assert.Equal(t, int64(2), info.CurrentOrgID, "must report the org calls reach, not the user's stored org")
		assert.Equal(t, "admin", info.Login)
	})

	t.Run("falls back to the persisted org when no org is resolvable", func(t *testing.T) {
		info, err := CurrentUserInfo(server(t, 5, 0))
		require.NoError(t, err)
		assert.Equal(t, int64(5), info.CurrentOrgID)
	})

	t.Run("UserPersistedOrgID still reports the persisted org", func(t *testing.T) {
		got, err := UserPersistedOrgID(server(t, 1, 2))
		require.NoError(t, err)
		assert.Equal(t, int64(1), got)
	})
}
