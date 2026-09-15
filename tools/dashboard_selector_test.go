//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardSelectorSchemaRejectsAdditionalProperties(t *testing.T) {
	var schema map[string]interface{}
	require.NoError(t, json.Unmarshal(GetDashboardProperty.Tool.RawInputSchema, &schema))

	selectorSchema := findSchemaWithProperties(schema, "kind", "panelId", "refId", "name")
	require.NotNil(t, selectorSchema, "selector schema not found")
	assert.Equal(t, false, selectorSchema["additionalProperties"])
}

func TestDashboardSelectorRejectsUnknownFields(t *testing.T) {
	var params GetDashboardPropertyParams
	err := json.Unmarshal([]byte(`{
		"uid": "service-overview",
		"jsonPath": "$.title",
		"selector": {"kind": "dashboard", "panelID": 17}
	}`), &params)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
	assert.Contains(t, err.Error(), "panelID")
}

func findSchemaWithProperties(node interface{}, names ...string) map[string]interface{} {
	switch value := node.(type) {
	case map[string]interface{}:
		if properties, ok := value["properties"].(map[string]interface{}); ok {
			matches := true
			for _, name := range names {
				if _, ok := properties[name]; !ok {
					matches = false
					break
				}
			}
			if matches {
				return value
			}
		}
		for _, child := range value {
			if found := findSchemaWithProperties(child, names...); found != nil {
				return found
			}
		}
	case []interface{}:
		for _, child := range value {
			if found := findSchemaWithProperties(child, names...); found != nil {
				return found
			}
		}
	}
	return nil
}

func TestResolveDashboardSelectorClassic(t *testing.T) {
	panelID := 17
	dashboard := map[string]interface{}{
		"title": "Service overview",
		"panels": []interface{}{
			map[string]interface{}{
				"id":    float64(panelID),
				"title": "Requests",
				"targets": []interface{}{
					map[string]interface{}{"refId": "A", "rawSql": "select 1"},
					map[string]interface{}{"refId": "B", "expr": "up"},
				},
			},
		},
		"templating": map[string]interface{}{
			"list": []interface{}{
				map[string]interface{}{"name": "cluster", "label": "Cluster"},
			},
		},
	}

	tests := []struct {
		name     string
		selector DashboardSelector
		want     interface{}
	}{
		{
			name:     "dashboard",
			selector: DashboardSelector{Kind: "dashboard"},
			want:     "Service overview",
		},
		{
			name:     "panel",
			selector: DashboardSelector{Kind: "panel", PanelID: &panelID},
			want:     "Requests",
		},
		{
			name:     "query",
			selector: DashboardSelector{Kind: "query", PanelID: &panelID, RefID: "A"},
			want:     "select 1",
		},
		{
			name:     "variable",
			selector: DashboardSelector{Kind: "variable", Name: "cluster"},
			want:     "Cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, err := resolveDashboardSelector(dashboard, false, tt.selector)
			require.NoError(t, err)

			switch tt.selector.Kind {
			case "dashboard":
				assert.Equal(t, tt.want, selected["title"])
			case "panel":
				assert.Equal(t, tt.want, selected["title"])
			case "query":
				assert.Equal(t, tt.want, selected["rawSql"])
			case "variable":
				assert.Equal(t, tt.want, selected["label"])
			}
		})
	}
}

func TestResolveDashboardSelectorLegacyRows(t *testing.T) {
	panelID := 5

	selected, err := resolveDashboardSelector(
		loadLegacyRowsDashboard(t),
		false,
		DashboardSelector{Kind: "panel", PanelID: &panelID},
	)

	require.NoError(t, err)
	assert.Equal(t, "Receive bandwidth", selected["title"])
}

func TestResolveDashboardSelectorV2(t *testing.T) {
	_, spec := loadV2Dashboard(t)
	panelID := 1

	panel, err := resolveDashboardSelector(spec, true, DashboardSelector{Kind: "panel", PanelID: &panelID})
	require.NoError(t, err)
	assert.Equal(t, "CPU usage", panel["title"])

	libraryPanelID := 3
	libraryPanel, err := resolveDashboardSelector(spec, true, DashboardSelector{Kind: "panel", PanelID: &libraryPanelID})
	require.NoError(t, err)
	assert.Equal(t, "Shared graph", libraryPanel["title"])

	query, err := resolveDashboardSelector(spec, true, DashboardSelector{Kind: "query", PanelID: &panelID, RefID: "A"})
	require.NoError(t, err)
	assert.Equal(t, `rate(cpu_seconds_total{job="$job"}[5m])`, query["expr"])

	variable, err := resolveDashboardSelector(spec, true, DashboardSelector{Kind: "variable", Name: "job"})
	require.NoError(t, err)
	assert.Equal(t, "Job", variable["label"])
}

