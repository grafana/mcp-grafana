package tools

import (
	"encoding/json"
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

// The host must be able to find the payload whichever channel it reads, and the
// result itself must name the app: the tool-level _meta only tells a host which
// resource to fetch, not which result to attach it to.
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

	// 3. The app's resource URI on the result meta.
	require.NotNil(t, out.Meta)
	ui, ok := out.Meta["ui"].(map[string]any)
	require.True(t, ok, "result should carry _meta.ui")
	assert.Equal(t, mcpgrafana.MetricsViewerResourceURI, ui["resourceUri"])
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
	assert.Nil(t, out.Meta, "and no app to render it")
}

func TestMetricsAppResultOmitsAbsentExploreURL(t *testing.T) {
	out, err := newMetricsAppResult(&QueryPrometheusResult{Data: model.Vector{}})
	require.NoError(t, err)
	text := out.Content[0].(*mcp.TextContent).Text
	assert.NotContains(t, text, "exploreUrl", "an unresolved public URL must not emit an empty link")
}
