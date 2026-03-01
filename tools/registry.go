package tools

import (
	"log/slog"
	"slices"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// CollectAllTools registers all tool categories with the given ToolAdder,
// filtered by the enabledTools list and enableWriteTools flag. This is the
// single entry point for tool registration, used by both MCP server mode
// and CLI mode.
func CollectAllTools(adder mcpgrafana.ToolAdder, enabledTools []string, enableWriteTools bool) {
	maybeAdd(adder, AddSearchTools, enabledTools, "search")
	maybeAdd(adder, AddDatasourceTools, enabledTools, "datasource")
	maybeAdd(adder, func(a mcpgrafana.ToolAdder) { AddIncidentTools(a, enableWriteTools) }, enabledTools, "incident")
	maybeAdd(adder, AddPrometheusTools, enabledTools, "prometheus")
	maybeAdd(adder, AddLokiTools, enabledTools, "loki")
	maybeAdd(adder, AddElasticsearchTools, enabledTools, "elasticsearch")
	maybeAdd(adder, func(a mcpgrafana.ToolAdder) { AddAlertingTools(a, enableWriteTools) }, enabledTools, "alerting")
	maybeAdd(adder, func(a mcpgrafana.ToolAdder) { AddDashboardTools(a, enableWriteTools) }, enabledTools, "dashboard")
	maybeAdd(adder, func(a mcpgrafana.ToolAdder) { AddFolderTools(a, enableWriteTools) }, enabledTools, "folder")
	maybeAdd(adder, AddOnCallTools, enabledTools, "oncall")
	maybeAdd(adder, AddAssertsTools, enabledTools, "asserts")
	maybeAdd(adder, func(a mcpgrafana.ToolAdder) { AddSiftTools(a, enableWriteTools) }, enabledTools, "sift")
	maybeAdd(adder, AddAdminTools, enabledTools, "admin")
	maybeAdd(adder, AddPyroscopeTools, enabledTools, "pyroscope")
	maybeAdd(adder, AddNavigationTools, enabledTools, "navigation")
	maybeAdd(adder, func(a mcpgrafana.ToolAdder) { AddAnnotationTools(a, enableWriteTools) }, enabledTools, "annotations")
	maybeAdd(adder, AddRenderingTools, enabledTools, "rendering")
	maybeAdd(adder, AddCloudWatchTools, enabledTools, "cloudwatch")
	maybeAdd(adder, AddExamplesTools, enabledTools, "examples")
	maybeAdd(adder, AddClickHouseTools, enabledTools, "clickhouse")
	maybeAdd(adder, AddSearchLogsTools, enabledTools, "searchlogs")
	maybeAdd(adder, AddRunPanelQueryTools, enabledTools, "runpanelquery")
}

func maybeAdd(adder mcpgrafana.ToolAdder, fn func(mcpgrafana.ToolAdder), enabledTools []string, category string) {
	if !slices.Contains(enabledTools, category) {
		slog.Debug("Not enabling tools", "category", category)
		return
	}
	slog.Debug("Enabling tools", "category", category)
	fn(adder)
}
