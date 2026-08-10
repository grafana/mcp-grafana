package mcpgrafana

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	appMIMEType            = "text/html;profile=mcp-app"
	PanelViewerResourceURI = "ui://mcp-grafana/panel-viewer.html"

	// UIContentKindDeeplink is the `_meta.ui.kind` value for a Grafana deeplink.
	UIContentKindDeeplink = "deeplink"
)

// WithUIResource attaches a _meta.ui.resourceUri to a tool definition,
// linking it to an MCP App HTML resource for inline rendering.
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

// NewUIContentMeta builds an *mcp.Meta that sets `_meta.ui.kind = kind`
// on a tool-result content item. Use the UIContentKind* constants.
func NewUIContentMeta(kind string) *mcp.Meta {
	return &mcp.Meta{
		AdditionalFields: map[string]any{
			"ui": map[string]any{
				"kind": kind,
			},
		},
	}
}

// RegisterAppResources registers MCP App UI resources with the server.
func RegisterAppResources(s *server.MCPServer) {
	s.AddResource(
		mcp.NewResource(
			PanelViewerResourceURI,
			"Panel Viewer",
			mcp.WithResourceDescription("Interactive HTML viewer for Grafana panel images"),
			mcp.WithMIMEType(appMIMEType),
		),
		func(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      PanelViewerResourceURI,
					MIMEType: appMIMEType,
					Text:     panelViewerAppHTML,
				},
			}, nil
		},
	)
}
