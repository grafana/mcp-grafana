package main

import (
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestMCPAppsCategory(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  disabledTools
		enabled bool
	}{
		{"not opted in", disabledTools{enabledTools: "tempo,prometheus"}, false},
		{"opted in", disabledTools{enabledTools: "mcp-apps"}, true},
		{"read only", disabledTools{enabledTools: "mcp-apps", write: true}, true},
		{"queries disabled", disabledTools{enabledTools: "mcp-apps", query: true}, false},
		{"category disabled", disabledTools{enabledTools: "mcp-apps", mcpApps: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := server.NewMCPServer("test", "1")
			for _, entry := range tc.config.toolEntries() {
				if entry.category == "mcp-apps" {
					maybeAddTools(s, entry.adder, []string{tc.config.enabledTools}, entry.disabled, entry.category)
				}
			}
			enabledCategories, _ := tc.config.categoryReport()
			if tc.enabled {
				require.Contains(t, enabledCategories, "mcp-apps")
				require.Contains(t, tc.config.buildInstructions(), "MCP Apps:")
			} else {
				require.NotContains(t, enabledCategories, "mcp-apps")
				require.NotContains(t, tc.config.buildInstructions(), "MCP Apps:")
			}
			registered := s.ListTools()
			for _, name := range []string{"render_trace"} {
				_, ok := registered[name]
				require.Equal(t, tc.enabled, ok, name)
			}
		})
	}
}
