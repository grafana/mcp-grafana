package rbac

import (
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestScopeMatch(t *testing.T) {
	cases := []struct {
		grant string
		req   string
		want  bool
	}{
		{"datasources:*", "datasources:uid:prom", true},
		{"datasources:uid:prom", "datasources:uid:prom", true},
		{"datasources:uid:prom", "datasources:uid:loki", false},
		{"datasources:uid:*", "datasources:uid:prom", true},
		{"*", "anything", true},
		{"", "datasources:uid:prom", false},
		{"folders:*", "dashboards:uid:x", false},
		// Empty requirement means "action on any resource": any non-empty grant satisfies it.
		// This is the common OSS case where Grafana returns folder-scoped dashboard grants.
		{"folders:uid:abc", "", true},
		{"dashboards:*", "", true},
		{"", "", true}, // empty grant, empty req: global action (both sides)

	}
	for _, tc := range cases {
		if got := scopeMatch(tc.grant, tc.req); got != tc.want {
			t.Errorf("scopeMatch(%q, %q)=%v want %v", tc.grant, tc.req, got, tc.want)
		}
	}
}

func TestPermissionGranted(t *testing.T) {
	perms := PermissionSet{
		"datasources:query": {"datasources:uid:prom", "datasources:uid:loki"},
		"datasources:read":  {"datasources:*"},
		"alert.rules:read":  {""}, // global
	}
	cases := []struct {
		req  Permission
		want bool
	}{
		{Permission{"datasources:read", "datasources:uid:foo"}, true},
		{Permission{"datasources:query", "datasources:uid:prom"}, true},
		{Permission{"datasources:query", "datasources:uid:foo"}, false},
		{Permission{"datasources:write", "datasources:uid:prom"}, false},
		{Permission{"alert.rules:read", ""}, true},
		// Empty scope requirement: "has the action on any resource". The user has
		// dashboards:read only on folder-scoped grants (OSS Viewer pattern) — still satisfies.
		{Permission{"datasources:read", ""}, true},  // non-empty grant covers empty req
		{Permission{"datasources:query", ""}, true}, // action present, scope doesn't matter
	}
	for _, tc := range cases {
		if got := permissionGranted(perms, tc.req); got != tc.want {
			t.Errorf("permissionGranted(%v)=%v want %v", tc.req, got, tc.want)
		}
	}
}

func TestGate_FilterEnterprise(t *testing.T) {
	gate := NewGate(map[string]ToolGate{
		"q_prom": {Permissions: []Permission{{"datasources:query", "datasources:uid:prom"}}},
		"q_loki": {Permissions: []Permission{{"datasources:query", "datasources:uid:loki"}}},
		"public": {},
	})
	snap := Snapshot{
		Permissions: PermissionSet{"datasources:query": {"datasources:uid:prom"}},
	}

	tools := []mcp.Tool{
		{Name: "q_prom"},
		{Name: "q_loki"},
		{Name: "public"},
		{Name: "unknown_tool"}, // not in registry
	}

	got := gate.Filter(ModeEnterprise, snap, tools)
	got = sortByName(got)
	want := []string{"public", "q_prom", "unknown_tool"}
	if !sameNames(got, want) {
		t.Errorf("got %v want %v", names(got), want)
	}
}

func TestGate_FilterBasic(t *testing.T) {
	gate := NewGate(map[string]ToolGate{
		"viewer_only": {MinBasicRole: "Viewer"},
		"editor_only": {MinBasicRole: "Editor"},
		"admin_only":  {MinBasicRole: "Admin"},
		"public":      {},
	})
	tools := []mcp.Tool{
		{Name: "viewer_only"},
		{Name: "editor_only"},
		{Name: "admin_only"},
		{Name: "public"},
	}

	for _, tc := range []struct {
		role string
		want []string
	}{
		{"Viewer", []string{"public", "viewer_only"}},
		{"Editor", []string{"editor_only", "public", "viewer_only"}},
		{"Admin", []string{"admin_only", "editor_only", "public", "viewer_only"}},
		{"", []string{"public"}},
	} {
		got := gate.Filter(ModeBasic, Snapshot{BasicRole: tc.role}, tools)
		if !sameNames(sortByName(got), tc.want) {
			t.Errorf("role=%q got %v want %v", tc.role, names(got), tc.want)
		}
	}
}

func TestGate_FilterOff(t *testing.T) {
	gate := NewGate(map[string]ToolGate{"x": {Permissions: []Permission{{"a", ""}}}})
	tools := []mcp.Tool{{Name: "x"}, {Name: "y"}}
	got := gate.Filter(ModeOff, Snapshot{}, tools)
	if len(got) != 2 {
		t.Errorf("ModeOff should not filter, got %v", names(got))
	}
}

func names(ts []mcp.Tool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func sortByName(ts []mcp.Tool) []mcp.Tool {
	out := append([]mcp.Tool(nil), ts...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sameNames(ts []mcp.Tool, want []string) bool {
	got := names(ts)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestGate_FilterFailsOpenForUnknownMode(t *testing.T) {
	// A gated tool that nominally requires Enterprise permissions.
	registry := map[string]ToolGate{
		"gated": {Permissions: []Permission{{Action: "x:read"}}},
	}
	g := NewGate(registry)
	tools := []mcp.Tool{{Name: "gated"}, {Name: "other"}}

	// ModeAuto must NOT silently drop the gated tool — without a default
	// case the switch would skip the append and remove it.
	got := g.Filter(ModeAuto, Snapshot{}, tools)
	if len(got) != 2 {
		t.Errorf("ModeAuto must fail open: got %d tools, want 2 (%v)", len(got), got)
	}

	// And same for any other unrecognised value.
	got = g.Filter(Mode("not-a-real-mode"), Snapshot{}, tools)
	if len(got) != 2 {
		t.Errorf("unknown mode must fail open: got %d tools, want 2 (%v)", len(got), got)
	}
}

func TestGate_FilterEnterpriseFallsBackToBasicRoleWhenNoPermissions(t *testing.T) {
	// A gate with MinBasicRole only (e.g. Incident/Sift entries) must
	// honour the role check even in ModeEnterprise — without the
	// fallback, allPermissionsGranted(_, nil)==true would expose these
	// tools to every authenticated user regardless of role.
	registry := map[string]ToolGate{
		"editor_only": {MinBasicRole: "Editor"},
	}
	g := NewGate(registry)
	tools := []mcp.Tool{{Name: "editor_only"}}

	// Viewer in ModeEnterprise: must NOT see the tool.
	got := g.Filter(ModeEnterprise, Snapshot{BasicRole: "Viewer"}, tools)
	if len(got) != 0 {
		t.Errorf("Viewer should not see Editor-gated tool in ModeEnterprise, got %d tools", len(got))
	}
	// Editor in ModeEnterprise: should see the tool.
	got = g.Filter(ModeEnterprise, Snapshot{BasicRole: "Editor"}, tools)
	if len(got) != 1 {
		t.Errorf("Editor should see Editor-gated tool in ModeEnterprise, got %d tools", len(got))
	}
	// Empty role in ModeEnterprise: must NOT see the tool.
	got = g.Filter(ModeEnterprise, Snapshot{BasicRole: ""}, tools)
	if len(got) != 0 {
		t.Errorf("user with no role should not see Editor-gated tool in ModeEnterprise, got %d tools", len(got))
	}
}
