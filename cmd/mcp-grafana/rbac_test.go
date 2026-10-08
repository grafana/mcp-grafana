package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// TestEveryToolDeclaresPermissions fails when a tool is registered without
// mcpgrafana.RequiresPermissions. It checks every combination of the flags
// that pick between separate MustTool variants of a tool.
func TestEveryToolDeclaresPermissions(t *testing.T) {
	var variants []disabledTools
	for _, write := range []bool{false, true} {
		for _, query := range []bool{false, true} {
			for _, enableQuery := range []bool{false, true} {
				variants = append(variants, disabledTools{write: write, query: query, enableQuery: enableQuery})
			}
		}
	}
	for _, dt := range variants {
		for name, tool := range listAllCategoryTools(t, dt) {
			_, ok := mcpgrafana.ToolRequiredPermissions(tool)
			assert.True(t, ok, "tool %q declares no RBAC permissions: add mcpgrafana.RequiresPermissions(...) "+
				"to its MustTool call, e.g. RequiresPermissions(datasourceQuery...) for a datasource query tool "+
				"(see tools/rbac.go), or RequiresPermissions() if it needs none", name)
		}
	}
}
