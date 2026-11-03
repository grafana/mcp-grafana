package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/tools"
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
		{"datasource view", disabledTools{enabledTools: "prometheus,rendering,dsquery"}, []string{"query_prometheus", "query_datasource"}},
		{"datasource view only", disabledTools{enabledTools: "rendering,dsquery"}, []string{"query_datasource"}},
		{"datasource view disabled", disabledTools{enabledTools: "prometheus,rendering,dsquery", dsquery: true}, []string{"query_prometheus"}},
		{"datasource view without rendering", disabledTools{enabledTools: "prometheus,dsquery"}, nil},
		{"datasource view read only", disabledTools{enabledTools: "prometheus,rendering,dsquery", write: true}, []string{"query_prometheus"}},
		{"datasource view read only, enable-query", disabledTools{enabledTools: "prometheus,rendering,dsquery", write: true, enableQuery: true}, []string{"query_prometheus", "query_datasource"}},
		{"datasource view read only, write override", disabledTools{enabledTools: "prometheus,rendering,dsquery", write: true, writeToolOverrides: "query_datasource"}, []string{"query_prometheus", "query_datasource"}},
		{"datasource view, query disabled", disabledTools{enabledTools: "prometheus,rendering,dsquery", query: true}, nil},
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
			// query_datasource is an App source only, never a tool of its own.
			require.NotContains(t, registered, tools.QueryDatasourceToolName)
			enabled, _ := tc.config.categoryReport()
			require.Equal(t, slices.Contains(tc.sources, tools.QueryDatasourceToolName), slices.Contains(enabled, "dsquery"))
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

// TestAppSourceNamesMatchUI keeps the source names that the App routes on
// (ui/mcp-apps/app/appSources.json) in step with the Go sources.
func TestAppSourceNamesMatchUI(t *testing.T) {
	body, err := os.ReadFile("../../ui/mcp-apps/app/appSources.json")
	require.NoError(t, err)
	var names map[string]string
	require.NoError(t, json.Unmarshal(body, &names))
	require.Equal(t, map[string]string{
		"metrics":    tools.QueryPrometheus.Tool.Name,
		"trace":      tools.GetTempoTraceTool.Tool.Name,
		"datasource": tools.QueryDatasourceToolName,
	}, names)
	dt := disabledTools{enabledTools: "prometheus,tempo,rendering,dsquery"}
	for _, source := range dt.appSources() {
		require.Contains(t, names, map[string]string{
			tools.QueryPrometheus.Tool.Name:   "metrics",
			tools.GetTempoTraceTool.Tool.Name: "trace",
			tools.QueryDatasourceToolName:     "datasource",
		}[source.Tool.Name], "the App has no view for %s", source.Tool.Name)
	}
}
