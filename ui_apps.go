package mcpgrafana

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	appMIMEType = "text/html;profile=mcp-app"

	// PanelViewerResourceURI is the MCP App that renders get_panel_image results.
	PanelViewerResourceURI = "ui://mcp-grafana/panel-viewer.html"

	// UIContentKindDeeplink is the `_meta.ui.kind` value for a Grafana deeplink.
	UIContentKindDeeplink = "deeplink"
)

// UIApp describes an MCP App HTML resource that the server serves via the MCP
// Resources API. A tool links itself to one with WithUIResource(app.URI); a
// host that supports the MCP Apps extension then fetches the resource and
// renders it in a sandboxed iframe. Adding a new app is one entry in
// appResources plus a //go:embed line (see ui_embed.go) — not a new code path.
type UIApp struct {
	// URI is the ui:// resource URI referenced from a tool's _meta.ui.resourceUri.
	URI string
	// Name is the human-readable resource name shown in resource listings.
	Name string
	// Description explains what the app renders.
	Description string
	// HTML is the self-contained (single-file) app bundle, embedded at build time.
	HTML string
}

// appResources is the registry of MCP App UI resources served by the server.
// Each built bundle is embedded in ui_embed.go and listed here once.
var appResources = []UIApp{
	{
		URI:         PanelViewerResourceURI,
		Name:        "Panel Viewer",
		Description: "Interactive HTML viewer for Grafana panel images",
		HTML:        panelViewerAppHTML,
	},
}

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

// RegisterAppResources registers every MCP App UI resource in the appResources
// registry with the server.
func RegisterAppResources(s *server.MCPServer) {
	for _, app := range appResources {
		app := app // capture per-iteration for the closure
		s.AddResource(
			mcp.NewResource(
				app.URI,
				app.Name,
				mcp.WithResourceDescription(app.Description),
				mcp.WithMIMEType(appMIMEType),
			),
			func(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
				return []mcp.ResourceContents{
					mcp.TextResourceContents{
						URI:      app.URI,
						MIMEType: appMIMEType,
						Text:     app.HTML,
					},
				}, nil
			},
		)
	}
}
