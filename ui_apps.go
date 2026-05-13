package mcpgrafana

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	appMIMEType = "text/html;profile=mcp-app"

	linkIframeResourceURI = "ui://mcp-grafana/link-iframe.html"
	LinkIframeResourceURI = linkIframeResourceURI
)

// WithUIResource returns a ToolOption that associates a tool with an MCP App
// UI resource. Hosts that support MCP Apps render the HTML resource inline.
func WithUIResource(resourceURI string) mcp.ToolOption {
	return func(t *mcp.Tool) {
		if t.Meta == nil {
			t.Meta = &mcp.Meta{}
		}
		if t.Meta.AdditionalFields == nil {
			t.Meta.AdditionalFields = make(map[string]any)
		}
		t.Meta.AdditionalFields["ui"] = map[string]any{
			"resourceUri": resourceURI,
		}
	}
}

func appResourceUIMeta() map[string]any {
	return map[string]any{
		"ui": map[string]any{
			"csp": map[string]any{
				// Allow nested iframes for both HTTP and HTTPS links.
				"frameDomains": []string{"https:", "http:"},
			},
		},
	}
}

// RegisterAppResources registers MCP App UI resources with the server.
func RegisterAppResources(s *server.MCPServer) {
	linkResource := mcp.NewResource(
		linkIframeResourceURI,
		"Link Iframe Viewer",
		mcp.WithResourceDescription("Renders a provided HTTP(S) link inside an iframe."),
		mcp.WithMIMEType(appMIMEType),
	)
	linkResource.Meta = &mcp.Meta{AdditionalFields: appResourceUIMeta()}

	s.AddResource(
		linkResource,
		func(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					Meta:     appResourceUIMeta(),
					URI:      linkIframeResourceURI,
					MIMEType: appMIMEType,
					Text:     linkIframeAppHTML,
				},
			}, nil
		},
	)
}
