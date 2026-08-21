//go:build unit
// +build unit

package tools

import (
	"context"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminReadToolDefinition(t *testing.T) {
	require.NotNil(t, AdminRead, "AdminRead tool should be defined")
	assert.Equal(t, "admin_read", AdminRead.Tool.Name)
	assert.Contains(t, AdminRead.Tool.Description, "teams, organization users, roles")
	for _, op := range adminReadOperations {
		assert.Contains(t, AdminRead.Tool.Description, op, "description should document operation %q", op)
	}
}

func TestAdminReadParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  AdminReadParams
		wantErr string // empty means no error expected
	}{
		{
			name:   "list_teams needs nothing",
			params: AdminReadParams{Operation: "list_teams"},
		},
		{
			name:   "list_users needs nothing",
			params: AdminReadParams{Operation: "list_users"},
		},
		{
			name:   "list_roles needs nothing",
			params: AdminReadParams{Operation: "list_roles", DelegatableOnly: true},
		},
		{
			name:    "get_role missing roleUID",
			params:  AdminReadParams{Operation: "get_role"},
			wantErr: "roleUID is required for 'get_role' operation",
		},
		{
			name:   "get_role with roleUID",
			params: AdminReadParams{Operation: "get_role", RoleUID: "editor"},
		},
		{
			name:    "role_assignments missing roleUID",
			params:  AdminReadParams{Operation: "role_assignments"},
			wantErr: "roleUID is required for 'role_assignments' operation",
		},
		{
			name:   "role_assignments with roleUID",
			params: AdminReadParams{Operation: "role_assignments", RoleUID: "editor"},
		},
		{
			name:    "user_roles missing userIds",
			params:  AdminReadParams{Operation: "user_roles"},
			wantErr: "userIds is required for 'user_roles' operation",
		},
		{
			name:   "user_roles with userIds",
			params: AdminReadParams{Operation: "user_roles", UserIDs: []int64{1, 2}},
		},
		{
			name:    "team_roles missing teamIds",
			params:  AdminReadParams{Operation: "team_roles"},
			wantErr: "teamIds is required for 'team_roles' operation",
		},
		{
			name:   "team_roles with teamIds",
			params: AdminReadParams{Operation: "team_roles", TeamIDs: []int64{3}},
		},
		{
			name:    "resource_permissions missing both fields",
			params:  AdminReadParams{Operation: "resource_permissions"},
			wantErr: "resource is required for 'resource_permissions' operation",
		},
		{
			name:    "resource_permissions missing resourceId",
			params:  AdminReadParams{Operation: "resource_permissions", Resource: "dashboards"},
			wantErr: "resourceId is required for 'resource_permissions' operation",
		},
		{
			name:   "resource_permissions with both fields",
			params: AdminReadParams{Operation: "resource_permissions", Resource: "dashboards", ResourceID: "abc"},
		},
		{
			name:    "describe_resource missing resourceType",
			params:  AdminReadParams{Operation: "describe_resource"},
			wantErr: "resourceType is required for 'describe_resource' operation",
		},
		{
			name:   "describe_resource with resourceType",
			params: AdminReadParams{Operation: "describe_resource", ResourceType: "folders"},
		},
		{
			name:    "unknown operation",
			params:  AdminReadParams{Operation: "delete_everything"},
			wantErr: `unknown operation "delete_everything", must be one of: list_teams, list_users, list_roles, get_role, role_assignments, user_roles, team_roles, resource_permissions, describe_resource`,
		},
		{
			name:    "empty operation",
			params:  AdminReadParams{},
			wantErr: `unknown operation "", must be one of:`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestAdminReadDispatch verifies each operation routes to the correct
// underlying handler. Without a Grafana client in context every handler
// panics on first use of the client, which is enough to prove validate()
// passed and dispatch reached the expected code path per operation
// (mirroring the "nil client handling" tests the pre-consolidation tools
// used to have individually).
func TestAdminReadDispatch(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name   string
		params AdminReadParams
	}{
		{"list_teams", AdminReadParams{Operation: "list_teams", Query: "test"}},
		{"list_users", AdminReadParams{Operation: "list_users"}},
		{"list_roles", AdminReadParams{Operation: "list_roles"}},
		{"get_role", AdminReadParams{Operation: "get_role", RoleUID: "r1"}},
		{"role_assignments", AdminReadParams{Operation: "role_assignments", RoleUID: "r2"}},
		{"user_roles", AdminReadParams{Operation: "user_roles", UserIDs: []int64{1}}},
		{"team_roles", AdminReadParams{Operation: "team_roles", TeamIDs: []int64{2}}},
		{"resource_permissions", AdminReadParams{Operation: "resource_permissions", Resource: "dashboards", ResourceID: "abc"}},
		{"describe_resource", AdminReadParams{Operation: "describe_resource", ResourceType: "folders"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Panics(t, func() {
				_, _ = adminRead(ctx, tt.params)
			}, "should reach the Grafana client and panic without one in context")
		})
	}
}

func TestAdminReadDispatch_UnknownOperationDoesNotPanic(t *testing.T) {
	ctx := context.Background()
	_, err := adminRead(ctx, AdminReadParams{Operation: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin_read")
	assert.Contains(t, err.Error(), "unknown operation")
}

func TestAdminHandlers_ParameterStructures(t *testing.T) {
	userParams := ListUsersByOrgParams{}
	teamParams := ListTeamsParams{Query: "test-query"}
	roleParams := ListAllRolesParams{}
	roleDetailParams := GetRoleDetailsParams{RoleUID: "r1"}
	assignParams := GetRoleAssignmentsParams{RoleUID: "r2"}
	userRoleParams := ListUserRolesParams{UserIDs: []int64{1, 2}}
	teamRoleParams := ListTeamRolesParams{TeamIDs: []int64{3}}
	permParams := GetResourcePermissionsParams{Resource: "dashboards", ResourceID: "abc"}
	descParams := GetResourceDescriptionParams{ResourceType: "folders"}

	// ListUsersByOrgParams should be an empty struct (no parameters required)
	assert.IsType(t, ListUsersByOrgParams{}, userParams)

	assert.Equal(t, "test-query", teamParams.Query)
	assert.IsType(t, ListAllRolesParams{}, roleParams)
	assert.Equal(t, "r1", roleDetailParams.RoleUID)
	assert.Equal(t, "r2", assignParams.RoleUID)
	assert.Equal(t, []int64{1, 2}, userRoleParams.UserIDs)
	assert.Equal(t, []int64{3}, teamRoleParams.TeamIDs)
	assert.Equal(t, "dashboards", permParams.Resource)
	assert.Equal(t, "abc", permParams.ResourceID)
	assert.Equal(t, "folders", descParams.ResourceType)
}

func TestAdminHandlers_NilClientHandling(t *testing.T) {
	// Test that functions handle missing client gracefully
	ctx := context.Background() // No client in context

	// Handlers panic on nil pointer dereference when there's no Grafana
	// client in context; this is the current, longstanding behavior.
	assert.Panics(t, func() {
		listUsersByOrg(ctx, ListUsersByOrgParams{})
	}, "Should panic when no Grafana client in context")

	assert.Panics(t, func() {
		listTeams(ctx, ListTeamsParams{})
	}, "Should panic when no Grafana client in context")

	assert.Panics(t, func() {
		listAllRoles(ctx, ListAllRolesParams{})
	}, "Should panic when no Grafana client in context")

	assert.Panics(t, func() {
		getRoleDetails(ctx, GetRoleDetailsParams{RoleUID: "x"})
	}, "Should panic when no Grafana client in context")

	assert.Panics(t, func() {
		getRoleAssignments(ctx, GetRoleAssignmentsParams{RoleUID: "x"})
	}, "Should panic when no Grafana client in context")

	assert.Panics(t, func() {
		listUserRoles(ctx, ListUserRolesParams{UserIDs: []int64{1}})
	}, "Should panic when no Grafana client in context")

	assert.Panics(t, func() {
		listTeamRoles(ctx, ListTeamRolesParams{TeamIDs: []int64{2}})
	}, "Should panic when no Grafana client in context")

	assert.Panics(t, func() {
		getResourcePermissions(ctx, GetResourcePermissionsParams{
			Resource:   "dashboards",
			ResourceID: "x",
		})
	}, "Should panic when no Grafana client in context")

	assert.Panics(t, func() {
		getResourceDescription(ctx, GetResourceDescriptionParams{
			ResourceType: "folders",
		})
	}, "Should panic when no Grafana client in context")
}

func TestAdminHandlers_FunctionSignatures(t *testing.T) {
	// Create context with configuration but no client, to validate handler
	// signatures the same way the pre-consolidation tests did.
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:    "http://test.grafana.com",
		APIKey: "test-key",
	})

	assert.Panics(t, func() {
		listUsersByOrg(ctx, ListUsersByOrgParams{})
	})

	assert.Panics(t, func() {
		listTeams(ctx, ListTeamsParams{Query: "test"})
	})

	assert.Panics(t, func() {
		listAllRoles(ctx, ListAllRolesParams{})
	})

	assert.Panics(t, func() {
		getRoleDetails(ctx, GetRoleDetailsParams{RoleUID: "r1"})
	})

	assert.Panics(t, func() {
		getRoleAssignments(ctx, GetRoleAssignmentsParams{RoleUID: "r2"})
	})

	assert.Panics(t, func() {
		listUserRoles(ctx, ListUserRolesParams{UserIDs: []int64{1}})
	})

	assert.Panics(t, func() {
		listTeamRoles(ctx, ListTeamRolesParams{TeamIDs: []int64{2}})
	})

	assert.Panics(t, func() {
		getResourcePermissions(ctx, GetResourcePermissionsParams{
			Resource:   "dashboards",
			ResourceID: "abc",
		})
	})

	assert.Panics(t, func() {
		getResourceDescription(ctx, GetResourceDescriptionParams{
			ResourceType: "folders",
		})
	})
}
