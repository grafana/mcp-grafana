package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
// A tool that addresses no Grafana organization must not advertise orgId even
// when dynamic multi-org is on: the argument is inert there (the middleware
// strips it and the handler never reads it), and advertising it suggests the
// answer is org-scoped when it is not.
func TestResolveToolNotOrgScoped(t *testing.T) {
	type fooParams struct {
		Foo string `json:"foo,omitempty" jsonschema:"description=a foo"`
	}
	handler := func(_ context.Context, _ fooParams) (string, error) { return "", nil }
	scoped := MustTool("scoped_tool", "demo", handler)
	unscoped := MustTool("unscoped_tool", "demo", handler).NotOrgScoped()

	DynamicMultiOrgEnabled = true
	t.Cleanup(func() { DynamicMultiOrgEnabled = false })

	props := schemaProperties(t, unscoped.resolveTool().RawInputSchema)
	require.Contains(t, props, "foo", "the handler's own arguments are preserved")
	assert.NotContains(t, props, OrgIDArgument)

	// The marker is per-tool, not global.
	assert.Contains(t, schemaProperties(t, scoped.resolveTool().RawInputSchema), OrgIDArgument)
}

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

// user_info's currentOrgId must name the org this connection's calls actually
// reach. /api/user reports the org persisted on the user record and ignores
// GRAFANA_ORG_ID and X-Grafana-Org-Id, so reporting it would tell a client that
// calls target one org while they reach another.
func TestCurrentUserInfoOrgIsRequestScoped(t *testing.T) {
	// persistedOrg is what /api/user reports; requestOrg is what /api/org
	// reports. A zero requestOrg serves 404, standing in for an unavailable
	// endpoint.
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
		// Neither /api/org nor the config names an org, so Grafana applies the
		// identity's own org -- which is what /api/user reports.
		info, err := CurrentUserInfo(server(t, 5, 0))
		require.NoError(t, err)
		assert.Equal(t, int64(5), info.CurrentOrgID)
	})

	t.Run("UserPersistedOrgID still reports the persisted org", func(t *testing.T) {
		// The two must not converge: the deeplink gate needs the browser-session
		// org, which is the persisted one.
		got, err := UserPersistedOrgID(server(t, 1, 2))
		require.NoError(t, err)
		assert.Equal(t, int64(1), got)
	})
}
