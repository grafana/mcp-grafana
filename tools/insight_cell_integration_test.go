// Protocol-level integration test for render_insight_cell and its MCP App
// resource. Unlike the other integration tests this needs no external services:
// the tool is a render substrate (it repackages agent-supplied data), so the
// whole path — tools/call over MCP for every panel type, the three output
// channels, the trust _meta, and resources/read of the embedded UI bundle — is
// exercised against an in-process server.
//go:build integration
// +build integration

package tools

import (
	"context"
	"encoding/json"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newInsightCellClient(t *testing.T) *client.Client {
	t.Helper()

	s := server.NewMCPServer("mcp-grafana-test", "0.0.0",
		server.WithResourceCapabilities(false, true),
	)
	AddInsightCellTools(s)
	mcpgrafana.RegisterAppResources(s)

	c, err := client.NewInProcessClient(s)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	require.NoError(t, c.Start(ctx))

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "insight-cell-integration-test", Version: "0.0.0"}
	_, err = c.Initialize(ctx, initReq)
	require.NoError(t, err)

	return c
}

func callRenderInsightCell(t *testing.T, c *client.Client, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = "render_insight_cell"
	req.Params.Arguments = args
	res, err := c.CallTool(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, "tool call errored: %+v", res.Content)
	return res
}

// cellFromResult decodes the embedded application/json resource block — the
// channel hosts keep when they drop structuredContent.
func cellFromResult(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.GreaterOrEqual(t, len(res.Content), 2, "expected text + embedded resource content")

	embedded, ok := res.Content[1].(mcp.EmbeddedResource)
	require.True(t, ok, "content[1] should be an embedded resource, got %T", res.Content[1])
	trc, ok := embedded.Resource.(mcp.TextResourceContents)
	require.True(t, ok, "embedded resource should carry text contents, got %T", embedded.Resource)
	assert.Equal(t, "application/json", trc.MIMEType)

	var cell map[string]any
	require.NoError(t, json.Unmarshal([]byte(trc.Text), &cell))
	return cell
}

func renderHintType(t *testing.T, cell map[string]any) string {
	t.Helper()
	hint, ok := cell["renderHint"].(map[string]any)
	require.True(t, ok, "cell should have a renderHint object")
	typ, _ := hint["type"].(string)
	return typ
}

// TestInsightCellIntegrationPanelTypes drives every panel type over the
// protocol and checks the three output channels plus the trust _meta.
func TestInsightCellIntegrationPanelTypes(t *testing.T) {
	c := newInsightCellClient(t)

	timeFrame := map[string]any{"fields": []any{
		map[string]any{"name": "time", "type": "time", "values": []any{1700000000000, 1700000060000, 1700000120000}},
		map[string]any{"name": "p99", "type": "number", "values": []any{0.21, 0.34, 0.29}},
	}}

	cases := map[string]map[string]any{
		"stat": {
			"frames": []any{map[string]any{"fields": []any{
				map[string]any{"name": "value", "type": "number", "values": []any{0.42}},
			}}},
			"unit": "percent",
		},
		"timeseries": {"frames": []any{timeFrame}},
		"bullet": {
			"frames": []any{map[string]any{"fields": []any{
				map[string]any{"name": "value", "type": "number", "values": []any{0.91}},
			}}},
			"target": 0.99,
		},
		"bar": {
			"frames": []any{map[string]any{"fields": []any{
				map[string]any{"name": "service", "type": "string", "values": []any{"api", "web"}},
				map[string]any{"name": "errors", "type": "number", "values": []any{14, 3}},
			}}},
			"sort": "desc",
		},
		"table": {"frames": []any{timeFrame}},
		"logs": {"logs": []any{
			map[string]any{"time": "2026-07-27T10:00:00Z", "level": "error", "line": "connection refused"},
		}},
		"trace": {"trace": map[string]any{
			"traceId": "abc123", "durationMs": 120.0,
			"spans": []any{map[string]any{"id": "s1", "name": "GET /", "service": "api", "startMs": 0.0, "durationMs": 120.0}},
		}},
		"worklist": {"items": []any{
			map[string]any{"title": "Silence flapping alert", "priority": "high", "why": "fired 41 times in 6h"},
		}},
		"rca": {
			"rootCause": map[string]any{"title": "Pod OOM", "confidence": "high"},
			"findings":  []any{map[string]any{"title": "restarts spiked", "evidence": "kube_pod_container_status_restarts_total"}},
		},
		"timeline": {"events": []any{
			map[string]any{"time": "2026-07-27T09:30:00Z", "title": "deploy api v2", "kind": "deploy", "correlated": true},
		}},
		"cost": {
			"drivers":   []any{map[string]any{"name": "http_requests_total", "series": 40000}},
			"costTotal": map[string]any{"label": "1.2M active series", "value": 1200000},
		},
	}

	for panel, args := range cases {
		t.Run(panel, func(t *testing.T) {
			args["panel"] = panel
			args["verdict"] = "verdict for " + panel
			res := callRenderInsightCell(t, c, args)

			// Channel 1: the text verdict fallback.
			text, ok := res.Content[0].(mcp.TextContent)
			require.True(t, ok, "content[0] should be text, got %T", res.Content[0])
			assert.Contains(t, text.Text, "verdict for "+panel)

			// Channel 2: the embedded JSON resource block.
			cell := cellFromResult(t, res)
			assert.Equal(t, panel, renderHintType(t, cell))

			// Channel 3: structuredContent.
			sc, ok := res.StructuredContent.(map[string]any)
			require.True(t, ok, "structuredContent should be an object, got %T", res.StructuredContent)
			assert.Equal(t, panel, renderHintType(t, sc))

			// _meta: the app resource URI and the trust profile.
			require.NotNil(t, res.Meta)
			ui, ok := res.Meta.AdditionalFields["ui"].(map[string]any)
			require.True(t, ok, "_meta.ui should be present")
			assert.Equal(t, mcpgrafana.InsightCellResourceURI, ui["resourceUri"])
			trust, ok := res.Meta.AdditionalFields["grafana.insightCell/v0"].(map[string]any)
			require.True(t, ok, "_meta should carry the grafana.insightCell/v0 trust profile")
			reasoning, ok := trust["reasoning"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "agent", reasoning["source"])

			// No query supplied -> synthesized data mode.
			meta, ok := cell["meta"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "synthesized", meta["dataMode"])
		})
	}
}

