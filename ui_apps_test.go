package mcpgrafana

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The registry must include both the panel-viewer and the insight-cell apps,
// each with an embedded, non-empty, single-file HTML bundle.
func TestAppResourcesRegistry(t *testing.T) {
	byURI := map[string]UIApp{}
	for _, app := range appResources {
		byURI[app.URI] = app
	}

	for _, uri := range []string{PanelViewerResourceURI, InsightCellResourceURI} {
		app, ok := byURI[uri]
		require.True(t, ok, "expected an app registered at %s", uri)
		assert.NotEmpty(t, app.Name)
		assert.NotEmpty(t, app.HTML, "app %s should embed a built HTML bundle (run make build-ui)", uri)
	}
}

// RegisterAppResources should register every app so it is discoverable via the
// MCP Resources API (resources/list).
func TestRegisterAppResourcesListsEachApp(t *testing.T) {
	s := server.NewMCPServer("test", "0.0.0", server.WithResourceCapabilities(false, true))
	RegisterAppResources(s)

	req := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	resp := s.HandleMessage(context.Background(), req)
	out, err := json.Marshal(resp)
	require.NoError(t, err)

	for _, app := range appResources {
		assert.Contains(t, string(out), app.URI, "resources/list should advertise %s", app.URI)
	}
}
