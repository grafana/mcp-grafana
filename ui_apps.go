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

// AttachUIResource marks a tool *result* as renderable by an app, by setting
// `_meta.ui.resourceUri` on the result itself.
//
// WithUIResource on the tool definition tells a host which resource to fetch;
// it does not say that a particular result should be rendered by it. Hosts that
// visualize tool output themselves will do so unless the result names its app,
// so a tool that wants its own view must call this in addition to
// WithUIResource. Existing `_meta.ui` keys are preserved.
func AttachUIResource(res *mcp.CallToolResult, resourceURI string) {
	if res == nil || resourceURI == "" {
		return
	}
	if res.Meta == nil {
		res.Meta = mcp.Meta{}
	}
	ui, _ := res.Meta["ui"].(map[string]any)
	if ui == nil {
		ui = map[string]any{}
	}
	ui["resourceUri"] = resourceURI
	res.Meta["ui"] = ui
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
	RegisterTraceAppResource(s)
	s.AddResource(
		&mcp.Resource{
			Meta:        panelViewerAppMetadata(),
			URI:         PanelViewerResourceURI,
			Name:        "Panel Viewer",
			Description: "Interactive HTML viewer for Grafana panel images",
			MIMEType:    appMIMEType,
		},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{
						Meta:     panelViewerAppMetadata(),
						URI:      PanelViewerResourceURI,
						MIMEType: appMIMEType,
						Text:     panelViewerAppHTML,
					},
				},
			}, nil
		},
	)
}

func panelViewerAppMetadata() mcp.Meta {
	return mcp.Meta{"ui": map[string]any{"csp": map[string]any{
		"connectDomains": []string{}, "resourceDomains": []string{"data:"},
	}}}
}
