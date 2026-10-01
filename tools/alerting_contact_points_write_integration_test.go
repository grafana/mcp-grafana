//go:build integration

package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateContactPointIntegration(t *testing.T) {
	baseCtx := newTestContext()
	ctx, cancel := context.WithTimeout(baseCtx, time.Minute)
	defer cancel()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	AddAlertingTools(srv, true)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	call := func(t *testing.T, tool string, args map[string]any, output any) {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		require.NoError(t, err)
		require.Len(t, result.Content, 1)
		text, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		require.False(t, result.IsError, "%s", text.Text)
		require.NoError(t, json.Unmarshal([]byte(text.Text), output))
	}

	for _, tc := range []struct {
		name        string
		explicitUID bool
		provenance  *bool
	}{
		{name: "generated UID and editable by default"},
		{name: "explicit UID and provisioned", explicitUID: true, provenance: new(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "mcp-contact-point-test-" + uuid.NewString()
			c := mcpgrafana.GrafanaClientFromContext(baseCtx)
			// Register cleanup before creation so even a failed read-back is cleaned up.
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(baseCtx, 30*time.Second)
				defer cancel()
				params := provisioning.NewGetContactpointsParams().WithContext(cleanupCtx).WithName(&name)
				listed, err := c.Provisioning.GetContactpoints(params)
				require.NoError(t, err)
				for _, cp := range listed.Payload {
					require.Equal(t, name, cp.Name)
					_, err := c.Provisioning.DeleteContactpointsWithParams(
						provisioning.NewDeleteContactpointsParams().WithContext(cleanupCtx).WithUID(cp.UID),
					)
					require.NoError(t, err)
				}
				listed, err = c.Provisioning.GetContactpoints(params)
				require.NoError(t, err)
				assert.Empty(t, listed.Payload, "test contact points must be removed")
			})
			args := map[string]any{
				"operation": "create_contact_point", "name": name, "type": "webhook",
				"settings":                map[string]any{"url": "https://example.com/mcp-test"},
				"disable_resolve_message": true,
			}
			if tc.explicitUID {
				args["uid"] = uuid.NewString()
			}
			if tc.provenance != nil {
				args["disable_provenance"] = *tc.provenance
			}
			var created map[string]any
			call(t, "alerting_routing_write", args, &created)
			require.Len(t, created, 3)
			require.NotEmpty(t, created["uid"])
			assert.Equal(t, name, created["name"])
			assert.Equal(t, "webhook", created["type"])
			if tc.explicitUID {
				assert.Equal(t, args["uid"], created["uid"])
			}

			var listed []contactPointSummary
			call(t, "alerting_manage_routing", map[string]any{
				"operation": "get_contact_points", "name": name,
			}, &listed)
			require.Len(t, listed, 1)
			assert.Equal(t, created["uid"], listed[0].UID)

			var details []*models.EmbeddedContactPoint
			call(t, "alerting_manage_routing", map[string]any{
				"operation": "get_contact_point", "contact_point_title": name,
			}, &details)
			require.Len(t, details, 1)
			assert.Equal(t, created["uid"], details[0].UID)
			assert.True(t, details[0].DisableResolveMessage)
			assert.Equal(t, args["settings"], details[0].Settings)
			if tc.provenance == nil || *tc.provenance {
				assert.Empty(t, details[0].Provenance)
			} else {
				assert.EqualValues(t, "api", details[0].Provenance)
			}
		})
	}
}
