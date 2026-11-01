package mcpgrafana

import (
	"context"
	_ "embed"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GrafanaAppResourceURI identifies the shared metrics and trace viewer.
const GrafanaAppResourceURI = "ui://grafana/app.html"

//go:embed ui/mcp-apps/dist/app.html
var grafanaAppHTML string

// RegisterGrafanaAppResource makes the shared viewer available to MCP hosts.
func RegisterGrafanaAppResource(s *mcp.Server) {
	s.AddResource(
		&mcp.Resource{
			Meta:        grafanaAppMetadata(),
			URI:         GrafanaAppResourceURI,
			Name:        "Grafana App",
			Title:       "Grafana App",
			Description: "Interactive metrics charts and trace waterfall",
			MIMEType:    appMIMEType,
			Size:        int64(len(grafanaAppHTML)),
		},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					Meta: grafanaAppMetadata(), URI: GrafanaAppResourceURI, MIMEType: appMIMEType, Text: grafanaAppHTML,
				}},
			}, nil
		},
	)
}

func grafanaAppMetadata() mcp.Meta {
	ui := map[string]any{"csp": map[string]any{
		"connectDomains": []string{}, "resourceDomains": []string{"data:"},
	}}
	ui["permissions"] = map[string]any{"clipboardWrite": map[string]any{}}
	return mcp.Meta{"ui": ui}
}
