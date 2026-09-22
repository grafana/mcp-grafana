package mcpgrafana

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

func TestAdvertisesUIExtension(t *testing.T) {
	t.Run("true when the client negotiated the MCP Apps extension", func(t *testing.T) {
		assert.True(t, AdvertisesUIExtension(mcp.ClientCapabilities{
			Extensions: map[string]any{"io.modelcontextprotocol/ui": map[string]any{}},
		}))
	})

	t.Run("false when the client declared other extensions but not this one", func(t *testing.T) {
		// The distinction that matters: "advertises extensions" is not "renders apps".
		assert.False(t, AdvertisesUIExtension(mcp.ClientCapabilities{
			Extensions: map[string]any{"io.example/other": map[string]any{}},
		}))
	})

	t.Run("false when the client declared no extensions at all", func(t *testing.T) {
		// Claude Code's terminal, which renders no app and spills a large payload to a
		// file for the agent to pick apart.
		assert.False(t, AdvertisesUIExtension(mcp.ClientCapabilities{}))
	})
}