func TestResolveDashboardSelectorValidatesFields(t *testing.T) {
	panelID := 1
	tests := []struct {
		name     string
		selector DashboardSelector
		want     string
	}{
		{name: "unknown kind", selector: DashboardSelector{Kind: "row"}, want: "unsupported selector kind"},
		{name: "panel needs id", selector: DashboardSelector{Kind: "panel"}, want: "panelId is required"},
		{name: "query needs ref id", selector: DashboardSelector{Kind: "query", PanelID: &panelID}, want: "refId is required"},
		{name: "variable needs name", selector: DashboardSelector{Kind: "variable"}, want: "name is required"},
		{name: "dashboard rejects panel id", selector: DashboardSelector{Kind: "dashboard", PanelID: &panelID}, want: "does not accept"},
		{name: "panel rejects query field", selector: DashboardSelector{Kind: "panel", PanelID: &panelID, RefID: "A"}, want: "does not accept"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveDashboardSelector(map[string]interface{}{}, false, tt.selector)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestReadDashboardPropertyWithSelector(t *testing.T) {
	panelID := 17
	selector := &DashboardSelector{Kind: "panel", PanelID: &panelID}
	res := &dashboardResult{
		Spec: map[string]interface{}{
			"panels": []interface{}{
				map[string]interface{}{
					"id": float64(panelID),
					"fieldConfig": map[string]interface{}{
						"defaults": map[string]interface{}{"unit": "short"},
					},
				},
			},
		},
		Meta: &models.DashboardMeta{Version: 9},
	}

	result, err := readDashboardProperty(context.Background(), res, GetDashboardPropertyParams{
		UID:      "service-overview",
		JSONPath: "$.fieldConfig.defaults.unit",
		Selector: selector,
	})

	require.NoError(t, err)
	response, ok := result.(*DashboardPropertyResponse)
	require.True(t, ok)
	assert.Equal(t, "short", response.Value)
	assert.Equal(t, "9", response.Revision)
	assert.False(t, response.IsV2)
	assert.Equal(t, *selector, response.Selector)
}

func TestReadDashboardPropertyWithoutSelectorKeepsLegacyResponse(t *testing.T) {
	res := &dashboardResult{Spec: map[string]interface{}{"title": "Service overview"}}

	result, err := readDashboardProperty(context.Background(), res, GetDashboardPropertyParams{
		UID:      "service-overview",
		JSONPath: "$.title",
	})

	require.NoError(t, err)
	assert.Equal(t, "Service overview", result)
}

func TestReadDashboardPropertyUsesKubernetesRevision(t *testing.T) {
	res := &dashboardResult{
		Spec: map[string]interface{}{"title": "Service overview"},
		IsV2: true,
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{"resourceVersion": "42"},
		},
	}

	result, err := readDashboardProperty(context.Background(), res, GetDashboardPropertyParams{
		UID:      "service-overview",
		JSONPath: "$.title",
		Selector: &DashboardSelector{Kind: "dashboard"},
	})

	require.NoError(t, err)
	assert.Equal(t, "42", result.(*DashboardPropertyResponse).Revision)
	assert.True(t, result.(*DashboardPropertyResponse).IsV2)
}

func TestReadDashboardPropertyRejectsOversizedSelectorResponse(t *testing.T) {
	res := &dashboardResult{
		Spec: map[string]interface{}{
			"description": strings.Repeat("x", maxDashboardPropertyResponseBytes),
		},
	}

	_, err := readDashboardProperty(context.Background(), res, GetDashboardPropertyParams{
		UID:      "large-dashboard",
		JSONPath: "$.description",
		Selector: &DashboardSelector{Kind: "dashboard"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
	assert.Contains(t, err.Error(), "narrower JSONPath")
}
