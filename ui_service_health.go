package mcpgrafana

import (
	"context"
	_ "embed"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ServiceHealthResourceURI identifies the embedded grafana service health app.
const ServiceHealthResourceURI = "ui://grafana/service-health.html"

//go:embed ui/mcp-apps/dist/service-health.html
var serviceHealthAppHTML string

// RegisterServiceHealthAppResource makes the self-contained grafana service health UI available
// to MCP hosts. Call RegisterAppResources to register every bundled app instead.
func RegisterServiceHealthAppResource(s *server.MCPServer) {
	resource := mcp.NewResource(ServiceHealthResourceURI, "Grafana service health",
		mcp.WithResourceTitle("Grafana service health"),
		mcp.WithResourceDescription("Service RED metrics and outbound dependencies"),
		mcp.WithMIMEType(appMIMEType),
		mcp.WithResourceSize(int64(len(serviceHealthAppHTML))),
	)
	resource.Meta = &mcp.Meta{AdditionalFields: serviceHealthAppMetadata()}
	s.AddResource(resource, func(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{mcp.TextResourceContents{
			Meta: serviceHealthAppMetadata(), URI: ServiceHealthResourceURI, MIMEType: appMIMEType, Text: serviceHealthAppHTML,
		}}, nil
	})
}

func serviceHealthAppMetadata() map[string]any {
	ui := map[string]any{"csp": map[string]any{
		"connectDomains": []string{}, "resourceDomains": []string{},
	}}
	return map[string]any{"ui": ui}
}
