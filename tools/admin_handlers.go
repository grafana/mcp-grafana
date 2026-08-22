package tools

import (
	"context"
	"fmt"

	"github.com/grafana/grafana-openapi-client-go/client/access_control"
	"github.com/grafana/grafana-openapi-client-go/client/org"
	"github.com/grafana/grafana-openapi-client-go/client/teams"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

func adminRead(ctx context.Context, args AdminReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("admin_read: %w", err)
	}

	switch args.Operation {
	case "list_teams":
		return listTeams(ctx, ListTeamsParams{Query: args.Query})
	case "list_users":
		return listUsersByOrg(ctx, ListUsersByOrgParams{})
	case "list_roles":
		return listAllRoles(ctx, ListAllRolesParams{DelegatableOnly: args.DelegatableOnly})
	case "get_role":
		return getRoleDetails(ctx, GetRoleDetailsParams{RoleUID: args.RoleUID})
	case "role_assignments":
		return getRoleAssignments(ctx, GetRoleAssignmentsParams{RoleUID: args.RoleUID})
	case "user_roles":
		return listUserRoles(ctx, ListUserRolesParams{UserIDs: args.UserIDs})
	case "team_roles":
		return listTeamRoles(ctx, ListTeamRolesParams{TeamIDs: args.TeamIDs})
	case "resource_permissions":
		return getResourcePermissions(ctx, GetResourcePermissionsParams{Resource: args.Resource, ResourceID: args.ResourceID})
	case "describe_resource":
		return getResourceDescription(ctx, GetResourceDescriptionParams{ResourceType: args.ResourceType})
	default:
		return nil, fmt.Errorf("admin_read: unknown operation %q", args.Operation)
	}
}

type ListTeamsParams struct {
	Query string `json:"query" jsonschema:"description=The query to search for teams. Can be left empty to fetch all teams"`
}

func listTeams(ctx context.Context, args ListTeamsParams) (*models.SearchTeamQueryResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := teams.NewSearchTeamsParamsWithContext(ctx)
	if args.Query != "" {
		params.SetQuery(&args.Query)
	}
	search, err := c.Teams.SearchTeams(params)
	if err != nil {
		return nil, fmt.Errorf("search teams for %+v: %w", c, err)
	}
	return search.Payload, nil
}

type ListUsersByOrgParams struct{}

func listUsersByOrg(ctx context.Context, args ListUsersByOrgParams) ([]*models.OrgUserDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	params := org.NewGetOrgUsersForCurrentOrgParamsWithContext(ctx)
	search, err := c.Org.GetOrgUsersForCurrentOrg(params)
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}
	return search.Payload, nil
}

type ListAllRolesParams struct {
	DelegatableOnly bool `json:"delegatableOnly,omitempty" jsonschema:"description=Optional: If set true only return roles that can be delegated by current user"`
}

func listAllRoles(ctx context.Context, args ListAllRolesParams) ([]*models.RoleDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := access_control.NewListRolesParamsWithContext(ctx)

	if args.DelegatableOnly {
		delegatable := true
		params.Delegatable = &delegatable
	}

	resp, err := c.AccessControl.ListRoles(params)
	if err != nil {
		return nil, fmt.Errorf("list all roles: %w", err)
	}
	return resp.Payload, nil
}

type GetRoleDetailsParams struct {
	RoleUID string `json:"roleUID" jsonschema:"required,description=Role UID to retrieve"`
}

func getRoleDetails(ctx context.Context, args GetRoleDetailsParams) (*models.RoleDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := access_control.NewGetRoleParamsWithContext(ctx).WithRoleUID(args.RoleUID)

	resp, err := c.AccessControl.GetRoleWithParams(params)
	if err != nil {
		return nil, fmt.Errorf("get role details: %w", err)
	}
	return resp.Payload, nil
}

type GetRoleAssignmentsParams struct {
	RoleUID string `json:"roleUID" jsonschema:"required,description=Role UID to retrieve"`
}

func getRoleAssignments(ctx context.Context, args GetRoleAssignmentsParams) (*models.RoleAssignmentsDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := access_control.NewGetRoleAssignmentsParamsWithContext(ctx).WithRoleUID(args.RoleUID)

	resp, err := c.AccessControl.GetRoleAssignmentsWithParams(params)
	if err != nil {
		return nil, fmt.Errorf("get role assignments: %w", err)
	}
	return resp.Payload, nil
}

type ListUserRolesParams struct {
	UserIDs []int64 `json:"userIds" jsonschema:"required,description=User ID(s) to get roles for. Can be a single user or multiple users."`
}

func listUserRoles(ctx context.Context, args ListUserRolesParams) (map[string][]models.RoleDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	searchQuery := &models.RolesSearchQuery{UserIds: args.UserIDs}
	params := access_control.NewListUsersRolesParamsWithContext(ctx).WithBody(searchQuery)

	resp, err := c.AccessControl.ListUsersRolesWithParams(params)
	if err != nil {
		return nil, fmt.Errorf("list user roles: %w", err)
	}
	return resp.Payload, nil
}

type ListTeamRolesParams struct {
	TeamIDs []int64 `json:"teamIds" jsonschema:"required,description=Team ID(s) to get roles for. Can be a single team or multiple teams."`
}

func listTeamRoles(ctx context.Context, args ListTeamRolesParams) (map[string][]models.RoleDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	searchQuery := &models.RolesSearchQuery{TeamIds: args.TeamIDs}
	params := access_control.NewListTeamsRolesParamsWithContext(ctx).WithBody(searchQuery)

	resp, err := c.AccessControl.ListTeamsRolesWithParams(params)
	if err != nil {
		return nil, fmt.Errorf("list team roles: %w", err)
	}
	return resp.Payload, nil
}

type GetResourcePermissionsParams struct {
	Resource   string `json:"resource" jsonschema:"required,description=Resource type (e.g. 'dashboards' 'datasources' 'folders')"`
	ResourceID string `json:"resourceId" jsonschema:"required,description=Unique identifier of the resource (UID for dashboards/datasources/folders)"`
}

func getResourcePermissions(ctx context.Context, args GetResourcePermissionsParams) ([]*models.ResourcePermissionDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := access_control.NewGetResourcePermissionsParamsWithContext(ctx).WithResource(args.Resource).WithResourceID(args.ResourceID)

	resp, err := c.AccessControl.GetResourcePermissionsWithParams(params)
	if err != nil {
		return nil, fmt.Errorf("get resource permissions: %w", err)
	}
	return resp.Payload, nil
}

type GetResourceDescriptionParams struct {
	ResourceType string `json:"resourceType" jsonschema:"required,enum=dashboards,enum=datasources,enum=folders,enum=teams,enum=users,enum=serviceaccounts,description=Type of Grafana resource to get description for"`
}

func getResourceDescription(ctx context.Context, args GetResourceDescriptionParams) (*models.Description, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	params := access_control.NewGetResourceDescriptionParamsWithContext(ctx).
		WithResource(args.ResourceType)

	resp, err := c.AccessControl.GetResourceDescriptionWithParams(params)
	if err != nil {
		return nil, fmt.Errorf("get resource description: %w", err)
	}

	return resp.Payload, nil
}
