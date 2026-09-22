//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callRoutingTool(t *testing.T, ctx context.Context, write bool, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	srv := server.NewMCPServer("test", "0")
	AddAlertingTools(srv, write)
	tool := srv.GetTool("alerting_manage_routing")
	require.NotNil(t, tool)
	result, err := tool.Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "alerting_manage_routing", Arguments: args,
	}})
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func TestManageRouting_CreateContactPoint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provenance any
		wantHeader string
	}{
		{name: "editable by default", wantHeader: "true"},
		{name: "explicitly editable", provenance: true, wantHeader: "true"},
		{name: "API provisioned", provenance: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received map[string]any
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/api/v1/provisioning/contact-points", r.URL.Path)
				assert.Equal(t, tc.wantHeader, r.Header.Get("X-Disable-Provenance"))
				if err := json.NewDecoder(r.Body).Decode(&received); !assert.NoError(t, err) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"uid":"team-webhook","name":"Team webhook","type":"webhook","settings":{"url":"https://example.com/hook","password":"test-secret"},"disableResolveMessage":true}`))
			}))
			defer api.Close()

			args := map[string]any{
				"operation": "create_contact_point", "name": "Team webhook", "type": "webhook",
				"uid": "team-webhook", "disable_resolve_message": true,
				"settings": map[string]any{"url": "https://example.com/hook", "password": "test-secret"},
			}
			if tc.provenance != nil {
				args["disable_provenance"] = tc.provenance
			}
			result := callRoutingTool(t, mockCtxWithClient(api), true, args)
			require.False(t, result.IsError, "%+v", result.Content)
			assert.Equal(t, map[string]any{
				"uid": "team-webhook", "name": "Team webhook", "type": "webhook",
				"settings":              map[string]any{"url": "https://example.com/hook", "password": "test-secret"},
				"disableResolveMessage": true,
			}, received)
			require.Len(t, result.Content, 1)
			content := result.Content[0].(mcp.TextContent)
			assert.JSONEq(t, `{"uid":"team-webhook","name":"Team webhook","type":"webhook"}`, content.Text)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "test-secret")
		})
	}
}

func TestManageRouting_CreateContactPointRejectsInvalidInput(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("invalid input reached Grafana: %s %s", r.Method, r.URL.Path)
	}))
	defer api.Close()
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"missing name", "name", nil},
		{"blank name", "name", "  "},
		{"missing type", "type", nil},
		{"blank type", "type", "  "},
		{"missing settings", "settings", nil},
		{"empty settings", "settings", map[string]any{}},
		{"non-object settings", "settings", "not an object"},
		{"array settings", "settings", []string{"email"}},
		{"external Alertmanager", "datasource_uid", "external-am"},
		{"invalid UID", "uid", "invalid/uid"},
		{"long UID", "uid", strings.Repeat("x", 41)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{
				"operation": "create_contact_point", "name": "Team email", "type": "email",
				"settings": map[string]any{"addresses": "team@example.com"},
			}
			args[tc.field] = tc.value
			result := callRoutingTool(t, mockCtxWithClient(api), true, args)
			require.True(t, result.IsError)
			assert.Contains(t, result.Content[0].(mcp.TextContent).Text, tc.field)
		})
	}
}

func TestManageRouting_ContactPointWriteGate(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method, "read-only mode must never send a write")
		assert.Equal(t, "/api/v1/provisioning/contact-points", r.URL.Path)
		assert.Equal(t, "Team email", r.URL.Query().Get("name"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"uid":"team-email","name":"Team email","type":"email","settings":{}}]`))
	}))
	defer api.Close()
	for _, write := range []bool{false, true} {
		srv := server.NewMCPServer("test", "0")
		AddAlertingTools(srv, write)
		tool := srv.GetTool("alerting_manage_routing").Tool
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(tool.RawInputSchema, &schema))
		var operation struct {
			Enum []string `json:"enum"`
		}
		require.NoError(t, json.Unmarshal(schema.Properties["operation"], &operation))
		if write {
			assert.Contains(t, operation.Enum, "create_contact_point")
			assert.Contains(t, schema.Properties, "settings")
		} else {
			assert.NotContains(t, operation.Enum, "create_contact_point")
			assert.NotContains(t, schema.Properties, "settings")
		}
		require.NotNil(t, tool.Annotations.ReadOnlyHint)
		assert.Equal(t, !write, *tool.Annotations.ReadOnlyHint)
		if write {
			assert.False(t, tool.Annotations.IdempotentHint != nil && *tool.Annotations.IdempotentHint)
		}
		result := callRoutingTool(t, mockCtxWithClient(api), write, map[string]any{
			"operation": "get_contact_points", "name": "Team email", "limit": 1,
		})
		require.False(t, result.IsError)
		assert.JSONEq(t, `[{"uid":"team-email","name":"Team email","type":"email"}]`, result.Content[0].(mcp.TextContent).Text)
	}
	result := callRoutingTool(t, mockCtxWithClient(api), false, map[string]any{
		"operation": "create_contact_point", "name": "Team email", "type": "email",
		"settings": map[string]any{"addresses": "team@example.com"},
	})
	require.True(t, result.IsError)
}

func TestManageRouting_CreateContactPointAPIErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"contact point creation failed"}`))
			}))
			defer api.Close()
			result := callRoutingTool(t, mockCtxWithClient(api), true, map[string]any{
				"operation": "create_contact_point", "name": "Team email", "type": "email",
				"settings": map[string]any{"addresses": "team@example.com"},
			})
			require.True(t, result.IsError)
			message := result.Content[0].(mcp.TextContent).Text
			assert.Contains(t, message, "create contact point:")
			assert.Contains(t, message, strconv.Itoa(status))
		})
	}
}

func TestManageRouting_CreateContactPointCancellation(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("canceled request reached Grafana")
	}))
	defer api.Close()
	ctx, cancel := context.WithCancel(mockCtxWithClient(api))
	cancel()
	result := callRoutingTool(t, ctx, true, map[string]any{
		"operation": "create_contact_point", "name": "Team email", "type": "email",
		"settings": map[string]any{"addresses": "team@example.com"},
	})
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "context canceled")
}
