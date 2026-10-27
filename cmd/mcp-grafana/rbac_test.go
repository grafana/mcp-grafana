package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// rbacFlagVariants returns every combination of the flags that pick between
// separate MustTool variants of a tool.
func rbacFlagVariants() []disabledTools {
	var variants []disabledTools
	for _, write := range []bool{false, true} {
		for _, query := range []bool{false, true} {
			for _, enableQuery := range []bool{false, true} {
				variants = append(variants, disabledTools{write: write, query: query, enableQuery: enableQuery})
			}
		}
	}
	return variants
}

// TestEveryToolDeclaresPermissions fails when a tool is registered without
// mcpgrafana.RequiresPermissions.
func TestEveryToolDeclaresPermissions(t *testing.T) {
	for _, dt := range rbacFlagVariants() {
		for name, tool := range listAllCategoryTools(t, dt) {
			_, ok := mcpgrafana.ToolRequiredPermissions(tool)
			assert.True(t, ok, "tool %q declares no RBAC permissions: add mcpgrafana.RequiresPermissions(...) "+
				"to its MustTool call, e.g. RequiresPermissions(datasourceQuery...) for a datasource query tool "+
				"(see tools/rbac.go), or RequiresPermissions() if it needs none", name)
		}
	}
}

// TestEveryToolHasRBACFixture fails when a tool has no entry in rbacFixtures,
// so TestToolPermissions checks every tool's declared permissions (or says why
// it can't), and when an entry names a tool that no longer exists.
func TestEveryToolHasRBACFixture(t *testing.T) {
	registered := map[string]bool{}
	for _, dt := range rbacFlagVariants() {
		for name := range listAllCategoryTools(t, dt) {
			registered[name] = true
		}
	}
	for name := range registered {
		_, ok := rbacFixtures[name]
		assert.True(t, ok, "tool %q has no entry in rbacFixtures (cmd/mcp-grafana/rbac_fixtures_test.go): "+
			"add the arguments TestToolPermissions should call it with, or a skip reason", name)
	}
	for name := range rbacFixtures {
		assert.True(t, registered[name], "rbacFixtures has an entry for %q, which is not a registered tool", name)
	}
}
