package tools

// AdminReadParams is the param struct for admin_read, a consolidated,
// read-only tool covering teams, users, roles, and resource permissions.
// admin_read has no write counterpart: none of the underlying Grafana admin
// APIs it wraps support mutation, so there is nothing to gate behind
// enableWriteTools and no admin_write should be introduced without new
// write-capable functionality to back it.
type AdminReadParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=list_teams,enum=list_users,enum=list_roles,enum=get_role,enum=role_assignments,enum=user_roles,enum=team_roles,enum=resource_permissions,enum=describe_resource,description=The operation to perform: 'list_teams' to search Grafana teams\\, 'list_users' to list organization users\\, 'list_roles' to list all RBAC roles\\, 'get_role' to get details for one role by UID\\, 'role_assignments' to list who a role is assigned to\\, 'user_roles' to list roles assigned to one or more users\\, 'team_roles' to list roles assigned to one or more teams\\, 'resource_permissions' to list permissions set on a specific resource\\, or 'describe_resource' to list the permissions and assignment capabilities available for a resource type."`

	// list_teams
	Query string `json:"query,omitempty" jsonschema:"description=Search query for 'list_teams'. Can be left empty to fetch all teams."`

	// list_roles
	DelegatableOnly bool `json:"delegatableOnly,omitempty" jsonschema:"description=For 'list_roles': if true\\, only return roles that can be delegated by the current user."`

	// get_role, role_assignments
	RoleUID string `json:"roleUID,omitempty" jsonschema:"description=Role UID (required for 'get_role' and 'role_assignments')."`

	// user_roles
	UserIDs []int64 `json:"userIds,omitempty" jsonschema:"description=User ID(s) to get roles for (required for 'user_roles'). Can be a single user or multiple users."`

	// team_roles
	TeamIDs []int64 `json:"teamIds,omitempty" jsonschema:"description=Team ID(s) to get roles for (required for 'team_roles'). Can be a single team or multiple teams."`

	// resource_permissions
	Resource   string `json:"resource,omitempty" jsonschema:"description=Resource type\\, e.g. 'dashboards'\\, 'datasources'\\, 'folders' (required for 'resource_permissions')."`
	ResourceID string `json:"resourceId,omitempty" jsonschema:"description=Unique identifier of the resource - UID for dashboards/datasources/folders (required for 'resource_permissions')."`

	// describe_resource
	ResourceType string `json:"resourceType,omitempty" jsonschema:"enum=dashboards,enum=datasources,enum=folders,enum=teams,enum=users,enum=serviceaccounts,description=Type of Grafana resource to get a description for (required for 'describe_resource')."`
}

var adminReadOperations = []string{
	"list_teams", "list_users", "list_roles", "get_role", "role_assignments",
	"user_roles", "team_roles", "resource_permissions", "describe_resource",
}

func (p AdminReadParams) validate() error {
	return NewOperationValidator(p.Operation, adminReadOperations...).
		Require("get_role", StringField("roleUID", p.RoleUID)).
		Require("role_assignments", StringField("roleUID", p.RoleUID)).
		Require("user_roles", SliceField("userIds", p.UserIDs)).
		Require("team_roles", SliceField("teamIds", p.TeamIDs)).
		Require("resource_permissions", StringField("resource", p.Resource), StringField("resourceId", p.ResourceID)).
		Require("describe_resource", StringField("resourceType", p.ResourceType)).
		Validate()
}
