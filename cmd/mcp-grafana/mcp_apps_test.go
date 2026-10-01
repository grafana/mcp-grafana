package main

import (
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestTempoInteractiveRegistration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  disabledTools
		enabled bool
	}{
		{"tempo", disabledTools{enabledTools: "tempo"}, true},
		{"read only", disabledTools{enabledTools: "tempo", write: true}, true},
		{"query disabled", disabledTools{enabledTools: "tempo", query: true}, false},
		{"tempo disabled", disabledTools{enabledTools: "tempo", tempo: true}, false},
		{"other category", disabledTools{enabledTools: "prometheus"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			for _, entry := range tc.config.toolEntries() {
				maybeAddTools(s, entry.adder, []string{tc.config.enabledTools}, entry.disabled, entry.category)
			}
			result, err := connectTestClient(t, s).ListTools(t.Context(), nil)
			require.NoError(t, err)
			registered := map[string]*mcp.Tool{}
			for _, tool := range result.Tools {
				registered[tool.Name] = tool
			}
			tool, ok := registered["get_tempo_trace"]
			require.Equal(t, tc.enabled, ok)
			require.NotContains(t, registered, "render_trace")
			if ok {
				require.NotNil(t, tool.Meta)
				require.Equal(t, mcpgrafana.TraceViewerResourceURI, tool.Meta["ui"].(map[string]any)["resourceUri"])
			}
			for _, entry := range tc.config.toolEntries() {
				require.NotEqual(t, "mcp-apps", entry.category)
			}
		})
	}
}
