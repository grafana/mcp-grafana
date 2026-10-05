package tools

import (
	"context"
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

func TestExtractPanelQueriesV2_StructuredTarget(t *testing.T) {
	// A CloudWatch metric search query has no string expression in its query
	// spec; it must still show up, described rather than dropped.
	panel := map[string]interface{}{
		"title": "EC2 CPU",
		"data": map[string]interface{}{
			"spec": map[string]interface{}{
				"queries": []interface{}{
					map[string]interface{}{
						"spec": map[string]interface{}{
							"refId": "A",
							"query": map[string]interface{}{
								"group":      "cloudwatch",
								"datasource": map[string]interface{}{"name": "cw-uid"},
								"spec": map[string]interface{}{
									"namespace":  "AWS/EC2",
									"metricName": "CPUUtilization",
									"statistic":  "Average",
								},
							},
						},
					},
				},
			},
		},
	}

	queries := extractPanelQueriesV2(panel, nil, nil)

	require.Len(t, queries, 1)
	assert.Equal(t, "EC2 CPU", queries[0].Title)
	assert.Equal(t, "A", queries[0].RefID)
	assert.Empty(t, queries[0].Query)
	assert.Equal(t, map[string]interface{}{
		"namespace":  "AWS/EC2",
		"metricName": "CPUUtilization",
		"statistic":  "Average",
	}, queries[0].Target)
	assert.Equal(t, "cw-uid", queries[0].Datasource.UID)
	assert.Equal(t, "cloudwatch", queries[0].Datasource.Type)
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

// v2PanelWithQuery returns a v2 panel spec with id 7 holding a single query.
// The id is a float64, as it is in a dashboard decoded from JSON.
func v2PanelWithQuery(group string, body map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":    float64(7),
		"title": "Test panel",
		"data": map[string]interface{}{
			"kind": "QueryGroup",
			"spec": map[string]interface{}{
				"queries": []interface{}{
					map[string]interface{}{
						"kind": "PanelQuery",
						"spec": map[string]interface{}{
							"refId": "B",
							"query": map[string]interface{}{
								"kind":       "DataQuery",
								"group":      group,
								"datasource": map[string]interface{}{"name": group + "-uid"},
								"spec":       body,
							},
						},
					},
				},
			},
		},
	}
}

func TestExtractPanelInfoV2(t *testing.T) {
	_, spec := loadV2Dashboard(t)
	panel, err := findPanelByIDV2(spec, 1)
	require.NoError(t, err)

	info, err := extractPanelInfoV2(panel, 0)
	require.NoError(t, err)
	assert.Equal(t, "prom-uid", info.DatasourceUID)
	assert.Equal(t, "prometheus", info.DatasourceType)
	assert.Equal(t, `rate(cpu_seconds_total{job="$job"}[5m])`, info.Query)
	assert.Equal(t, "A", info.RawTarget["refId"])
	assert.Equal(t, map[string]interface{}{"uid": "prom-uid", "type": "prometheus"}, info.RawTarget["datasource"])

	_, err = extractPanelInfoV2(panel, 1)
	assert.ErrorContains(t, err, "queryIndex 1 out of range")

	// CloudWatch targets are structured and carry no query expression.
	cloudwatch := v2PanelWithQuery("cloudwatch", map[string]interface{}{
		"namespace":  "AWS/ApplicationELB",
		"metricName": "TargetResponseTime",
		"statistic":  "p95",
	})
	info, err = extractPanelInfoV2(cloudwatch, 0)
	require.NoError(t, err)
	assert.Empty(t, info.Query)
	assert.Equal(t, "p95", info.RawTarget["statistic"])
	assert.Equal(t, "B", info.RawTarget["refId"])
}

func TestTemplatingV1FromV2(t *testing.T) {
	spec := map[string]interface{}{
		"variables": []interface{}{
			map[string]interface{}{"kind": "ConstantVariable", "spec": map[string]interface{}{"name": "region", "query": "us-east-1"}},
			map[string]interface{}{"kind": "TextVariable", "spec": map[string]interface{}{"name": "filter", "query": "web"}},
			map[string]interface{}{"kind": "QueryVariable", "spec": map[string]interface{}{
				"name":    "pods",
				"current": map[string]interface{}{"value": []interface{}{"pod-a", "pod-b"}},
			}},
			map[string]interface{}{"kind": "QueryVariable", "spec": map[string]interface{}{
				"name":    "all",
				"current": map[string]interface{}{"value": "$__all"},
			}},
		},
	}

	assert.Equal(t, templateVariableValues{
		"region": {"us-east-1"},
		"filter": {"web"},
		"pods":   {"pod-a", "pod-b"},
	}, extractTemplateVariableValues(templatingV1FromV2(spec)))
}

func TestRunSinglePanelQuery_V2Dashboard(t *testing.T) {
	_, spec := loadV2Dashboard(t)
	spec["elements"].(map[string]interface{})["panel-unsupported"] = map[string]interface{}{
		"kind": "Panel",
		"spec": v2PanelWithQuery("test-unsupported", map[string]interface{}{"expr": "up"}),
	}

	// An unsupported datasource type fails only after the panel, its query and
	// the dashboard variables are read, so no Grafana client is needed.
	_, err := runSinglePanelQuery(context.Background(), singlePanelQueryParams{DB: spec, IsV2: true, PanelID: 7})
	assert.ErrorContains(t, err, "datasource type 'test-unsupported' is not supported by run_panel_query")

	// The v1 lookup cannot read a v2 spec.
	_, err = runSinglePanelQuery(context.Background(), singlePanelQueryParams{DB: spec, PanelID: 7})
	assert.ErrorContains(t, err, "finding panel: dashboard has no panels")
}
