package mcpgrafana

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPAppResources(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "apps-test", Version: "1"}, nil)
	RegisterAppResources(s)
	ctx := t.Context()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = s.Run(ctx, serverTransport) }()
	c, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	resources, err := c.ListResources(ctx, nil)
	require.NoError(t, err)
	for _, uri := range []string{TraceViewerResourceURI} {
		t.Run(uri, func(t *testing.T) {
			var found bool
			for _, resource := range resources.Resources {
				if resource.URI == uri {
					found = true
					require.Equal(t, appMIMEType, resource.MIMEType)
					require.Contains(t, resource.Meta, "ui")
				}
			}
			require.True(t, found)
			result, err := c.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
			require.NoError(t, err)
			require.Len(t, result.Contents, 1)
			content := result.Contents[0]
			require.Equal(t, uri, content.URI)
			require.Equal(t, appMIMEType, content.MIMEType)
			require.Contains(t, strings.ToLower(content.Text), "<!doctype html>")
			require.NotContains(t, content.Text, "<script src=")
			ui, ok := content.Meta["ui"].(map[string]any)
			require.True(t, ok)
			csp, ok := ui["csp"].(map[string]any)
			require.True(t, ok)
			require.Empty(t, csp["connectDomains"])
			require.Empty(t, csp["resourceDomains"])
			if uri == TraceViewerResourceURI {
				require.Contains(t, ui, "permissions")
			} else {
				require.NotContains(t, ui, "permissions")
			}
		})
	}
}
