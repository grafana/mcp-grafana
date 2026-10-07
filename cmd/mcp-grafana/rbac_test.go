package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/grafana/mcp-grafana/v2/tools"
)

// TestRequiredPermissions_CoversEveryTool keeps tools.RequiredPermissions in
// step with the registered tools.
func TestRequiredPermissions_CoversEveryTool(t *testing.T) {
	registered := registerAllCategories(t, disabledTools{})

	for name := range registered {
		_, ok := tools.RequiredPermissions[name]
		assert.True(t, ok, "tool %q has no entry in tools.RequiredPermissions (tools/rbac.go); "+
			"add the RBAC actions it needs, or an empty list if it needs none", name)
	}
	for name := range tools.RequiredPermissions {
		assert.True(t, registered[name], "tools.RequiredPermissions lists %q, which is not a registered tool", name)
	}
}
