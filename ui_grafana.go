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

// grafanaAppDatasourceHTML is the same App with the Grafana ECharts panel
// renderer embedded, for the any-datasource view. It is served only when that
// view is enabled, so the metrics and trace views do not load the renderer.
//
//go:embed ui/mcp-apps/dist/app-datasource.html
var grafanaAppDatasourceHTML string

type appResourceConfig struct {
	datasourceView bool
}

// AppResourceOption configures the App resources.
type AppResourceOption func(*appResourceConfig)

// WithDatasourceView serves the App build that can display any datasource
// query. Use it when the query_datasource source is passed to AddAppTool.
func WithDatasourceView() AppResourceOption {
	return func(c *appResourceConfig) { c.datasourceView = true }
}

// RegisterGrafanaAppResource makes the shared viewer available to MCP hosts.
func RegisterGrafanaAppResource(s *mcp.Server, opts ...AppResourceOption) {
	var cfg appResourceConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	html := grafanaAppHTML
	if cfg.datasourceView {
		html = grafanaAppDatasourceHTML
	}
	s.AddResource(
		&mcp.Resource{
			Meta:        grafanaAppMetadata(),
			URI:         GrafanaAppResourceURI,
			Name:        "Grafana App",
			Title:       "Grafana App",
			Description: "Interactive metrics charts and trace waterfall",
			MIMEType:    appMIMEType,
			Size:        int64(len(html)),
		},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					Meta: grafanaAppMetadata(), URI: GrafanaAppResourceURI, MIMEType: appMIMEType, Text: html,
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
