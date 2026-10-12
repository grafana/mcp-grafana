//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func routingTestSession(t *testing.T, ctx context.Context, enableWriteTools bool) *mcp.ClientSession {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	AddAlertingTools(srv, enableWriteTools)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestContactPointWriteRegistration(t *testing.T) {
	var readSchema []byte
	for _, enableWrite := range []bool{false, true} {
		t.Run(strconv.FormatBool(enableWrite), func(t *testing.T) {
			session := routingTestSession(t, t.Context(), enableWrite)
			listed, err := session.ListTools(t.Context(), nil)
			require.NoError(t, err)
			registered := map[string]*mcp.Tool{}
			for _, tool := range listed.Tools {
				registered[tool.Name] = tool
			}
			require.Contains(t, registered, "alerting_manage_routing")
			read := registered["alerting_manage_routing"]
			assert.True(t, read.Annotations.ReadOnlyHint)
			schema, err := json.Marshal(read.InputSchema)
			require.NoError(t, err)
			assert.NotContains(t, string(schema), "create_contact_point")
			if !enableWrite {
				readSchema = schema
				assert.NotContains(t, registered, "alerting_routing_write")
				_, err := session.CallTool(t.Context(), &mcp.CallToolParams{
					Name: "alerting_routing_write", Arguments: map[string]any{"operation": "create_contact_point"},
				})
				require.Error(t, err)
				return
			}
			assert.JSONEq(t, string(readSchema), string(schema))
			require.Contains(t, registered, "alerting_routing_write")
			write := registered["alerting_routing_write"]
			assert.False(t, write.Annotations.ReadOnlyHint)
			assert.False(t, write.Annotations.IdempotentHint)
			require.NotNil(t, write.Annotations.DestructiveHint)
			assert.False(t, *write.Annotations.DestructiveHint)
			schema, err = json.Marshal(write.InputSchema)
			require.NoError(t, err)
			assert.Contains(t, string(schema), "create_contact_point")
			assert.NotContains(t, string(schema), "get_contact_points")
		})
	}
}

func TestCreateContactPoint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provenance *bool
		wantHeader string
		uid        string
	}{
		{name: "editable by default", wantHeader: "true"},
		{name: "explicitly editable", provenance: new(true), wantHeader: "true", uid: "team_email-1"},
		{name: "provisioned", provenance: new(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/api/v1/provisioning/contact-points", r.URL.Path)
				assert.Equal(t, tc.wantHeader, r.Header.Get("X-Disable-Provenance"))
				var body map[string]any
				if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				assert.Equal(t, "Team", body["name"])
				assert.Equal(t, "webhook", body["type"])
				assert.Equal(t, true, body["disableResolveMessage"])
				assert.Equal(t, map[string]any{"url": "https://example.com/hook", "password": "secret"}, body["settings"])
				if tc.uid != "" {
					assert.Equal(t, tc.uid, body["uid"])
				} else {
					assert.Empty(t, body["uid"])
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"uid":"created-id","name":"Team","type":"webhook","settings":{"password":"secret"}}`))
			}))
			defer server.Close()
			session := routingTestSession(t, mockCtxWithClient(server), true)
			args := map[string]any{
				"operation": "create_contact_point", "name": "Team", "type": "webhook",
				"settings":                map[string]any{"url": "https://example.com/hook", "password": "secret"},
				"disable_resolve_message": true,
			}
			if tc.uid != "" {
				args["uid"] = tc.uid
			}
			if tc.provenance != nil {
				args["disable_provenance"] = *tc.provenance
			}
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "alerting_routing_write", Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError, "%v", result.Content)
			require.Len(t, result.Content, 1)
			content, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			assert.JSONEq(t, `{"uid":"created-id","name":"Team","type":"webhook"}`, content.Text)
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestCreateContactPointInvalidInput(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	session := routingTestSession(t, mockCtxWithClient(server), true)
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"missing operation", "operation", nil},
		{"unsupported operation", "operation", "delete_contact_point"},
		{"read operation", "operation", "get_contact_points"},
		{"missing name", "name", nil},
		{"blank name", "name", "  "},
		{"missing type", "type", nil},
		{"blank type", "type", "\t"},
		{"missing settings", "settings", nil},
		{"empty settings", "settings", map[string]any{}},
		{"array settings", "settings", []string{"email"}},
		{"string settings", "settings", "email"},
		{"invalid uid", "uid", "invalid/uid"},
		{"long uid", "uid", strings.Repeat("a", 41)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{
				"operation": "create_contact_point", "name": "Team", "type": "email",
				"settings": map[string]any{"addresses": "team@example.com"},
			}
			if tc.value == nil {
				delete(args, tc.field)
			} else {
				args[tc.field] = tc.value
			}
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "alerting_routing_write", Arguments: args})
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.NotEmpty(t, result.Content)
			assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, tc.field)
		})
	}
	assert.Zero(t, calls.Load())
}

func TestCreateContactPointAPIErrors(t *testing.T) {
	for _, status := range []int{400, 403, 409, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"contact point rejected"}`))
			}))
			defer server.Close()
			session := routingTestSession(t, mockCtxWithClient(server), true)
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
				Name: "alerting_routing_write", Arguments: map[string]any{
					"operation": "create_contact_point", "name": "Team", "type": "email",
					"settings": map[string]any{"addresses": "team@example.com"},
				},
			})
			require.NoError(t, err)
			require.True(t, result.IsError)
			assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, strconv.Itoa(status))
		})
	}
}

func TestCreateContactPointCancellation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(mockCtxWithClient(server))
	cancel()
	_, err := manageRoutingWrite(ctx, ManageRoutingWriteParams{
		Operation: "create_contact_point", Name: "Team", Type: "email",
		Settings: map[string]any{"addresses": "team@example.com"},
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls.Load())
}
