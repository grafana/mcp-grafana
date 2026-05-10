// Package rbac implements per-user RBAC-based filtering of the MCP tools/list
// response. It is layered on top of the auth package: when a request carries
// a resolved auth session, the registered hook fetches that user's Grafana
// permissions (cached per session) and removes from the list any tool the
// user is not allowed to call.
//
// This is a UX/correctness feature, not a security boundary. Grafana itself
// still enforces RBAC when the per-user credential reaches it.
package rbac

import "fmt"

// Permission is a Grafana RBAC requirement: an action and an optional scope.
// Empty Scope means the action can be granted at any scope.
type Permission struct {
	Action string
	Scope  string
}

func (p Permission) String() string {
	scope := p.Scope
	if scope == "" {
		scope = "*"
	}
	return p.Action + " @ " + scope
}

// ToolGate describes the RBAC + basic-role requirements for a single MCP tool.
//
//   - Permissions: every entry must be matched by some grant in the user's
//     permission set for the tool to be visible (in `enterprise` mode).
//   - MinBasicRole: the lowest built-in role the user must hold for the tool
//     to be visible in `basic` mode. Empty means the tool is public.
//
// A zero ToolGate is "public": the tool is always visible. This is correct
// only for tools that genuinely have no Grafana-side authorization (e.g.,
// the navigation-deeplink generator).
type ToolGate struct {
	Permissions  []Permission
	MinBasicRole string // "Viewer" | "Editor" | "Admin" | ""
}

// IsPublic reports whether the gate has no requirements.
func (g ToolGate) IsPublic() bool {
	return len(g.Permissions) == 0 && g.MinBasicRole == ""
}

// Errors used by the package.
var (
	ErrFetchFailed = fmt.Errorf("rbac: permission fetch failed")
	ErrModeUnknown = fmt.Errorf("rbac: unknown gating mode")
)
