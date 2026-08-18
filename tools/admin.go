package tools

import (
	"context"
	"fmt"

	"github.com/grafana/grafana-openapi-client-go/client/access_control"
	"github.com/grafana/grafana-openapi-client-go/client/org"
	"github.com/grafana/grafana-openapi-client-go/client/teams"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

var ListTeams = mcpgrafana.MustTool(
	"list_teams",
	"Search for Grafana teams by a query string. Returns a list of matching teams with details like name, ID, and URL.",
	listTeams,
	mcpgrafana.WithTitleAnnotation("List teams"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

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

var ListUsersByOrg = mcpgrafana.MustTool(
	"list_users_by_org",
	"List users in the Grafana organization. Returns a list of organization users with details like userid, email, role etc.",
	listUsersByOrg,
	mcpgrafana.WithTitleAnnotation("List users by org"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

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

var ListAllRoles = mcpgrafana.MustTool(
	"list_all_roles",
	"List all roles in Grafana. Optionally filter to show only roles that can be delegated by the current user. Returns role details including UID, name, permissions, and metadata.",
	listAllRoles,
	mcpgrafana.WithTitleAnnotation("List all roles"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

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

var GetRoleDetails = mcpgrafana.MustTool(
	"get_role_details",
	"Get detailed information about a specific Grafana role by its UID, including permissions, metadata, and configuration.",
	getRoleDetails,
	mcpgrafana.WithTitleAnnotation("Get role details"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

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

var GetRoleAssignments = mcpgrafana.MustTool(
	"get_role_assignments",
	"List all assignments for a specific role, showing which users, teams, and service accounts have been assigned this role.",
	getRoleAssignments,
	mcpgrafana.WithTitleAnnotation("Get role assignments"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

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

var ListUserRoles = mcpgrafana.MustTool(
	"list_user_roles",
	"List all roles assigned to one or more users. Returns a map of user IDs to their assigned roles, excluding built-in roles and team-inherited roles.",
	listUserRoles,
	mcpgrafana.WithTitleAnnotation("List user roles"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

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

var ListTeamRoles = mcpgrafana.MustTool(
	"list_team_roles",
	"List all roles assigned to one or more teams. Returns a map of team IDs to their assigned roles.",
	listTeamRoles,
	mcpgrafana.WithTitleAnnotation("List team roles"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

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

var GetResourcePermissions = mcpgrafana.MustTool(
	"get_resource_permissions",
	"List all permissions set on a specific Grafana resource (e.g., dashboard, datasource, folder) by its type and ID.",
	getResourcePermissions,
	mcpgrafana.WithTitleAnnotation("Get resource permissions"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

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

var GetResourceDescription = mcpgrafana.MustTool(
	"get_resource_description",
	"List available permissions and assignment capabilities for a Grafana resource type.",
	getResourceDescription,
	mcpgrafana.WithTitleAnnotation("Get resource description"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

func AddAdminTools(mcp *mcp.Server) {
	ListTeams.Register(mcp)
	ListUsersByOrg.Register(mcp)
	ListAllRoles.Register(mcp)
	GetRoleDetails.Register(mcp)
	GetRoleAssignments.Register(mcp)
	ListUserRoles.Register(mcp)
	ListTeamRoles.Register(mcp)
	GetResourcePermissions.Register(mcp)
	GetResourceDescription.Register(mcp)
}
