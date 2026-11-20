//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDashboardSummary_IncludesRowNestedPanels(t *testing.T) {
	dashboard := map[string]interface{}{
		"title": "rows",
		"panels": []interface{}{
			map[string]interface{}{"id": float64(1), "title": "Open", "type": "timeseries", "targets": []interface{}{map[string]interface{}{"refId": "A"}}},
			map[string]interface{}{
				"id": float64(2), "title": "Collapsed", "type": "row", "collapsed": true,
				"panels": []interface{}{
					map[string]interface{}{"id": float64(3), "title": "Hidden A", "type": "stat", "targets": []interface{}{map[string]interface{}{"refId": "A"}}},
					map[string]interface{}{"id": float64(4), "title": "Hidden B", "type": "gauge", "targets": []interface{}{map[string]interface{}{"refId": "A"}, map[string]interface{}{"refId": "B"}}},
				},
			},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/dashboards/uid/rows" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"dashboard": dashboard, "meta": map[string]interface{}{}})
	}))
	defer server.Close()

	summary, err := getDashboardSummary(enforceTestCtx(server, false), GetDashboardSummaryParams{UID: "rows"})
	require.NoError(t, err)

	var ids []int
	for _, p := range summary.Panels {
		ids = append(ids, p.ID)
	}
	assert.Equal(t, []int{1, 2, 3, 4}, ids, "panels collapsed into a row must be listed")
	assert.Equal(t, 4, summary.PanelCount)
	require.Len(t, summary.Panels, 4)
	assert.Equal(t, 2, summary.Panels[3].QueryCount)
}
