package mcpgrafana

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newCallToolRequest builds a *mcp.CallToolRequest for tests, marshaling args
// to the json.RawMessage the official SDK expects on the wire (mirroring what
// a real client sends, rather than the map[string]any mark3labs let tests
// construct directly).
func newCallToolRequest(name string, args map[string]any) *mcp.CallToolRequest {
	var raw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			panic(err)
		}
		raw = b
	}
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      name,
			Arguments: raw,
		},
	}
}
