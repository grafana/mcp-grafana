//go:build integration

package tools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	gcf "github.com/blackwell-systems/gcf-go"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGCFOutputEndToEnd exercises the full opt-in GCF path against a live
// Grafana: it calls the registered list_datasources tool through its MustTool
// handler (the same wrapper the server uses) with OutputFormat=gcf, and checks
// that the model-facing text is GCF and decodes back to exactly the JSON the
// same tool returns by default. Relies on the docker-compose provisioned
// datasources being numerous enough for GCF to win (they are).
func TestGCFOutputEndToEnd(t *testing.T) {
	base := newTestContext()
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "list_datasources",
		Arguments: map[string]any{},
	}}

	// Default: unchanged JSON.
	jsonRes, err := ListDatasources.Handler(base, req)
	require.NoError(t, err)
	jsonText := jsonRes.Content[0].(mcp.TextContent).Text
	require.True(t, strings.HasPrefix(strings.TrimSpace(jsonText), "{"), "default output should be JSON")

	// Opt-in: same request, OutputFormat=gcf on the config already in the ctx.
	cfg := mcpgrafana.GrafanaConfigFromContext(base)
	cfg.OutputFormat = "gcf"
	gctx := mcpgrafana.WithGrafanaConfig(base, cfg)

	gcfRes, err := ListDatasources.Handler(gctx, req)
	require.NoError(t, err)
	gcfText := gcfRes.Content[0].(mcp.TextContent).Text
	require.True(t, strings.HasPrefix(gcfText, "GCF profile=generic"),
		"expected GCF output (need provisioned datasources), got: %.60s", gcfText)
	assert.Less(t, len(gcfText), len(jsonText), "GCF wire should be smaller than JSON")

	// The GCF wire decodes back to exactly the tool's JSON value.
	decoded, err := gcf.DecodeGeneric(gcfText)
	require.NoError(t, err)
	decodedBytes, err := json.Marshal(decoded)
	require.NoError(t, err)
	assert.True(t, jsonSemanticallyEqual(t, []byte(jsonText), decodedBytes),
		"GCF must round-trip to the JSON result")
}

// jsonSemanticallyEqual compares two JSON documents ignoring object key order.
func jsonSemanticallyEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	require.NoError(t, json.Unmarshal(a, &av))
	require.NoError(t, json.Unmarshal(b, &bv))
	return reflect.DeepEqual(av, bv)
}
