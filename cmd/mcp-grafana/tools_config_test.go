package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/tools"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// toolsWithConfig loads body as the --config file under the flags in dt,
// where set names the flags given on the command line, and returns the tools
// a client sees in tools/list.
func toolsWithConfig(t *testing.T, body string, dt disabledTools, set ...string) map[string]*mcp.Tool {
	t.Helper()
	return listTools(t, withConfig(t, body, dt, set...))
}

// withConfig loads body as the --config file under the flags in dt, where set
// names the flags given on the command line.
func withConfig(t *testing.T, body string, dt disabledTools, set ...string) disabledTools {
	t.Helper()
	cfg, err := loadConfigFile(writeConfig(t, body))
	require.NoError(t, err)

	if dt.enabledTools == "" {
		dt.enabledTools = defaultEnabledTools
	}
	setFlags := map[string]bool{}
	for _, name := range set {
		setFlags[name] = true
	}
	dt.applyConfig(cfg.Tools, setFlags)
	return dt
}

func listTools(t *testing.T, dt disabledTools) map[string]*mcp.Tool {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	dt.processTools(srv)
	result, err := connectTestClient(t, srv).ListTools(context.Background(), nil)
	require.NoError(t, err)
	listed := make(map[string]*mcp.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		listed[tool.Name] = tool
	}
	return listed
}

