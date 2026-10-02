package mcpgrafana

import (
	"context"
	_ "embed"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TraceViewerResourceURI identifies the embedded grafana trace app.
const TraceViewerResourceURI = "ui://grafana/trace.html"

//go:embed ui/mcp-apps/dist/trace.html
var traceViewerAppHTML string

// RegisterTraceAppResource makes the self-contained grafana trace UI available
// to MCP hosts. Call RegisterAppResources to register every bundled app instead.
func RegisterTraceAppResource(s *mcp.Server) {
	s.AddResource(
		&mcp.Resource{
			Meta:        traceAppMetadata(),
			URI:         TraceViewerResourceURI,
			Name:        "Grafana trace",
			Title:       "Grafana trace",
			Description: "Interactive trace waterfall and span details",
			MIMEType:    appMIMEType,
			Size:        int64(len(traceViewerAppHTML)),
		},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					Meta: traceAppMetadata(), URI: TraceViewerResourceURI, MIMEType: appMIMEType, Text: traceViewerAppHTML,
				}},
			}, nil
		},
	)
}

func traceAppMetadata() mcp.Meta {
	ui := map[string]any{"csp": map[string]any{
		"connectDomains": []string{}, "resourceDomains": []string{"data:"},
	}}
	ui["permissions"] = map[string]any{"clipboardWrite": map[string]any{}}
	return mcp.Meta{"ui": ui}
}
