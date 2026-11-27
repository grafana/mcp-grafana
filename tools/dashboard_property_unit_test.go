package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const v1DashboardPropertyFixture = `{
	"title": "Service",
	"panels": [
		{"id": 1, "type": "row", "title": "Overview"},
		{"id": 2, "type": "timeseries", "title": "Requests \"total\""},
		{"id": 3, "type": "table", "title": "Errors"}
	],
	"templating": {"list": [{"name": "job"}]}
}`

const v2DashboardPropertyFixture = `{
	"title": "Service",
	"elements": {"panel-1": {"kind": "Panel"}},
	"layout": {"kind": "GridLayout"},
	"variables": [{"kind": "QueryVariable"}]
}`

func decodeDashboardPropertyFixture(t *testing.T, fixture string) interface{} {
	t.Helper()
	var data interface{}
	require.NoError(t, json.Unmarshal([]byte(fixture), &data))
	return data
}

func TestEvalDashboardProperty(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected interface{}
	}{
		{"double-quoted filter", `$.panels[?(@.type=="row")].title`, []interface{}{"Overview"}},
		{"single-quoted filter", `$.panels[?(@.type=='row')].title`, []interface{}{"Overview"}},
		{"single-quoted filter with embedded double quote", `$.panels[?(@.title=='Requests "total"')].id`, []interface{}{float64(2)}},
		{"single-quoted filter with escaped single quote", `$.panels[?(@.title=='it\'s')].id`, []interface{}{}},
		{"single-quoted key", `$['title']`, "Service"},
		{"single-quoted multi-key", `$.panels[0]['id','title']`, []interface{}{float64(1), "Overview"}},
		{"single-quoted multi-key over wildcard", `$.panels[*]['id','title']`, []interface{}{float64(1), "Overview", float64(2), `Requests "total"`, float64(3), "Errors"}},
		{"single-character single-quoted key", `$.panels[?(@.type=='x')].id`, []interface{}{}},
	}
	data := decodeDashboardPropertyFixture(t, v1DashboardPropertyFixture)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evalDashboardProperty(context.Background(), data, tt.path, false)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEvalDashboardPropertySchemaHint(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		isV2     bool
		path     string
		contains []string
	}{
		{
			name:     "v1 path on v2 dashboard",
			fixture:  v2DashboardPropertyFixture,
			isV2:     true,
			path:     "$.templating.list",
			contains: []string{"unknown key templating", "v2 schema", "elements, layout, title, variables"},
		},
		{
			name:     "v2 path on v1 dashboard",
			fixture:  v1DashboardPropertyFixture,
			path:     "$.variables",
			contains: []string{"unknown key variables", "classic v1 schema", "panels, templating, title"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := decodeDashboardPropertyFixture(t, tt.fixture)
			_, err := evalDashboardProperty(context.Background(), data, tt.path, tt.isV2)
			require.Error(t, err)
			for _, s := range tt.contains {
				assert.Contains(t, err.Error(), s)
			}
		})
	}
}
