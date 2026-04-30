package mcpgrafana

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	appMIMEType              = "text/html;profile=mcp-app"
	timeseriesResourceURI    = "ui://mcp-grafana/timeseries.html"
	TimeseriesResourceURI    = timeseriesResourceURI
)

// WithUIResource returns a ToolOption that associates a tool with an MCP App
// UI resource. Hosts that support MCP Apps will render the HTML resource inline
// when the tool is called. Hosts that don't support MCP Apps simply ignore the
// metadata and display the tool's text result as usual.
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

// RegisterAppResources registers all MCP App UI resources with the server.
// Call this during server initialization alongside tool registration.
func RegisterAppResources(s *server.MCPServer) {
	s.AddResource(
		mcp.NewResource(
			timeseriesResourceURI,
			"Time Series Viewer",
			mcp.WithResourceDescription("Interactive time series chart for Prometheus query results"),
			mcp.WithMIMEType(appMIMEType),
		),
		func(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      timeseriesResourceURI,
					MIMEType: appMIMEType,
					Text:     timeseriesAppHTML,
				},
			}, nil
		},
	)
}
