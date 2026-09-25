package mcpgrafana

import (
	"context"
	_ "embed"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TraceViewerResourceURI identifies the embedded grafana trace app.
const TraceViewerResourceURI = "ui://grafana/trace.html"

//go:embed ui/mcp-apps/dist/trace.html
var traceViewerAppHTML string

// RegisterTraceAppResource makes the self-contained grafana trace UI available
// to MCP hosts. Call RegisterAppResources to register every bundled app instead.
func RegisterTraceAppResource(s *server.MCPServer) {
	resource := mcp.NewResource(TraceViewerResourceURI, "Grafana trace",
		mcp.WithResourceTitle("Grafana trace"),
		mcp.WithResourceDescription("Interactive trace waterfall and span details"),
		mcp.WithMIMEType(appMIMEType),
		mcp.WithResourceSize(int64(len(traceViewerAppHTML))),
	)
	resource.Meta = &mcp.Meta{AdditionalFields: traceAppMetadata()}
	s.AddResource(resource, func(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{mcp.TextResourceContents{
			Meta: traceAppMetadata(), URI: TraceViewerResourceURI, MIMEType: appMIMEType, Text: traceViewerAppHTML,
		}}, nil
	})
}

func traceAppMetadata() map[string]any {
	ui := map[string]any{"csp": map[string]any{
		"connectDomains": []string{}, "resourceDomains": []string{},
	}}
	ui["permissions"] = map[string]any{"clipboardWrite": map[string]any{}}
	return map[string]any{"ui": ui}
}
