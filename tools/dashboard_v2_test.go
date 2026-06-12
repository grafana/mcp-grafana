package tools

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadV2Dashboard reads the v2beta1 fixture and returns the full k8s object and
// its spec.
func loadV2Dashboard(t *testing.T) (obj, spec map[string]interface{}) {
	t.Helper()
	data, err := os.ReadFile("testdata/v2beta1_dashboard.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &obj))
	spec, ok := obj["spec"].(map[string]interface{})
	require.True(t, ok, "fixture must have a spec object")
	return obj, spec
}

func TestCollectElementsV2(t *testing.T) {
	_, spec := loadV2Dashboard(t)

	els := collectElementsV2(spec)
	require.Len(t, els, 3)
	// Sorted by panel id: 1 (cpu), 2 (mem), 3 (library panel).
	assert.Equal(t, "panel-cpu", els[0].Name)
	assert.Equal(t, "Panel", els[0].Kind)
	assert.Equal(t, "panel-mem", els[1].Name)
	assert.Equal(t, "lib-1", els[2].Name)
	assert.Equal(t, "LibraryPanel", els[2].Kind)

	// collectAllPanelsV2 excludes library panels.
	panels := collectAllPanelsV2(spec)
	require.Len(t, panels, 2)
	assert.Equal(t, "CPU usage", safeString(panels[0], "title"))
}

func TestFindPanelByIDV2(t *testing.T) {
	_, spec := loadV2Dashboard(t)

	panel, err := findPanelByIDV2(spec, 2)
	require.NoError(t, err)
	assert.Equal(t, "Memory usage", safeString(panel, "title"))

	_, err = findPanelByIDV2(spec, 99)
	assert.Error(t, err)
}

func TestGetPanelQueriesV2(t *testing.T) {
	_, spec := loadV2Dashboard(t)

	queries, err := getPanelQueriesV2(spec, DashboardPanelQueriesParams{UID: "v2-test-uid"})
	require.NoError(t, err)
	require.Len(t, queries, 2)

	assert.Equal(t, "CPU usage", queries[0].Title)
	assert.Equal(t, `rate(cpu_seconds_total{job="$job"}[5m])`, queries[0].Query)
	assert.Equal(t, "A", queries[0].RefID)
	// Datasource type comes from query.group, uid from datasource.name.
	assert.Equal(t, "prometheus", queries[0].Datasource.Type)
	assert.Equal(t, "prom-uid", queries[0].Datasource.UID)

	assert.Equal(t, "loki", queries[1].Datasource.Type)
	assert.Equal(t, "loki-uid", queries[1].Datasource.UID)
}

func TestGetPanelQueriesV2_WithVariableSubstitution(t *testing.T) {
	_, spec := loadV2Dashboard(t)

	queries, err := getPanelQueriesV2(spec, DashboardPanelQueriesParams{
		UID:       "v2-test-uid",
		PanelID:   intPtrV2(1),
		Variables: map[string]string{"job": "web"},
	})
	require.NoError(t, err)
	require.Len(t, queries, 1)

	assert.Equal(t, `rate(cpu_seconds_total{job="web"}[5m])`, queries[0].ProcessedQuery)
	require.Len(t, queries[0].RequiredVariables, 1)
	assert.Equal(t, "job", queries[0].RequiredVariables[0].Name)
	assert.Equal(t, "web", queries[0].RequiredVariables[0].CurrentValue)
}

func TestExtractDashboardVariablesV2(t *testing.T) {
	_, spec := loadV2Dashboard(t)

	vars := extractDashboardVariablesV2(spec)
	require.Contains(t, vars, "job")
	require.Contains(t, vars, "env")
	assert.Equal(t, "api", vars["job"].CurrentValue)
	assert.Equal(t, "prod", vars["env"].CurrentValue)
}

func TestDashboardSummaryV2(t *testing.T) {
	_, spec := loadV2Dashboard(t)

	summary, err := dashboardSummaryV2(spec, "v2-test-uid", nil)
	require.NoError(t, err)

	assert.Equal(t, "v2-test-uid", summary.UID)
	assert.Equal(t, "V2 Test Dashboard", summary.Title)
	assert.Equal(t, []string{"v2", "test"}, summary.Tags)
	assert.Equal(t, "now-6h", summary.TimeRange.From)
	assert.Equal(t, "now", summary.TimeRange.To)
	assert.Equal(t, "30s", summary.Refresh)

	// 3 elements: two panels + one library panel.
	assert.Equal(t, 3, summary.PanelCount)
	require.Len(t, summary.Panels, 3)
	assert.Equal(t, 1, summary.Panels[0].ID)
	assert.Equal(t, "timeseries", summary.Panels[0].Type)
	assert.Equal(t, 1, summary.Panels[0].QueryCount)
	assert.Equal(t, "LibraryPanel", summary.Panels[2].Type)

	require.Len(t, summary.Variables, 2)
	assert.Equal(t, "job", summary.Variables[0].Name)
	assert.Equal(t, "query", summary.Variables[0].Type)
	assert.Equal(t, "Job", summary.Variables[0].Label)
	assert.Equal(t, "custom", summary.Variables[1].Type)
}

func intPtrV2(i int) *int { return &i }
