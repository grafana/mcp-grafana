package mcpgrafana

import (
	"context"
	_ "embed"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GrafanaAppResourceURI identifies the shared metrics and trace viewer.
const GrafanaAppResourceURI = "ui://grafana/app.html"

//go:embed ui/mcp-apps/dist/app.html
var grafanaAppHTML string

//go:embed ui/mcp-apps/dist/viz-renderer.js
var vizRendererJS string

// vizRendererHash is the first 16 hex digits of the sha256 of vizRendererJS. The UI build writes it, so the server
// does not hash the script at start.
//
//go:embed ui/mcp-apps/dist/viz-renderer.hash
var vizRendererHash string

// VizRendererResourceURI identifies the viz Renderer script that the Grafana App reads to draw query_datasource
// results (#1589). It holds a hash of the script, so a new build gets a new URI and a host never runs a cached old one.
var VizRendererResourceURI = "ui://grafana/viz-renderer-" + strings.TrimSpace(vizRendererHash) + ".js"

const vizRendererMIMEType = "text/javascript"

// vizRendererTTLMs is the cache hint on a read of the Renderer: 7 days. The URI changes with the content, so a host
// can keep it that long.
const vizRendererTTLMs = 7 * 24 * 60 * 60 * 1000

// RegisterGrafanaAppResource makes the shared viewer and its viz Renderer available to MCP hosts.
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
	s.AddResource(
		&mcp.Resource{
			URI:         VizRendererResourceURI,
			Name:        "Grafana App viz Renderer",
			Title:       "Grafana App viz Renderer",
			Description: "Script that the Grafana App loads to draw query_datasource results. It is not for the chat.",
			MIMEType:    vizRendererMIMEType,
			Size:        int64(len(vizRendererJS)),
		},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Cacheable: mcp.Cacheable{TTLMs: vizRendererTTLMs, CacheScope: "public"},
				Contents: []*mcp.ResourceContents{{
					URI: VizRendererResourceURI, MIMEType: vizRendererMIMEType, Text: vizRendererJS,
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
