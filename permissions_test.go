package mcpgrafana

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequiresPermissions(t *testing.T) {
	tool := &mcp.Tool{}
	RequiresPermissions("b:read", "a:read")(tool)
	RequiresPermissions("a:read", "c:write")(tool)
	perms, ok := ToolRequiredPermissions(tool)
	require.True(t, ok)
	assert.Equal(t, []string{"a:read", "b:read", "c:write"}, perms)

	// An empty declaration still counts, including after a tools/list round trip.
	empty := &mcp.Tool{Name: "none"}
	RequiresPermissions()(empty)
	b, err := json.Marshal(empty)
	require.NoError(t, err)
	var decoded mcp.Tool
	require.NoError(t, json.Unmarshal(b, &decoded))
	perms, ok = ToolRequiredPermissions(&decoded)
	require.True(t, ok)
	assert.Empty(t, perms)

	_, ok = ToolRequiredPermissions(&mcp.Tool{})
	assert.False(t, ok)
}
