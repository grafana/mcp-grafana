package mcpgrafana

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	appMIMEType            = "text/html;profile=mcp-app"
	PanelViewerResourceURI = "ui://mcp-grafana/panel-viewer.html"
	PanelEmbedResourceURI  = "ui://mcp-grafana/panel-embed.html"

	// UIContentKindDeeplink is the `_meta.ui.kind` value for a Grafana deeplink.
	UIContentKindDeeplink = "deeplink"

	// UIContentKindPanelQuery marks the content item carrying a run_panel_query
	// payload. An app locates it structurally instead of parsing whichever text
	// block happens to come first, which is what lets the result also carry a
	// short summary written for the model.
	UIContentKindPanelQuery = "panel-query"
)

// uiExtensionID is the MCP Apps extension a host negotiates at initialize when it can
// render an app. Declared in the ext-apps specification.
const uiExtensionID = "io.modelcontextprotocol/ui"

// HostRendersApps reports whether this session's client said it can render MCP Apps.
//
// Worth asking rather than assuming, because the alternative is to hand every host the
// data an app would have drawn. `annotations.audience` looks like it should cover this
// and does not: it is advisory, and Claude Code ignores it - a payload addressed to the
// user still lands in the model's tool result, where a large one gets spilled to a file
// that the agent then picks apart with jq. Capability negotiation is the one signal a
// server can actually act on.
func HostRendersApps(ctx context.Context) bool {
	session, ok := server.ClientSessionFromContext(ctx).(server.SessionWithClientInfo)
	if !ok {
		return false
	}

	return AdvertisesUIExtension(session.GetClientCapabilities())
}

// AdvertisesUIExtension is the decision itself, split out so it can be tested without
// standing up a session.
func AdvertisesUIExtension(capabilities mcp.ClientCapabilities) bool {
	_, declared := capabilities.Extensions[uiExtensionID]

	return declared
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

// staticAppResource serves an embedded MCP App HTML bundle at uri.
func staticAppResource(uri, html string) server.ResourceHandlerFunc {
	return func(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      uri,
				MIMEType: appMIMEType,
				Text:     html,
			},
		}, nil
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
		staticAppResource(PanelViewerResourceURI, panelViewerAppHTML),
	)

	s.AddResource(
		mcp.NewResource(
			PanelEmbedResourceURI,
			"Grafana Panels",
			mcp.WithResourceDescription("Live Grafana panels rendered with Grafana's own visualization pipeline, with a time-range control"),
			mcp.WithMIMEType(appMIMEType),
		),
		staticAppResource(PanelEmbedResourceURI, panelEmbedAppHTML),
	)
}
