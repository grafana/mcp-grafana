package mcpgrafana

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// maxAppSize caps each inlined App resource, so a growth in a bundle is a decision. app.html, about 1.3 MB without
// the Renderer, has a lower cap in maxGrafanaAppSize.
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

// maxGrafanaAppSize caps app.html at 1.5 MiB. It is about 1.3 MB without the Renderer, so a Renderer that is bundled
// into it again (3.7 MB) fails here (#1589).
const maxGrafanaAppSize = 3 << 19

func TestGrafanaAppReadsVizRenderer(t *testing.T) {
	require.Contains(t, grafanaAppHTML, vizRendererURI,
		"app.html reads the Renderer at the URI the server registers; run mise run build-ui")
	require.Less(t, len(grafanaAppHTML), maxGrafanaAppSize, "app.html grew past %d bytes", maxGrafanaAppSize)
}

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
	require.Equal(t, "ui://grafana/viz-renderer-"+hex.EncodeToString(sum[:8])+".js", vizRendererURI,
		"dist/viz-renderer.hash is the hash of dist/viz-renderer.js; run mise run build-ui")

	resources, err := c.ListResources(ctx, nil)
	require.NoError(t, err)
	var listed *mcp.Resource
	for _, resource := range resources.Resources {
		if resource.URI == vizRendererURI {
			listed = resource
		}
	}
	require.NotNil(t, listed)
	require.Equal(t, vizRendererMIMEType, listed.MIMEType)
	require.NotNil(t, listed.Annotations)
	require.Equal(t, []mcp.Role{"user"}, listed.Annotations.Audience, "for the App, not the model")

	result, err := c.ReadResource(ctx, &mcp.ReadResourceParams{URI: vizRendererURI})
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	require.Equal(t, vizRendererMIMEType, result.Contents[0].MIMEType)
	require.Equal(t, vizRendererJS, result.Contents[0].Text)
	require.Equal(t, vizRendererTTLMs, result.TTLMs)
	require.True(t, strings.HasPrefix(vizRendererJS, "var __grafanaVizRenderer="), "a classic script that sets the Renderer global")
	require.Less(t, len(vizRendererJS), maxVizRendererSize, "the Renderer grew past %d bytes", maxVizRendererSize)
}

// A host can pass a read from the App only for a URI that a tool declares, so a tool declares the Renderer (#1654).
func TestVizRendererTool(t *testing.T) {
	listTools := func(t *testing.T, caps *mcp.ClientCapabilities) map[string]*mcp.Tool {
		t.Helper()
		s := mcp.NewServer(&mcp.Implementation{Name: "apps-test", Version: "1"}, nil)
		s.AddTool(
			&mcp.Tool{Name: "other", InputSchema: json.RawMessage(`{"type":"object"}`)},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return NewToolResultText(""), nil
			},
		)
		RegisterVizRendererTool(s)
		ctx := t.Context()
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		go func() { _ = s.Run(ctx, serverTransport) }()
		client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, &mcp.ClientOptions{Capabilities: caps})
		c, err := client.Connect(ctx, clientTransport, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		listed, err := c.ListTools(ctx, nil)
		require.NoError(t, err)
		byName := make(map[string]*mcp.Tool, len(listed.Tools))
		for _, tool := range listed.Tools {
			byName[tool.Name] = tool
		}
		return byName
	}

	t.Run("a client of MCP Apps gets the tool", func(t *testing.T) {
		tools := listTools(t, &mcp.ClientCapabilities{Extensions: map[string]any{AppsExtensionID: map[string]any{}}})
		require.Contains(t, tools, "other")
		tool := tools[vizRendererToolName]
		require.NotNil(t, tool)
		ui, ok := tool.Meta["ui"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, vizRendererURI, ui["resourceUri"], "the URI that the App reads")
		require.Equal(t, []any{"app"}, ui["visibility"], "hidden from the model")
		require.NotNil(t, tool.Annotations)
		require.True(t, tool.Annotations.ReadOnlyHint)
		require.Equal(t, false, *tool.Annotations.DestructiveHint)
		require.Equal(t, false, *tool.Annotations.OpenWorldHint)
	})

	t.Run("a client without MCP Apps does not", func(t *testing.T) {
		tools := listTools(t, &mcp.ClientCapabilities{})
		require.Contains(t, tools, "other")
		require.NotContains(t, tools, vizRendererToolName)
	})
}
