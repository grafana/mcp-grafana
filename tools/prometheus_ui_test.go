package tools

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// query_prometheus declares the metrics app so the host renders that tool's own
// result. Nothing the model writes chooses the visualization: the app derives it
// from the response shape.
func TestQueryPrometheusDeclaresMetricsApp(t *testing.T) {
	require.NotNil(t, QueryPrometheus.Tool.Meta)
	ui, ok := QueryPrometheus.Tool.Meta["ui"].(map[string]any)
	require.True(t, ok, "query_prometheus should carry _meta.ui")
	assert.Equal(t, mcpgrafana.MetricsViewerResourceURI, ui["resourceUri"])
}

// The host must be able to find the payload whichever channel it reads.
func TestMetricsAppResultCarriesEveryHostSignal(t *testing.T) {
	result := &QueryPrometheusResult{
		Data:       model.Vector{{Metric: model.Metric{"__name__": "up"}, Timestamp: 1760000000000, Value: 1}},
		ExploreURL: "https://grafana.example.com/explore?panes=%7B%7D",
	}

	out, err := newMetricsAppResult(result)
	require.NoError(t, err)

	// 1. A text block carrying the JSON — what the model reads, and the only
	//    channel some hosts pass through to the iframe.
	require.Len(t, out.Content, 1)
	text, ok := out.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected a text content block")
	var roundTripped map[string]any
	require.NoError(t, json.Unmarshal([]byte(text.Text), &roundTripped))
	assert.Contains(t, roundTripped, "data")
	assert.Equal(t, result.ExploreURL, roundTripped["exploreUrl"])

	// 2. The same payload as structuredContent.
	assert.Equal(t, result, out.StructuredContent)
}

// Past the view budget the tool keeps its text output and offers no interactive
// view, matching the trace viewer's documented behaviour.
func TestMetricsAppResultDropsTheViewWhenTooLarge(t *testing.T) {
	// One series wide enough to exceed 1 MiB once marshalled.
	values := make([]model.SamplePair, 60_000)
	for i := range values {
		values[i] = model.SamplePair{Timestamp: model.Time(1760000000000 + int64(i)*1000), Value: model.SampleValue(i)}
	}
	big := &QueryPrometheusResult{Data: model.Matrix{{Metric: model.Metric{"__name__": "wide"}, Values: values}}}

	out, err := newMetricsAppResult(big)
	require.NoError(t, err)
	require.Len(t, out.Content, 1, "the text output is always preserved")
	assert.Greater(t, len(out.Content[0].(*mcp.TextContent).Text), maxMetricsViewBytes)
	assert.Nil(t, out.StructuredContent, "no viewer data past the budget")
}

// The Explore link has to carry enough state for the query to reopen as it ran.
func TestExploreURLForQueryCarriesTheQuery(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaClient(t.Context(), &mcpgrafana.GrafanaClient{
		PublicURL: "https://example.grafana.net",
		Version:   "12.1.0",
	})

	ranged := exploreURLForQuery(ctx, QueryPrometheusParams{
		Expr: "node_load1", DatasourceUID: "prom-uid", QueryType: "range",
		StartTime: "now-1h", EndTime: "now",
	})
	require.NotEmpty(t, ranged)
	parsed, err := url.Parse(ranged)
	require.NoError(t, err)
	assert.Equal(t, "https", parsed.Scheme)
	assert.Equal(t, "/explore", parsed.Path)
	// Grafana 10.2+ reads Explore state from `panes`.
	assert.Equal(t, "1", parsed.Query().Get("schemaVersion"))
	panes := parsed.Query().Get("panes")
	assert.Contains(t, panes, `"expr":"node_load1"`)
	assert.Contains(t, panes, `"uid":"prom-uid"`)
	assert.Contains(t, panes, `"range":true`)
	assert.Contains(t, panes, `"from":"now-1h"`)

	instant := exploreURLForQuery(ctx, QueryPrometheusParams{
		Expr: "go_goroutines", DatasourceUID: "prom-uid", QueryType: "instant",
	})
	assert.Contains(t, instant, "instant%22%3Atrue")
}

// Without a resolvable public URL there is no link to offer, and an empty
// string must not become a broken header action.
func TestExploreURLForQueryIsEmptyWithoutAPublicURL(t *testing.T) {
	assert.Empty(t, exploreURLForQuery(t.Context(), QueryPrometheusParams{Expr: "up", DatasourceUID: "p"}))
	ctx := mcpgrafana.WithGrafanaClient(t.Context(), &mcpgrafana.GrafanaClient{PublicURL: "https://example.grafana.net"})
	assert.Empty(t, exploreURLForQuery(ctx, QueryPrometheusParams{Expr: "", DatasourceUID: "p"}))
	assert.Empty(t, exploreURLForQuery(ctx, QueryPrometheusParams{Expr: "up", DatasourceUID: ""}))
}

func TestMetricsAppResultOmitsAbsentExploreURL(t *testing.T) {
	out, err := newMetricsAppResult(&QueryPrometheusResult{Data: model.Vector{}})
	require.NoError(t, err)
	text := out.Content[0].(*mcp.TextContent).Text
	assert.NotContains(t, text, "exploreUrl", "an unresolved public URL must not emit an empty link")
}
