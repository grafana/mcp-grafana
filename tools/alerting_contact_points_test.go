//go:build integration

package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

func TestManageRouting_CreateContactPointRoundTrip(t *testing.T) {
	for _, explicitUID := range []bool{false, true} {
		name := "generated UID"
		if explicitUID {
			name = "explicit UID"
		}
		t.Run(name, func(t *testing.T) {
			ctx := newTestContext()
			name := "mcp-contact-point-" + uuid.NewString()
			args := ManageRoutingReadWriteParams{
				Operation: "create_contact_point",
				Name:      &name,
				Type:      "email",
				Settings:  map[string]any{"addresses": "team@example.com"},
			}
			if explicitUID {
				args.UID = uuid.NewString()
			}
			result, err := manageRoutingReadWrite(ctx, args)
			require.NoError(t, err)
			created := result.(*contactPointSummary)
			require.NotEmpty(t, created.UID)
			t.Cleanup(func() {
				client := mcpgrafana.GrafanaClientFromContext(ctx)
				_, err := client.Provisioning.DeleteContactpointsWithParams(
					provisioning.NewDeleteContactpointsParams().WithContext(context.WithoutCancel(ctx)).WithUID(created.UID),
				)
				require.NoError(t, err)
			})
			if explicitUID {
				require.Equal(t, args.UID, created.UID)
			}
			require.Equal(t, name, created.Name)
			require.NotNil(t, created.Type)
			require.Equal(t, "email", *created.Type)

			listed, err := manageRouting(ctx, ManageRoutingParams{Operation: "get_contact_points", Name: &name})
			require.NoError(t, err)
			require.Equal(t, []contactPointSummary{*created}, listed)
			details, err := getContactPointDetail(ctx, name)
			require.NoError(t, err)
			require.Len(t, details, 1)
			require.Empty(t, details[0].Provenance, "new contact points should be editable in the UI by default")
		})
	}
}
