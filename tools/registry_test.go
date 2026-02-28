package tools

import (
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

func TestCollectAllToolsDefaultEnabled(t *testing.T) {
	c := mcpgrafana.NewToolCollector()
	enabledTools := []string{
		"search", "datasource", "incident", "prometheus", "loki",
		"alerting", "dashboard", "folder", "oncall", "asserts",
		"sift", "pyroscope", "navigation", "annotations", "rendering",
	}
	CollectAllTools(c, enabledTools, true)

	tools := c.Tools()
	if len(tools) == 0 {
		t.Fatal("expected tools to be registered, got 0")
	}

	// Spot-check a few expected tools
	for _, name := range []string{"search_dashboards", "list_datasources", "list_alert_rules"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestCollectAllToolsSubsetEnabled(t *testing.T) {
	c := mcpgrafana.NewToolCollector()
	CollectAllTools(c, []string{"search"}, true)

	tools := c.Tools()
	if _, ok := tools["search_dashboards"]; !ok {
		t.Error("expected search_dashboards to be registered when 'search' is enabled")
	}
	// Tools from other categories should not be present
	if _, ok := tools["list_datasources"]; ok {
		t.Error("expected list_datasources to NOT be registered when only 'search' is enabled")
	}
}

func TestCollectAllToolsEmptyEnabledList(t *testing.T) {
	c := mcpgrafana.NewToolCollector()
	CollectAllTools(c, []string{}, true)

	tools := c.Tools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools with empty enabled list, got %d", len(tools))
	}
}

func TestCollectAllToolsWriteToolsDisabled(t *testing.T) {
	withWrite := mcpgrafana.NewToolCollector()
	CollectAllTools(withWrite, []string{"alerting"}, true)
	withWriteTools := withWrite.Tools()

	withoutWrite := mcpgrafana.NewToolCollector()
	CollectAllTools(withoutWrite, []string{"alerting"}, false)
	withoutWriteTools := withoutWrite.Tools()

	if len(withWriteTools) <= len(withoutWriteTools) {
		t.Errorf("expected more tools with write enabled (%d) than without (%d)",
			len(withWriteTools), len(withoutWriteTools))
	}

	// create_alert_rule should only be present when write tools are enabled
	if _, ok := withWriteTools["create_alert_rule"]; !ok {
		t.Error("expected create_alert_rule to be present when write tools are enabled")
	}
	if _, ok := withoutWriteTools["create_alert_rule"]; ok {
		t.Error("expected create_alert_rule to NOT be present when write tools are disabled")
	}
}
