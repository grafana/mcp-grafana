package main

import (
	"slices"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessTools_DashboardVariableQueryGates(t *testing.T) {
	for _, tc := range []struct {
		name         string
		disabled     disabledTools
		allowOptions bool
	}{
		{name: "default", allowOptions: true},
		{name: "disable query", disabled: disabledTools{query: true}},
		{name: "disable write", disabled: disabledTools{write: true}},
		{name: "enable query", disabled: disabledTools{write: true, enableQuery: true}, allowOptions: true},
		{name: "SQL override", disabled: disabledTools{write: true, writeToolOverrides: "query_sql"}, allowOptions: true},
		{name: "InfluxDB override must not enable SQL", disabled: disabledTools{write: true, writeToolOverrides: "query_influxdb"}},
		{name: "disable query wins", disabled: disabledTools{write: true, query: true, enableQuery: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := listAllCategoryTools(t, tc.disabled)
			inspection := tools["get_dashboard_panel_queries"]
			require.NotNil(t, inspection, "raw inspection remains available")
			assert.Equal(t, !tc.allowOptions, inspection.Annotations.ReadOnlyHint)
			assert.Equal(t, tc.allowOptions, *inspection.Annotations.DestructiveHint)
			permissions, ok := mcpgrafana.ToolRequiredPermissions(inspection)
			require.True(t, ok)
			assert.Equal(t, tc.allowOptions, slices.Contains(permissions, "datasources:query"))
		})
	}
}

func TestProcessTools_DashboardSQLCategoryGates(t *testing.T) {
	for _, tc := range []struct {
		name         string
		disabled     disabledTools
		allowOptions bool
	}{
		{name: "dashboard only", disabled: disabledTools{enabledTools: "dashboard"}},
		{name: "SQL opted in", disabled: disabledTools{enabledTools: "dashboard,sql"}, allowOptions: true},
		{name: "explicit SQL disable", disabled: disabledTools{enabledTools: "dashboard,sql", sql: true}},
		{name: "query disable", disabled: disabledTools{enabledTools: "dashboard,sql", query: true}},
		{name: "write disable", disabled: disabledTools{enabledTools: "dashboard,sql", write: true}},
		{name: "query opt in needs category", disabled: disabledTools{enabledTools: "dashboard", write: true, enableQuery: true}},
		{name: "query opt in with category", disabled: disabledTools{enabledTools: "dashboard,sql", write: true, enableQuery: true}, allowOptions: true},
		{name: "deprecated SQL alias", disabled: disabledTools{enabledTools: "dashboard,clickhouse"}, allowOptions: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
			tc.disabled.processTools(srv)
			session := connectTestClient(t, srv)
			result, err := session.ListTools(t.Context(), nil)
			require.NoError(t, err)
			var inspection *mcp.Tool
			for _, tool := range result.Tools {
				if tool.Name == "get_dashboard_panel_queries" {
					inspection = tool
				}
			}
			require.NotNil(t, inspection, "raw inspection stays available")
			assert.Equal(t, !tc.allowOptions, inspection.Annotations.ReadOnlyHint)
			permissions, ok := mcpgrafana.ToolRequiredPermissions(inspection)
			require.True(t, ok)
			assert.Equal(t, tc.allowOptions, slices.Contains(permissions, "datasources:query"))
		})
	}
}