func TestLoadConfigFile_RejectsInvalidFiles(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantErr string
	}{
		{"unknown tools key", "tools:\n  alow: [update_dashboard]\n", "field alow not found"},
		{"unknown section", "server:\n  port: 8000\n", "field server not found"},
		{"version key", "version: 1\ntools:\n  mode: read-only\n", "field version not found"},
		{"nested categories", "tools:\n  categories:\n    enable: [search]\n", "cannot unmarshal"},
		{"unknown mode", "tools:\n  mode: readonly\n", `mode must be "read-write" or "read-only", got "readonly"`},
		{"unknown category", "tools:\n  categories: [search, nope]\n", `unknown category "nope"`},
		{"deprecated alias", "tools:\n  categories: [clickhouse]\n", `unknown category "clickhouse"`},
		{"unknown allow tool", "tools:\n  allow: [update_dashbord]\n", `allow: unknown tool "update_dashbord"`},
		{"unknown deny tool", "tools:\n  deny: [query_sequel]\n", `deny: unknown tool "query_sequel"`},
		{"allow outside enabled categories", "tools:\n  categories: [search]\n  allow: [update_dashboard]\n", `allow: tool "update_dashboard" is in a category that is not enabled (dashboard)`},
		{"allow outside its own category", "tools:\n  categories: [api]\n  mode: read-only\n  allow: [query_sql]\n", `allow: tool "query_sql" is in a category that is not enabled (sql)`},
		{"allow and deny", "tools:\n  allow: [update_dashboard]\n  deny: [update_dashboard]\n", `tool "update_dashboard" is in both allow and deny`},
		{"two documents", "tools:\n  mode: read-only\n---\ntools:\n  mode: read-write\n", "only one YAML document is allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfigFile(writeConfig(t, tc.body))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLoadConfigFile_ReportsEveryProblem(t *testing.T) {
	_, err := loadConfigFile(writeConfig(t, "tools:\n  categories: [nope]\n  deny: [query_sequel]\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown category "nope"`)
	assert.Contains(t, err.Error(), `deny: unknown tool "query_sequel"`)
}

func TestLoadConfigFile_EmptyFileKeepsDefaults(t *testing.T) {
	want := listTools(t, disabledTools{enabledTools: defaultEnabledTools})
	for _, body := range []string{"", "tools:\n", "tools: {}\n"} {
		assert.Equal(t, want, toolsWithConfig(t, body, disabledTools{}), "config %q", body)
	}
}

func TestConfig_AllowKeepsWriteToolUnderReadOnly(t *testing.T) {
	listed := toolsWithConfig(t, "tools:\n  mode: read-only\n  allow: [update_dashboard]\n", disabledTools{})
	assert.Contains(t, listed, "update_dashboard")
	assert.NotContains(t, listed, "create_folder", "allow keeps only the tools it names")
}

func TestConfig_AllowDoesNotLiftQueryGate(t *testing.T) {
	body := "tools:\n  categories: [sql]\n  mode: read-only\n  query: false\n  allow: [query_sql]\n"
	listed := toolsWithConfig(t, body, disabledTools{})
	assert.NotContains(t, listed, "query_sql", "query: false is a hard off")
	assert.Contains(t, listed, "list_sql_tables")
}

func TestConfig_DenyRemovesToolAndItsVariants(t *testing.T) {
	listed := toolsWithConfig(t, "tools:\n  deny: [update_dashboard, grafana_api_request, generate_deeplink]\n", disabledTools{})
	assert.NotContains(t, listed, "update_dashboard")
	assert.NotContains(t, listed, "grafana_api_request", "deny must not fall back to a read-only variant")
	assert.NotContains(t, listed, "generate_deeplink")
	assert.Contains(t, listed, "get_dashboard_by_uid")
}

func TestConfig_DenyRemovesAppTool(t *testing.T) {
	require.Contains(t, toolsWithConfig(t, "", disabledTools{}), "render_grafana_app")

	listed := toolsWithConfig(t, "tools:\n  deny: [render_grafana_app]\n", disabledTools{})
	assert.NotContains(t, listed, "render_grafana_app")
	assert.Contains(t, listed, "query_prometheus")
	assert.Contains(t, listed, "get_panel_image")
}

func TestConfig_DenyKeepsFullDescriptionWhileQueryToolsRemain(t *testing.T) {
	const full = "Prometheus: Run PromQL queries"
	for _, tc := range []struct {
		deny        string
		wantReduced bool
	}{
		{"list_prometheus_metric_names", false},
		{"query_prometheus_histogram", false},
		{"query_prometheus, query_prometheus_histogram", true},
	} {
		t.Run(tc.deny, func(t *testing.T) {
			dt := withConfig(t, "tools:\n  deny: ["+tc.deny+"]\n", disabledTools{})
			instructions := dt.buildInstructions()
			assert.Equal(t, !tc.wantReduced, strings.Contains(instructions, full), instructions)
			assert.Equal(t, tc.wantReduced, strings.Contains(instructions, "Query execution is disabled"), instructions)
		})
	}
}

func TestConfig_StrictCategoryCheckIsFileOnly(t *testing.T) {
	_, err := loadConfigFile(writeConfig(t, "tools:\n  categories: [search]\n  mode: read-only\n  allow: [query_sql]\n"))
	require.Error(t, err, "sql is not enabled, so allow: [query_sql] is rejected")

	listed := toolsWithConfig(t, "tools:\n  categories: [search]\n  mode: read-only\n", disabledTools{enableQuery: true}, "enable-query")
	assert.NotContains(t, listed, "query_sql", "--enable-query still skips tools in disabled categories silently")
	assert.Contains(t, listed, "search_dashboards")
}

func TestConfig_ShorthandFlagsAddToAllow(t *testing.T) {
	body := "tools:\n  categories: [dashboard, sql]\n  mode: read-only\n  allow: [update_dashboard]\n"
	listed := toolsWithConfig(t, body, disabledTools{enableQuery: true}, "enable-query")
	assert.Contains(t, listed, "update_dashboard", "--enable-query must not replace the file's allow list")
	assert.Contains(t, listed, "query_sql")
}

func TestConfig_FlagsOverrideFile(t *testing.T) {
	t.Run("--disable-write=false beats mode: read-only", func(t *testing.T) {
		listed := toolsWithConfig(t, "tools:\n  mode: read-only\n", disabledTools{write: false}, "disable-write")
		assert.Contains(t, listed, "update_dashboard")
	})
	t.Run("--disable-query=false beats query: false", func(t *testing.T) {
		listed := toolsWithConfig(t, "tools:\n  query: false\n", disabledTools{query: false}, "disable-query")
		assert.Contains(t, listed, "query_prometheus")
	})
	t.Run("--enabled-tools replaces categories", func(t *testing.T) {
		listed := toolsWithConfig(t, "tools:\n  categories: [search]\n", disabledTools{enabledTools: "dashboard"}, "enabled-tools")
		assert.Contains(t, listed, "update_dashboard")
		assert.NotContains(t, listed, "search_dashboards")
	})
	t.Run("--disable-<category> removes a file category", func(t *testing.T) {
		listed := toolsWithConfig(t, "tools:\n  categories: [search, dashboard]\n", disabledTools{dashboard: true}, "disable-dashboard")
		assert.NotContains(t, listed, "update_dashboard")
		assert.Contains(t, listed, "search_dashboards")
	})
	t.Run("--enable-write-tools beats deny", func(t *testing.T) {
		body := "tools:\n  categories: [sql]\n  deny: [query_sql]\n"
		listed := toolsWithConfig(t, body, disabledTools{writeToolOverrides: "query_sql"}, "enable-write-tools")
		assert.Contains(t, listed, "query_sql")
	})
	t.Run("unset flags keep the file", func(t *testing.T) {
		listed := toolsWithConfig(t, "tools:\n  mode: read-only\n  query: false\n", disabledTools{})
		assert.NotContains(t, listed, "update_dashboard")
		assert.NotContains(t, listed, "query_prometheus")
	})
}

func TestConfig_APIRequestVariants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		dt    disabledTools
		set   []string
		wants mcpgrafana.Tool
	}{
		{"read-only", "tools:\n  categories: [api]\n  mode: read-only\n", disabledTools{}, nil, tools.APIRequestReadOnlyGetOnly},
		{"allow by name", "tools:\n  categories: [api]\n  mode: read-only\n  allow: [grafana_api_request]\n", disabledTools{}, nil, tools.APIRequest},
		{"allow query_sql", "tools:\n  categories: [api, sql]\n  mode: read-only\n  allow: [query_sql]\n", disabledTools{}, nil, tools.APIRequestReadOnly},
		{"--enable-write-tools under file read-only", "tools:\n  categories: [api]\n  mode: read-only\n", disabledTools{writeToolOverrides: "grafana_api_request"}, []string{"enable-write-tools"}, tools.APIRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listed := toolsWithConfig(t, tc.body, tc.dt, tc.set...)
			require.Contains(t, listed, "grafana_api_request")
			assert.Equal(t, tc.wants.Tool.Description, listed["grafana_api_request"].Description)
		})
	}
}

func TestConfig_DocsExampleLoads(t *testing.T) {
	raw, err := os.ReadFile("../../docs/sources/configure/enable-and-disable-tools.md")
	require.NoError(t, err)
	_, example, ok := strings.Cut(string(raw), "```yaml\n")
	require.True(t, ok, "docs YAML example not found")
	example, _, _ = strings.Cut(example, "```")

	listed := toolsWithConfig(t, example, disabledTools{})
	assert.Contains(t, listed, "update_dashboard")
	assert.NotContains(t, listed, "query_sql")
	assert.NotContains(t, listed, "create_folder")
}
