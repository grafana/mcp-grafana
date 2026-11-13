package mcpgrafana

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

// externalScripts gives the src of each script element. The tokenizer reads a script body as text, so a string in
// the bundle that looks like a tag, for example in react-dom/server, is not an element.
func externalScripts(page string) []string {
	var srcs []string
	z := html.NewTokenizer(strings.NewReader(page))
	for tt := z.Next(); tt != html.ErrorToken; tt = z.Next() {
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		if t := z.Token(); t.Data == "script" {
			for _, a := range t.Attr {
				if a.Key == "src" {
					srcs = append(srcs, a.Val)
				}
			}
		}
	}
	return srcs
}

// maxAppSize caps the inlined app, about 5 MB with the Renderer, so a growth in the bundle is a decision.
const maxAppSize = 6 << 20

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
	for _, uri := range []string{GrafanaAppResourceURI, PanelViewerResourceURI} {
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
			require.Empty(t, externalScripts(content.Text), "the app loads no script file")
			require.Less(t, len(content.Text), maxAppSize, "the app grew past %d bytes", maxAppSize)
			ui, ok := content.Meta["ui"].(map[string]any)
			require.True(t, ok)
			csp, ok := ui["csp"].(map[string]any)
			require.True(t, ok)
			require.Empty(t, csp["connectDomains"])
			require.Equal(t, []any{"data:"}, csp["resourceDomains"])
			if uri == GrafanaAppResourceURI {
				require.Contains(t, ui, "permissions")
			} else {
				require.NotContains(t, ui, "permissions")
			}
		})
	}
}

// maxVizRendererSize caps the Renderer resource below 4 MiB, a size limit a host may set on one resource (#1589).
const maxVizRendererSize = 4 << 20

func TestVizRendererResource(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "apps-test", Version: "1"}, nil)
	RegisterGrafanaAppResource(s)
	ctx := t.Context()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = s.Run(ctx, serverTransport) }()
	c, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	sum := sha256.Sum256([]byte(vizRendererJS))
	require.Equal(t, "ui://grafana/viz-renderer-"+hex.EncodeToString(sum[:8])+".js", VizRendererResourceURI,
		"dist/viz-renderer.hash is the hash of dist/viz-renderer.js; run mise run build-ui")

	resources, err := c.ListResources(ctx, nil)
	require.NoError(t, err)
	var listed *mcp.Resource
	for _, resource := range resources.Resources {
		if resource.URI == VizRendererResourceURI {
			listed = resource
		}
	}
	require.NotNil(t, listed, "RegisterGrafanaAppResource registers the Renderer, so embedders get it too")
	require.Equal(t, vizRendererMIMEType, listed.MIMEType)

	result, err := c.ReadResource(ctx, &mcp.ReadResourceParams{URI: VizRendererResourceURI})
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	require.Equal(t, vizRendererMIMEType, result.Contents[0].MIMEType)
	require.Equal(t, vizRendererJS, result.Contents[0].Text)
	require.Equal(t, vizRendererTTLMs, result.TTLMs)
	require.True(t, strings.HasPrefix(vizRendererJS, "var __grafanaVizRenderer="), "a classic script that sets the Renderer global")
	require.Less(t, len(vizRendererJS), maxVizRendererSize, "the Renderer grew past %d bytes", maxVizRendererSize)
}
