package mcpgrafana

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestMCPAppResources(t *testing.T) {
	s := server.NewMCPServer("apps-test", "1", server.WithResourceCapabilities(false, false))
	RegisterAppResources(s)
	c, err := client.NewInProcessClient(s)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, c.Start(ctx))
	t.Cleanup(func() { _ = c.Close() })
	_, err = c.Initialize(ctx, mcp.InitializeRequest{})
	require.NoError(t, err)
	resources, err := c.ListResources(ctx, mcp.ListResourcesRequest{})
	require.NoError(t, err)
	for _, uri := range []string{TraceViewerResourceURI, ServiceHealthResourceURI} {
		t.Run(uri, func(t *testing.T) {
			var found bool
			for _, resource := range resources.Resources {
				if resource.URI == uri {
					found = true
					require.Equal(t, appMIMEType, resource.MIMEType)
					require.NotNil(t, resource.Meta)
					require.Contains(t, resource.Meta.AdditionalFields, "ui")
				}
			}
			require.True(t, found)
			request := mcp.ReadResourceRequest{}
			request.Params.URI = uri
			result, err := c.ReadResource(ctx, request)
			require.NoError(t, err)
			require.Len(t, result.Contents, 1)
			content, ok := result.Contents[0].(mcp.TextResourceContents)
			require.True(t, ok)
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
