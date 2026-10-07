package mcpgrafana

import (
	"context"
	_ "embed"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MetricsViewerResourceURI identifies the embedded grafana metrics app.
const MetricsViewerResourceURI = "ui://grafana/metrics.html"

//go:embed ui/mcp-apps/dist/metrics.html
var metricsViewerAppHTML string

// RegisterMetricsAppResource makes the self-contained grafana metrics UI
// available to MCP hosts. Call RegisterAppResources to register every bundled
// app instead.
func RegisterMetricsAppResource(s *mcp.Server) {
	s.AddResource(
		&mcp.Resource{
			Meta:        metricsAppMetadata(),
			URI:         MetricsViewerResourceURI,
			Name:        "Grafana metrics",
			Title:       "Grafana metrics",
			Description: "Interactive chart for a PromQL query result: time series, ranked bars, gauge, or stat, chosen from the shape of the response",
			MIMEType:    appMIMEType,
			Size:        int64(len(metricsViewerAppHTML)),
		},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					Meta: metricsAppMetadata(), URI: MetricsViewerResourceURI, MIMEType: appMIMEType, Text: metricsViewerAppHTML,
				}},
			}, nil
		},
	)
}

func metricsAppMetadata() mcp.Meta {
	// The app renders the tool's own result and reaches no network: charts are
	// drawn to a canvas, fonts and styles are inlined by the single-file build.
	return mcp.Meta{"ui": map[string]any{"csp": map[string]any{
		"connectDomains": []string{}, "resourceDomains": []string{"data:"},
	}}}
}
