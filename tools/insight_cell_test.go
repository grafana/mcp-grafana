package tools

import (
	"context"
	"encoding/json"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderInsightCellToolMeta(t *testing.T) {
	tool := RenderInsightCell.Tool
	require.NotNil(t, tool.Meta, "render_insight_cell should have _meta for MCP Apps")
	require.NotNil(t, tool.Meta.AdditionalFields)

	ui, ok := tool.Meta.AdditionalFields["ui"].(map[string]any)
	require.True(t, ok, "expected _meta.ui to be a map")
	assert.Equal(t, mcpgrafana.InsightCellResourceURI, ui["resourceUri"])
}

// decodeCellPayload finds the embedded application/json resource block and
// unmarshals it back into a generic map — the channel Claude Desktop keeps.
func decodeCellPayload(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.GreaterOrEqual(t, len(res.Content), 2, "expected text + resource content items")

	_, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok, "first content item should be the text verdict")

	embedded, ok := res.Content[1].(mcp.EmbeddedResource)
	require.True(t, ok, "second content item should be an embedded resource")
	rc, ok := embedded.Resource.(mcp.TextResourceContents)
	require.True(t, ok)
	assert.Equal(t, "application/json", rc.MIMEType)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(rc.Text), &payload))
	return payload
}

func TestRenderInsightCellStat(t *testing.T) {
	res, err := renderInsightCell(context.Background(), RenderInsightCellParams{
		Panel:   "stat",
		Title:   "Error rate",
		Verdict: "Error rate is nominal",
		Insight: "Errors held under 1% across the window.",
		Unit:    "percent",
		Frames: []icDataFrame{{
			Fields: []icField{{Name: "value", Type: "number", Values: []any{0.42}}},
		}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// structuredContent carries the typed cell.
	cell, ok := res.StructuredContent.(*insightCell)
	require.True(t, ok, "structuredContent should be an *insightCell")
	assert.Equal(t, "stat", cell.RenderHint.Type)
	assert.Equal(t, "percent", cell.RenderHint.Unit)
	assert.Equal(t, "mock", cell.Meta.DataMode, "no query -> sample/mock data mode")
	assert.False(t, cell.Meta.Attestation.Live)

	// _meta carries the resource URI and the trust profile.
	require.NotNil(t, res.Result.Meta)
	fields := res.Result.Meta.AdditionalFields
	ui, ok := fields["ui"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, mcpgrafana.InsightCellResourceURI, ui["resourceUri"])
	trust, ok := fields[insightCellMetaKey].(map[string]any)
	require.True(t, ok, "expected the %s trust block", insightCellMetaKey)
	reasoning, ok := trust["reasoning"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "agent", reasoning["source"])
	assert.Equal(t, "Error rate is nominal", reasoning["verdict"])

	// The embedded JSON resource round-trips to a cell.
	payload := decodeCellPayload(t, res)
	assert.Contains(t, payload, "renderHint")
}

func TestRenderInsightCellWorklistLive(t *testing.T) {
	res, err := renderInsightCell(context.Background(), RenderInsightCellParams{
		Panel:         "worklist",
		Verdict:       "1 needs action now",
		Query:         `ALERTS{alertstate="firing"}`,
		DatasourceUID: "prom-uid",
		Items: []icWorklistItem{
			{Title: "HighErrorRate", Priority: "critical", Status: "firing 22m", Why: "Sustained and climbing."},
		},
	})
	require.NoError(t, err)

	cell, ok := res.StructuredContent.(*insightCell)
	require.True(t, ok)
	assert.Equal(t, "worklist", cell.RenderHint.Type)
	require.Len(t, cell.Worklist, 1)
	assert.Equal(t, "HighErrorRate", cell.Worklist[0].Title)

	// A query + datasource -> live attestation and a recorded query.
	assert.Equal(t, "live", cell.Meta.DataMode)
	assert.True(t, cell.Meta.Attestation.Live)
	require.Len(t, cell.Meta.Query, 1)
	assert.Equal(t, `ALERTS{alertstate="firing"}`, cell.Meta.Query[0].Expr)
	assert.Equal(t, "prom-uid", cell.Meta.Query[0].DatasourceUID)

	// actions is always a (possibly empty) array, never null, so the UI can iterate.
	assert.NotNil(t, cell.Actions)
}

func TestRenderInsightCellBullet(t *testing.T) {
	target := 1.0
	max := 1.2
	res, err := renderInsightCell(context.Background(), RenderInsightCellParams{
		Panel:   "bullet",
		Verdict: "p95 latency 0.82s vs 1s SLO",
		Unit:    "s",
		Target:  &target,
		Max:     &max,
		Frames: []icDataFrame{{
			Fields: []icField{{Name: "value", Type: "number", Values: []any{0.82}}},
		}},
	})
	require.NoError(t, err)

	cell, ok := res.StructuredContent.(*insightCell)
	require.True(t, ok)
	assert.Equal(t, "bullet", cell.RenderHint.Type)
	require.NotNil(t, cell.RenderHint.Target)
	assert.Equal(t, 1.0, *cell.RenderHint.Target)
	require.NotNil(t, cell.RenderHint.Max)
	assert.Equal(t, 1.2, *cell.RenderHint.Max)
}

func TestRenderInsightCellSurfacesInsight(t *testing.T) {
	insight := "Errors held under 1% across the window; no action needed."
	res, err := renderInsightCell(context.Background(), RenderInsightCellParams{
		Panel:   "stat",
		Verdict: "Nominal",
		Insight: insight,
		Frames:  []icDataFrame{{Fields: []icField{{Name: "value", Type: "number", Values: []any{0.4}}}}},
	})
	require.NoError(t, err)

	cell := res.StructuredContent.(*insightCell)
	assert.Equal(t, insight, cell.RenderHint.Description, "insight rides on renderHint.description")

	text := res.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, insight, "insight must appear in the text fallback")
}

func TestRenderInsightCellNilArraysDefaultToEmpty(t *testing.T) {
	// rca with a root cause but no findings — findings must serialize as [] not null.
	res, err := renderInsightCell(context.Background(), RenderInsightCellParams{
		Panel:     "rca",
		RootCause: &icRcaRoot{Title: "Bad deploy", Confidence: "high"},
	})
	require.NoError(t, err)
	payload := decodeCellPayload(t, res)

	rca, ok := payload["rca"].(map[string]any)
	require.True(t, ok)
	findings, ok := rca["findings"].([]any)
	require.True(t, ok, "findings must be a JSON array, not null")
	assert.Empty(t, findings)
}

func TestRenderInsightCellKeepsZeroRangeBound(t *testing.T) {
	zero := 0.0
	five := 5.0
	res, err := renderInsightCell(context.Background(), RenderInsightCellParams{
		Panel:    "stat",
		Mappings: []icValueMapping{{Type: "range", From: &zero, To: &five, Text: "low"}},
		Frames:   []icDataFrame{{Fields: []icField{{Name: "value", Type: "number", Values: []any{2.0}}}}},
	})
	require.NoError(t, err)
	payload := decodeCellPayload(t, res)

	mappings := payload["renderHint"].(map[string]any)["mappings"].([]any)
	require.Len(t, mappings, 1)
	m := mappings[0].(map[string]any)
	from, ok := m["from"]
	require.True(t, ok, "a range bound of 0 must not be dropped")
	assert.Equal(t, 0.0, from)
}

func TestRenderInsightCellKeepsZeroCostMetric(t *testing.T) {
	zero := 0.0
	res, err := renderInsightCell(context.Background(), RenderInsightCellParams{
		Panel:   "cost",
		Drivers: []icCostDriver{{Name: "idle_series", Pct: &zero, Series: &zero}},
	})
	require.NoError(t, err)
	payload := decodeCellPayload(t, res)

	d := payload["cost"].(map[string]any)["drivers"].([]any)[0].(map[string]any)
	pct, ok := d["pct"]
	require.True(t, ok, "a 0%% cost share must not be dropped")
	assert.Equal(t, 0.0, pct)
}

func TestRenderInsightCellTimelineDefaultsBounds(t *testing.T) {
	res, err := renderInsightCell(context.Background(), RenderInsightCellParams{
		Panel:  "timeline",
		Events: []icChangeEvent{{Time: "2026-07-23T10:00:00Z", Title: "deploy v2", Kind: "deploy"}},
		// from/to intentionally omitted
	})
	require.NoError(t, err)

	cell := res.StructuredContent.(*insightCell)
	require.NotNil(t, cell.Timeline)
	assert.NotEmpty(t, cell.Timeline.From, "timeline.from must default to the computed range, not empty")
	assert.NotEmpty(t, cell.Timeline.To, "timeline.to must default to the computed range, not empty")
}

func TestRenderInsightCellRequiresPanel(t *testing.T) {
	_, err := renderInsightCell(context.Background(), RenderInsightCellParams{})
	require.Error(t, err)
}
