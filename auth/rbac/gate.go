package rbac

import (
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Gate filters tools/list responses according to a registry and a per-user
// snapshot.
type Gate struct {
	registry map[string]ToolGate
}

// NewGate builds a Gate over the given registry. Pass rbac.ToolGates in
// production; pass a smaller map in tests.
func NewGate(registry map[string]ToolGate) *Gate {
	return &Gate{registry: registry}
}

// Filter returns the subset of tools the user is allowed to see according
// to mode. Tools NOT in the registry are passed through unchanged: that's
// the "fail open" behavior the spec requires (the registry coverage test
// catches missing entries at build time).
func (g *Gate) Filter(mode Mode, snap Snapshot, tools []mcp.Tool) []mcp.Tool {
	if mode == ModeOff {
		return tools
	}
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if g.Allow(mode, snap, t.Name) {
			out = append(out, t)
		}
	}
	return out
}

// Allow reports whether a single tool is permitted under mode for the
// given snapshot. Same gate semantics as Filter; extracted so the
// call-time middleware can reuse it without rebuilding a tools slice.
//
// Tools not in the registry pass through (consistent with Filter's
// "unknown — let it through" branch). Public tools always pass. ModeOff
// returns true unconditionally — the caller (Filter / middleware) is
// responsible for short-circuiting before this is reached on the hot
// path, but this stays safe for direct callers.
func (g *Gate) Allow(mode Mode, snap Snapshot, toolName string) bool {
	if mode == ModeOff {
		return true
	}
	gate, ok := g.registry[toolName]
	if !ok {
		return true
	}
	if gate.IsPublic() {
		return true
	}
	switch mode {
	case ModeEnterprise:
		// If the gate has Permissions, ModeEnterprise checks them.
		// If the gate has ONLY MinBasicRole (e.g. Incident, Sift —
		// plugins without fine-grained RBAC actions), fall back to
		// the basic-role check; otherwise tools that have no
		// Permissions but DO have a MinBasicRole would be visible
		// to every authenticated user via
		// allPermissionsGranted(_, nil) == true.
		if len(gate.Permissions) > 0 {
			return allPermissionsGranted(snap.Permissions, gate.Permissions)
		}
		return basicRoleSatisfies(snap.BasicRole, gate.MinBasicRole)
	case ModeBasic:
		return basicRoleSatisfies(snap.BasicRole, gate.MinBasicRole)
	default:
		// Fail open for unrecognised modes (incl. ModeAuto). Filter / the
		// middleware never let ModeAuto through, but this keeps Allow safe
		// for direct callers — better to over-expose than to silently
		// drop the entire non-public catalog.
		return true
	}
}

// allPermissionsGranted reports whether every required permission has at
// least one matching grant in the user's permission set.
func allPermissionsGranted(perms PermissionSet, required []Permission) bool {
	if len(required) == 0 {
		return true
	}
	for _, req := range required {
		if !permissionGranted(perms, req) {
			return false
		}
	}
	return true
}

// permissionGranted reports whether the user has any grant for the action
// whose scope covers the requirement's scope.
func permissionGranted(perms PermissionSet, req Permission) bool {
	scopes, ok := perms[req.Action]
	if !ok {
		return false
	}
	for _, granted := range scopes {
		if scopeMatch(granted, req.Scope) {
			return true
		}
	}
	return false
}

// scopeMatch reports whether the grant covers the requirement under
// Grafana's scope-tree semantics. Empty grant doesn't match anything;
// "*" matches everything; "<prefix>:*" matches any string starting with
// "<prefix>:". Otherwise exact match.
//
// An empty requirement means "the action at any scope" — any non-empty
// grant satisfies it (the user has the action on at least one resource).
func scopeMatch(grant, requirement string) bool {
	if grant == "" {
		// Empty grant means "no scope filter at all" only when the action
		// is global. permissionGranted is the only caller; require an
		// empty requirement too (action is global on both sides).
		return requirement == ""
	}
	if requirement == "" {
		// Any non-empty grant satisfies an action-only (no scope) requirement.
		return true
	}
	if grant == "*" {
		return true
	}
	if strings.HasSuffix(grant, ":*") {
		return strings.HasPrefix(requirement, strings.TrimSuffix(grant, "*"))
	}
	return grant == requirement
}

// basicRoleSatisfies reports whether userRole >= minRole on the
// Viewer < Editor < Admin ladder. Empty minRole = always satisfied.
// Empty userRole = never satisfied (no role).
func basicRoleSatisfies(userRole, minRole string) bool {
	if minRole == "" {
		return true
	}
	return roleRank(userRole) >= roleRank(minRole)
}

func roleRank(r string) int {
	switch r {
	case "Admin":
		return 3
	case "Editor":
		return 2
	case "Viewer":
		return 1
	default:
		return 0
	}
}
