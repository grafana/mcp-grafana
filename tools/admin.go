package tools

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const adminReadDescription = `Access Grafana administration and RBAC information: teams, organization users, roles, role assignments, and resource permissions.

Operations:
- list_teams: search Grafana teams by name (or list all)
- list_users: list users in the current organization
- list_roles: list all Grafana RBAC roles, optionally filtered to delegatable roles
- get_role: get details for a specific role by UID
- role_assignments: list the users, teams, and service accounts a role is assigned to
- user_roles: list roles assigned to one or more users
- team_roles: list roles assigned to one or more teams
- resource_permissions: list permissions set on a specific resource (dashboard, datasource, folder, etc.)
- describe_resource: list available permissions and assignment capabilities for a resource type

This tool is read-only: none of these operations mutate Grafana state, so there is no admin_write counterpart.`

var AdminRead = mcpgrafana.MustTool(
	"admin_read",
	adminReadDescription,
	adminRead,
	mcp.WithTitleAnnotation("Access admin and RBAC information"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddAdminTools registers admin_read. There is no write half: none of the
// underlying admin/RBAC APIs support mutation today, so unlike most other
// consolidated domains this function takes no enableWriteTools parameter.
func AddAdminTools(mcp *server.MCPServer) {
	AdminRead.Register(mcp)
}
