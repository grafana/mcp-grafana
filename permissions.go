package mcpgrafana

import (
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RequiredPermissionsMetaKey is the tool _meta key that lists the Grafana RBAC
// actions a tool's calls need. Set it with RequiresPermissions.
const RequiredPermissionsMetaKey = "grafana.com/requiredPermissions"

// RequiresPermissions declares Grafana RBAC actions the tool's calls need. A
// caller missing any of them gets a 403 from the tool. Every tool must declare
// its actions, including tools that need none (call it with no arguments); a
// test fails otherwise. Repeated calls add to the list, so a shared group can
// be combined with extra actions:
//
//	mcpgrafana.RequiresPermissions(datasourceQuery...),
//	mcpgrafana.RequiresPermissions("dashboards:read"),
//
// Calls to /apis endpoints are checked twice: the user's legacy action (the
// verb as mapped by Grafana, e.g. dashboards:read) and, for on-behalf-of
// tokens, the token's group/resource:verb permission (e.g.
// dashboard.grafana.app/dashboards:get). Tools that use /apis declare both.
//
// Declare the actions for every operation the tool can perform, including
// write operations behind a read-write variant.
func RequiresPermissions(actions ...string) ToolOption {
	return func(t *mcp.Tool) {
		if t.Meta == nil {
			t.Meta = mcp.Meta{}
		}
		perms, _ := ToolRequiredPermissions(t)
		perms = append(append([]string{}, perms...), actions...)
		slices.Sort(perms)
		t.Meta[RequiredPermissionsMetaKey] = slices.Compact(perms)
	}
}

// ToolRequiredPermissions returns the actions declared with
// RequiresPermissions and whether the tool declared any. It accepts both the
// in-process value and one decoded from a tools/list response.
func ToolRequiredPermissions(t *mcp.Tool) ([]string, bool) {
	switch v := t.Meta[RequiredPermissionsMetaKey].(type) {
	case []string:
		return slices.Clone(v), true
	case []any:
		perms := make([]string, 0, len(v))
		for _, p := range v {
			s, ok := p.(string)
			if !ok {
				return nil, false
			}
			perms = append(perms, s)
		}
		return perms, true
	}
	return nil, false
}
