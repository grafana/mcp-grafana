package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// TestEveryToolDeclaresPermissions fails when a tool is registered without
// mcpgrafana.RequiresPermissions. It checks the read-write and read-only
// variants, which are separate MustTool calls for some tools.
func TestEveryToolDeclaresPermissions(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		for name, tool := range listAllCategoryTools(t, disabledTools{write: readOnly}) {
			_, ok := mcpgrafana.ToolRequiredPermissions(tool)
			assert.True(t, ok, "tool %q declares no RBAC permissions: add mcpgrafana.RequiresPermissions(...) "+
				"to its MustTool call, e.g. RequiresPermissions(datasourceQuery...) for a datasource query tool "+
				"(see tools/rbac.go), or RequiresPermissions() if it needs none", name)
		}
	}
}
