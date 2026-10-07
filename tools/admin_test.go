//go:build unit
// +build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminToolsUnit(t *testing.T) {
	t.Run("tool definitions", func(t *testing.T) {
		// Test that the tools are properly defined with correct metadata
		require.NotNil(t, ListUsersByOrg, "ListUsersByOrg tool should be defined")
		require.NotNil(t, ListTeams, "ListTeams tool should be defined")
		require.NotNil(t, ListAllRoles, "ListAllRoles tool should be defined")
		require.NotNil(t, GetRoleDetails, "GetRoleDetails tool should be defined")
		require.NotNil(t, GetRoleAssignments, "GetRoleAssignments tool should be defined")
		require.NotNil(t, ListUserRoles, "ListUserRoles tool should be defined")
		require.NotNil(t, GetResourcePermissions, "GetResourcePermissions tool should be defined")
		require.NotNil(t, GetResourceDescription, "GetResourceDescription tool should be defined")
		require.NotNil(t, ListTeamRoles, "ListTeamRoles tool should be defined")

		// Verify tool metadata
		assert.Equal(t, "list_users_by_org", ListUsersByOrg.Tool.Name)
		assert.Equal(t, "list_teams", ListTeams.Tool.Name)
		assert.Equal(t, "list_all_roles", ListAllRoles.Tool.Name)
		assert.Equal(t, "get_role_details", GetRoleDetails.Tool.Name)
		assert.Equal(t, "get_role_assignments", GetRoleAssignments.Tool.Name)
		assert.Equal(t, "list_user_roles", ListUserRoles.Tool.Name)
		assert.Equal(t, "list_team_roles", ListTeamRoles.Tool.Name)
		assert.Equal(t, "get_resource_permissions", GetResourcePermissions.Tool.Name)
		assert.Equal(t, "get_resource_description", GetResourceDescription.Tool.Name)
		assert.Contains(t, ListUsersByOrg.Tool.Description, "List users in the Grafana organization")
		assert.Contains(t, ListTeams.Tool.Description, "Search for Grafana teams by a query string")
		assert.Contains(t, ListAllRoles.Tool.Description, "List all roles in Grafana")
		assert.Contains(t, GetRoleDetails.Tool.Description, "Get detailed information about a specific Grafana role")
		assert.Contains(t, GetRoleAssignments.Tool.Description, "List all assignments for a specific role")
		assert.Contains(t, ListUserRoles.Tool.Description, "List all roles assigned to one or more users")
		assert.Contains(t, ListTeamRoles.Tool.Description, "List all roles assigned to one or more teams")
		assert.Contains(t, GetResourcePermissions.Tool.Description, "List all permissions set on a specific Grafana resource")
		assert.Contains(t, GetResourceDescription.Tool.Description, "List available permissions and assignment capabilities for a Grafana resource type")
	})

	t.Run("parameter structures", func(t *testing.T) {
		// Test parameter types are correctly defined
		userParams := ListUsersByOrgParams{}
		teamParams := ListTeamsParams{Query: "test-query"}
		roleParams := ListAllRolesParams{}
		roleDetailParams := GetRoleDetailsParams{RoleUID: "r1"}
		assignParams := GetRoleAssignmentsParams{RoleUID: "r2"}
		userRoleParams := ListUserRolesParams{UserIDs: []int64{1, 2}}
		teamRoleParams := ListTeamRolesParams{TeamIDs: []int64{3}}
		permParams := GetResourcePermissionsParams{Resource: "dashboards", ResourceID: "abc"}
		descParams := GetResourceDescriptionParams{ResourceType: "folders"}

		// ListUsersByOrgParams has only optional parameters
		assert.IsType(t, ListUsersByOrgParams{}, userParams)

		// ListTeamsParams should have a Query field
		assert.Equal(t, "test-query", teamParams.Query)

		assert.IsType(t, ListAllRolesParams{}, roleParams)
		assert.Equal(t, "r1", roleDetailParams.RoleUID)
		assert.Equal(t, "r2", assignParams.RoleUID)
		assert.Equal(t, []int64{1, 2}, userRoleParams.UserIDs)
		assert.Equal(t, []int64{3}, teamRoleParams.TeamIDs)
		assert.Equal(t, "dashboards", permParams.Resource)
		assert.Equal(t, "abc", permParams.ResourceID)
		assert.Equal(t, "folders", descParams.ResourceType)
	})

	t.Run("nil client handling", func(t *testing.T) {
		// Test that functions handle missing client gracefully
		ctx := context.Background() // No client in context

		// Both functions should return nil when client is not available
		// (they will panic on nil pointer dereference, which is the current behavior)
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
	})

	t.Run("function signatures", func(t *testing.T) {
		// Verify that function signatures follow the expected pattern
		// This test ensures the API migration was done correctly

		// Create context with configuration but no client
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
			URL:    "http://test.grafana.com",
			APIKey: "test-key",
		})

		// Test that both functions can be called with correct parameter types
		// They will fail due to no client, but this validates the signature
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
	})
}

func TestListUsersByOrg_Pagination(t *testing.T) {
	cases := []struct {
		name                     string
		args                     ListUsersByOrgParams
		query, perPage, pageWant string
	}{
		{"defaults", ListUsersByOrgParams{}, "", "100", "1"},
		{"explicit", ListUsersByOrgParams{Query: "alice", Limit: 20, Page: 3}, "alice", "20", "3"},
		{"limit capped", ListUsersByOrgParams{Limit: 5000}, "", "1000", "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/org/users/search", r.URL.Path)
				q := r.URL.Query()
				assert.Equal(t, tc.query, q.Get("query"))
				assert.Equal(t, tc.perPage, q.Get("perpage"))
				assert.Equal(t, tc.pageWant, q.Get("page"))

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"totalCount": 250,
					"page":       3,
					"perPage":    20,
					"orgUsers":   []map[string]any{{"userId": 7, "login": "alice", "role": "Viewer"}},
				})
			}))
			defer server.Close()

			result, err := listUsersByOrg(mockCtxWithClient(server), tc.args)
			require.NoError(t, err)
			assert.Equal(t, int64(250), result.TotalCount)
			assert.Equal(t, int64(3), result.Page)
			assert.Equal(t, int64(20), result.PerPage)
			require.Len(t, result.OrgUsers, 1)
			assert.Equal(t, int64(7), result.OrgUsers[0].UserID)
		})
	}
}
