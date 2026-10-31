package main

import (
	"encoding/json"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestExplicitAppRegistration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  disabledTools
		sources []string
	}{
		{"metrics", disabledTools{enabledTools: "prometheus,rendering"}, []string{"query_prometheus"}},
		{"trace", disabledTools{enabledTools: "tempo,rendering"}, []string{"get_tempo_trace"}},
		{"both", disabledTools{enabledTools: "tempo,prometheus,rendering"}, []string{"query_prometheus", "get_tempo_trace"}},
		{"read only", disabledTools{enabledTools: "tempo,prometheus,rendering", write: true}, []string{"query_prometheus", "get_tempo_trace"}},
		{"query disabled", disabledTools{enabledTools: "tempo,prometheus,rendering", query: true}, nil},
		{"tempo disabled", disabledTools{enabledTools: "tempo,prometheus,rendering", tempo: true}, []string{"query_prometheus"}},
		{"prometheus disabled", disabledTools{enabledTools: "tempo,prometheus,rendering", prometheus: true}, []string{"get_tempo_trace"}},
		{"sources disabled", disabledTools{enabledTools: "tempo,prometheus,rendering", tempo: true, prometheus: true}, nil},
		{"rendering disabled", disabledTools{enabledTools: "tempo,prometheus,rendering", rendering: true}, nil},
		{"data only", disabledTools{enabledTools: "tempo,prometheus"}, nil},
		{"no sources", disabledTools{enabledTools: "rendering"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			tc.config.processTools(s)
			result, err := connectTestClient(t, s).ListTools(t.Context(), nil)
			require.NoError(t, err)
			registered := map[string]*mcp.Tool{}
			for _, tool := range result.Tools {
				registered[tool.Name] = tool
			}
			for _, name := range []string{"query_prometheus", "get_tempo_trace"} {
				if source := registered[name]; source != nil {
					require.NotContains(t, source.Meta, "ui")
				}
			}
			renderer := registered["render_grafana_app"]
			if len(tc.sources) == 0 {
				require.Nil(t, renderer)
				return
			}
			require.NotNil(t, renderer)
			require.Equal(t, mcpgrafana.GrafanaAppResourceURI, renderer.Meta["ui"].(map[string]any)["resourceUri"])
			var schema struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			}
			body, err := json.Marshal(renderer.InputSchema)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, &schema))
			require.ElementsMatch(t, tc.sources, schema.Properties["toolName"].Enum)
		})
	}
}
