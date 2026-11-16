package main

import (
	"encoding/json"
	"slices"
	"strings"
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
		{"dsquery", disabledTools{enabledTools: "rendering,dsquery"}, []string{"query_datasource"}},
		{"dsquery with others", disabledTools{enabledTools: "prometheus,rendering,dsquery"}, []string{"query_prometheus", "query_datasource"}},
		{"dsquery disabled", disabledTools{enabledTools: "rendering,dsquery", dsquery: true}, nil},
		{"dsquery read only", disabledTools{enabledTools: "rendering,dsquery", write: true}, nil},
		{"dsquery read only, enable-query", disabledTools{enabledTools: "rendering,dsquery", write: true, enableQuery: true}, []string{"query_datasource"}},
		{"dsquery read only, enable-write-tools", disabledTools{enabledTools: "rendering,dsquery", write: true, writeToolOverrides: "query_datasource"}, []string{"query_datasource"}},
		{"dsquery query disabled", disabledTools{enabledTools: "rendering,dsquery", query: true}, nil},
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
			for _, name := range []string{"query_prometheus", "get_tempo_trace", "query_datasource"} {
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

func TestAppToolHintsFollowItsSources(t *testing.T) {
	for _, tc := range []struct {
		name     string
		config   disabledTools
		readOnly bool
	}{
		{"read-only sources", disabledTools{enabledTools: "prometheus,rendering"}, true},
		{"a mutating source", disabledTools{enabledTools: "prometheus,rendering,dsquery"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			tc.config.processTools(s)
			result, err := connectTestClient(t, s).ListTools(t.Context(), nil)
			require.NoError(t, err)
			var viewer *mcp.Tool
			for _, tool := range result.Tools {
				if tool.Name == "render_grafana_app" {
					viewer = tool
				}
			}
			require.NotNil(t, viewer)
			require.Equal(t, tc.readOnly, viewer.Annotations.ReadOnlyHint)
			require.Equal(t, !tc.readOnly, *viewer.Annotations.DestructiveHint)
		})
	}
}

func TestDSQueryCategoryFollowsItsSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config disabledTools
		active bool
	}{
		{"enabled", disabledTools{enabledTools: "rendering,dsquery"}, true},
		{"read only", disabledTools{enabledTools: "rendering,dsquery", write: true}, false},
		{"read only, enable-query", disabledTools{enabledTools: "rendering,dsquery", write: true, enableQuery: true}, true},
		{"query disabled", disabledTools{enabledTools: "rendering,dsquery", query: true}, false},
		{"no rendering", disabledTools{enabledTools: "dsquery"}, false},
		{"rendering disabled", disabledTools{enabledTools: "rendering,dsquery", rendering: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled, _ := tc.config.categoryReport()
			require.Equal(t, tc.active, slices.Contains(enabled, "dsquery"))
			require.Equal(t, tc.active, strings.Contains(tc.config.buildInstructions(), "query_datasource"))
		})
	}
}