// TestInsightCellIntegrationLiveDataMode checks that supplying a query and a
// datasource UID flips the attestation to live.
func TestInsightCellIntegrationLiveDataMode(t *testing.T) {
	c := newInsightCellClient(t)

	res := callRenderInsightCell(t, c, map[string]any{
		"panel":   "stat",
		"verdict": "error rate is nominal",
		"frames": []any{map[string]any{"fields": []any{
			map[string]any{"name": "value", "type": "number", "values": []any{0.003}},
		}}},
		"query":         `sum(rate(errors_total[5m]))`,
		"datasourceUid": "prom-uid",
	})

	meta, ok := cellFromResult(t, res)["meta"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "agent-supplied", meta["dataMode"])
	attestation, ok := meta["attestation"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, attestation["live"])
	queries, ok := meta["query"].([]any)
	require.True(t, ok)
	require.Len(t, queries, 1)
}

// TestInsightCellIntegrationReadOnly asserts the read-only contract at the
// protocol boundary: the tool is annotated read-only, and action kinds that
// could reach other tools are stripped before the cell is emitted.
func TestInsightCellIntegrationReadOnly(t *testing.T) {
	c := newInsightCellClient(t)
	ctx := context.Background()

	// The tool advertises ReadOnlyHint.
	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	require.NoError(t, err)
	var found bool
	for _, tool := range tools.Tools {
		if tool.Name == "render_insight_cell" {
			found = true
			require.NotNil(t, tool.Annotations.ReadOnlyHint)
			assert.True(t, *tool.Annotations.ReadOnlyHint)
		}
	}
	require.True(t, found, "render_insight_cell should be listed")

	// Actions with a kind that isn't link/refresh/ask never reach the cell.
	res := callRenderInsightCell(t, c, map[string]any{
		"panel":   "worklist",
		"verdict": "one alert needs action now",
		"items":   []any{map[string]any{"title": "Silence flapping alert", "priority": "high"}},
		"actions": []any{
			map[string]any{"label": "Apply via tool", "kind": "tool"},
			map[string]any{"label": "Runbook", "kind": "link", "url": "https://example.com/runbook"},
			map[string]any{"label": "Silence it", "kind": "ask", "text": "Silence the flapping alert via the appropriate write-gated tool."},
		},
	})

	cell := cellFromResult(t, res)
	actions, ok := cell["actions"].([]any)
	require.True(t, ok, "actions should be an array")
	require.Len(t, actions, 2, "the kind:tool action should be stripped")
	for _, a := range actions {
		kind, _ := a.(map[string]any)["kind"].(string)
		assert.Contains(t, []string{"link", "refresh", "ask"}, kind)
	}
}

// TestInsightCellIntegrationAppResource reads the embedded MCP App bundle over
// the Resources API — what a host does after seeing _meta.ui.resourceUri.
func TestInsightCellIntegrationAppResource(t *testing.T) {
	c := newInsightCellClient(t)

	req := mcp.ReadResourceRequest{}
	req.Params.URI = mcpgrafana.InsightCellResourceURI
	res, err := c.ReadResource(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, res.Contents, 1)

	html, ok := res.Contents[0].(mcp.TextResourceContents)
	require.True(t, ok, "app resource should be text contents, got %T", res.Contents[0])
	assert.Contains(t, html.MIMEType, "text/html")
	assert.Contains(t, html.Text, "<html", "should serve the built single-file bundle")
}
