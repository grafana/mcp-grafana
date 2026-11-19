package mcpgrafana

import (
	"context"
	_ "embed"
	"encoding/json"
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

// vizRendererURI identifies the viz Renderer script that the Grafana App reads to draw query_datasource results
// (#1589). It holds a hash of the script, so a new build gets a new URI and a host never runs a cached old one.
var vizRendererURI = "ui://grafana/viz-renderer-" + strings.TrimSpace(vizRendererHash) + ".js"

const vizRendererMIMEType = "text/javascript"

// vizRendererTTLMs is the cache hint on a read of the Renderer: 7 days. The URI changes with the content, so a host
// can keep it that long.
const vizRendererTTLMs = 7 * 24 * 60 * 60 * 1000

// RegisterGrafanaAppResource makes the shared viewer available to MCP hosts, with the viz Renderer script it reads to
// draw query_datasource results.
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
	registerVizRendererResource(s)
}

// registerVizRendererResource makes the viz Renderer script available to the Grafana App. It is registered with the App,
// so an embedder that serves the App cannot leave it out (#1589).
func registerVizRendererResource(s *mcp.Server) {
	s.AddResource(
		&mcp.Resource{
			// For the App, not the model: a host that sends resources to the model can leave this 3.7 MB script out.
			Annotations: &mcp.Annotations{Audience: []mcp.Role{"user"}},
			URI:         vizRendererURI,
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
					URI: vizRendererURI, MIMEType: vizRendererMIMEType, Text: vizRendererJS,
				}},
			}, nil
		},
	)
}

// AppsExtensionID is the client capability of MCP Apps (EXTENSION_ID of ext-apps).
const AppsExtensionID = "io.modelcontextprotocol/ui"

// vizRendererToolName names the tool that declares the viz Renderer as its UI resource.
const vizRendererToolName = "grafana_app_viz_renderer"

// RegisterVizRendererTool adds a tool whose only job is to declare the viz Renderer as its UI resource (#1654). A
// host can pass a resources/read from the App to the server only for a URI that a tool declares in
// _meta.ui.resourceUri, and the tool of the App declares app.html. The tool is for the App alone (visibility), so a
// host of MCP Apps hides it from the model. tools/list leaves it out for a client that cannot show MCP Apps.
//
// Only the query_datasource view reads the Renderer, so tools.AddAppTool and tools.Selection.Register call this
// when query_datasource is a source of the App tool. A server without that source does not get the tool.
func RegisterVizRendererTool(s *mcp.Server) {
	no := false
	s.AddTool(
		&mcp.Tool{
			Name:        vizRendererToolName,
			Title:       "Grafana App viz Renderer",
			Description: "Declares the chart script of the Grafana App as a UI resource, so that a host lets the App read it. It is for the App, not for the chat, and it returns no data.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &no, OpenWorldHint: &no,
			},
			Meta: mcp.Meta{"ui": map[string]any{"resourceUri": vizRendererURI, "visibility": []string{"app"}}},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return NewToolResultText("The Grafana App reads its chart script from the UI resource of this tool. The tool returns no data."), nil
		},
	)
	s.AddReceivingMiddleware(hideVizRendererTool)
}

// hideVizRendererTool takes the Renderer tool out of tools/list for a client that does not list the MCP Apps
// extension: that client has no App to read the script, and would show the tool to the model. Unknown capabilities,
// as in a stateless request of an older protocol, keep the tool.
func hideVizRendererTool(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, req)
		list, ok := result.(*mcp.ListToolsResult)
		if err != nil || !ok {
			return result, err
		}
		listReq, ok := req.(*mcp.ListToolsRequest)
		if !ok {
			return result, nil
		}
		caps := listReq.ClientCapabilities()
		if caps == nil {
			return result, nil
		}
		if _, apps := caps.Extensions[AppsExtensionID]; apps {
			return result, nil
		}
		// A copy: the result of the next handler is not ours to change in place.
		filtered := *list
		filtered.Tools = make([]*mcp.Tool, 0, len(list.Tools))
		for _, tool := range list.Tools {
			if tool.Name != vizRendererToolName {
				filtered.Tools = append(filtered.Tools, tool)
			}
		}
		return &filtered, nil
	}
}

func grafanaAppMetadata() mcp.Meta {
	ui := map[string]any{"csp": map[string]any{
		"connectDomains": []string{}, "resourceDomains": []string{"data:"},
	}}
	ui["permissions"] = map[string]any{"clipboardWrite": map[string]any{}}
	return mcp.Meta{"ui": ui}
}
