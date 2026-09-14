package mcpgrafana

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	appMIMEType            = "text/html;profile=mcp-app"
	PanelViewerResourceURI = "ui://mcp-grafana/panel-viewer.html"

	// UIContentKindDeeplink is the `_meta.ui.kind` value for a Grafana deeplink.
	UIContentKindDeeplink = "deeplink"
)

// WithUIResource attaches a _meta.ui.resourceUri to a tool definition,
// linking it to an MCP App HTML resource for inline rendering.
func WithUIResource(resourceURI string) ToolOption {
	return func(t *mcp.Tool) {
		if t.Meta == nil {
			t.Meta = mcp.Meta{}
		}
		t.Meta["ui"] = map[string]any{
			"resourceUri": resourceURI,
		}
	}
}

// NewUIContentMeta builds an mcp.Meta that sets `_meta.ui.kind = kind`
// on a tool-result content item. Use the UIContentKind* constants.
func NewUIContentMeta(kind string) mcp.Meta {
	return mcp.Meta{
		"ui": map[string]any{
			"kind": kind,
		},
	}
}

// RegisterAppResources registers MCP App UI resources with the server.
func RegisterAppResources(s *mcp.Server) {
	s.AddResource(
		&mcp.Resource{
			URI:         PanelViewerResourceURI,
			Name:        "Panel Viewer",
			Description: "Interactive HTML viewer for Grafana panel images",
			MIMEType:    appMIMEType,
		},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{
						URI:      PanelViewerResourceURI,
						MIMEType: appMIMEType,
						Text:     panelViewerAppHTML,
					},
				},
			}, nil
		},
	)
}
