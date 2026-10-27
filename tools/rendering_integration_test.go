// Requires the docker-compose environment (Grafana with the image renderer and
// the provisioned Prometheus datasource). Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderExplore_Integration(t *testing.T) {
	ctx := newTestContext()

	result, err := getPanelImage(ctx, GetPanelImageParams{
		Explore: &RenderExplore{
			DatasourceUID: "prometheus",
			Queries:       []map[string]interface{}{{"refId": "A", "expr": "up"}},
		},
		TimeRange: &RenderTimeRange{From: "now-1h", To: "now"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "render returned an error result: %+v", result.Content)
	require.Len(t, result.Content, 2)

	img, ok := result.Content[0].(*mcp.ImageContent)
	require.True(t, ok, "expected ImageContent at index 0, got %T", result.Content[0])
	assert.Equal(t, "image/png", img.MIMEType)
	require.NotEmpty(t, img.Data, "rendered PNG should not be empty")
	// PNG magic number.
	assert.Equal(t, []byte{0x89, 'P', 'N', 'G'}, img.Data[:4], "rendered image should be a PNG")

	deeplink, ok := result.Content[1].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent at index 1, got %T", result.Content[1])
	assert.True(t, strings.Contains(deeplink.Text, "/explore?"), "deeplink should point at Explore: %s", deeplink.Text)
	assert.NotContains(t, deeplink.Text, "/render/", "deeplink should not point at the renderer: %s", deeplink.Text)
}
