package rbac_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/mcp-grafana/auth/rbac"
	"github.com/grafana/mcp-grafana/tools"
)

func TestRegistry_AllRegisteredToolsCovered(t *testing.T) {
	s := server.NewMCPServer("rbac-coverage", "0")

	// Register every tool category. Pass `true` for write-tools so the
	// registry is exercised against the union of read+write tools.
	tools.AddSearchTools(s)
	tools.AddDatasourceTools(s)
	tools.AddIncidentTools(s, true)
	tools.AddPrometheusTools(s)
	tools.AddLokiTools(s)
	tools.AddElasticsearchTools(s)
	tools.AddInfluxDBTools(s)
	tools.AddAlertingTools(s, true)
	tools.AddDashboardTools(s, true)
	tools.AddFolderTools(s, true)
	tools.AddOnCallTools(s)
	tools.AddAssertsTools(s)
	tools.AddSiftTools(s, true)
	tools.AddAdminTools(s)
	tools.AddPyroscopeTools(s)
	tools.AddNavigationTools(s)
	tools.AddAnnotationTools(s, true)
	tools.AddRenderingTools(s)
	tools.AddCloudWatchTools(s)
	tools.AddExamplesTools(s)
	tools.AddClickHouseTools(s)
	tools.AddSnowflakeTools(s)
	tools.AddRunPanelQueryTools(s)
	tools.AddGraphiteTools(s)
	tools.AddPluginTools(s, true)
	tools.AddAPITools(s, true)

	// Pull the full tool list out of the server.
	resp := s.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	listed := extractToolNames(t, resp)
	if len(listed) == 0 {
		t.Fatalf("no tools were registered; check the test setup above")
	}

	missing := []string{}
	for _, name := range listed {
		if _, ok := rbac.ToolGates[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("the following tools have no entry in auth/rbac/registry.go ToolGates:\n  %v\nAdd a ToolGate{...} for each.", missing)
	}

	// Also flag stale registry entries — names in the registry that no
	// tool registration produces. Stale entries become misleading over time.
	registered := make(map[string]struct{}, len(listed))
	for _, n := range listed {
		registered[n] = struct{}{}
	}
	stale := []string{}
	for name := range rbac.ToolGates {
		if _, ok := registered[name]; !ok {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		t.Fatalf("the following entries in ToolGates do not match any registered tool (stale):\n  %v", stale)
	}
}

// TestRegistry_DatasourceToolsAcceptUIDScopedGrants is the regression guard
// for the bugbot finding: a user whose RBAC grant is per-datasource (e.g.
// datasources:uid:prom) must satisfy the gate for datasource tools.
// Requiring "datasources:*" on the registry side would force the user to
// have a wildcard grant, locking out users whose Grafana admin gave them
// targeted access — the same UX cliff the dashboard tools intentionally
// avoid via empty scopes.
func TestRegistry_DatasourceToolsAcceptUIDScopedGrants(t *testing.T) {
	uidGrant := rbac.PermissionSet{
		"datasources:read":  []string{"datasources:uid:prom"},
		"datasources:query": []string{"datasources:uid:prom"},
	}
	gate := rbac.NewGate(rbac.ToolGates)
	snap := rbac.Snapshot{Permissions: uidGrant, BasicRole: "Viewer"}
	for _, name := range []string{"list_datasources", "get_datasource", "query_prometheus"} {
		if !gate.Allow(rbac.ModeEnterprise, snap, name) {
			t.Errorf("user with datasources:uid:prom grant denied %q — registry scope is too narrow", name)
		}
	}
}

// extractToolNames pulls the tool names out of an MCP tools/list response.
// HandleMessage may return a typed response struct or raw JSON bytes;
// round-trip through encoding/json so we don't depend on mcp-go internals.
func extractToolNames(t *testing.T, raw any) []string {
	t.Helper()
	type tool struct {
		Name string `json:"name"`
	}
	type result struct {
		Tools []tool `json:"tools"`
	}
	type rpc struct {
		Result result `json:"result"`
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var r rpc
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	names := make([]string, 0, len(r.Result.Tools))
	for _, x := range r.Result.Tools {
		names = append(names, x.Name)
	}
	return names
}
