//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPanelGrafanaResponseEmptiness(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response backend.Responses
		empty    bool
	}{
		{"no responses", backend.Responses{}, true},
		{"no frames", backend.Responses{"A": {}}, true},
		{"empty fields", backend.Responses{"A": {Frames: data.Frames{data.NewFrame("", data.NewField("value", nil, []float64{}))}}}, true},
		{"nil frame", backend.Responses{"A": {Frames: data.Frames{nil}}}, true},
		{"one row", backend.Responses{"A": {Frames: data.Frames{data.NewFrame("", data.NewField("value", nil, []float64{0}))}}}, false},
		{"mixed refs", backend.Responses{"A": {}, "B": {Frames: data.Frames{data.NewFrame("", data.NewField("value", nil, []float64{1}))}}}, false},
		{"error is not no data", backend.Responses{"A": {Error: errors.New("failed")}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.empty, isEmptyPanelResult(tc.response)) })
	}
}

func TestPanelGrafanaEmbeddedErrors(t *testing.T) {
	for _, tc := range []struct{ name, body, wantErr string }{
		{"error", `{"results":{"A":{"error":"upstream failed","status":500}}}`, "upstream failed"},
		{"status only", `{"results":{"A":{"status":404}}}`, "status 404"},
		{"empty success", `{"results":{"A":{"frames":[]}}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/ds/query", r.URL.Path)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			result, err := executeGrafanaDSQuery(mockDatasourcesCtx(ts), map[string]interface{}{})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.True(t, isEmptyPanelResult(result))
			}
		})
	}
}

func TestCloudWatchTargetFrontendMacros(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		db := preparationFixture(v2, map[string]interface{}{
			"name": "selection", "type": "custom", "current": map[string]interface{}{"value": "$__range"},
		}, "")
		if v2 {
			db = templatingV1FromV2(db)
		}
		target := map[string]interface{}{
			"expression": "SEARCH($__range_s)",
			"dimensions": map[string]interface{}{"InstanceId": []interface{}{"$selection", "$__interval"}},
			"period":     "$__period_auto",
		}
		prepared, err := prepareDashboardQuery(context.Background(), db, "SEARCH($__range_s)", target, datasourceInfo{UID: "cw", Type: "cloudwatch"}, nil,
			"2026-10-19T00:00:00Z", "2026-10-19T01:00:00Z", "", "", nil)
		require.NoError(t, err)
		assert.Equal(t, "SEARCH(3600)", prepared.Target["expression"])
		assert.Equal(t, "SEARCH(3600)", prepared.Query, "displayed query must match the executed expression")
		assert.Equal(t, "$__period_auto", prepared.Target["period"])
		encoded, err := json.Marshal(prepared.Target)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), "$__range", "macro-like selected values remain literal")
		assert.NotContains(t, string(encoded), "$__interval")
		assert.Equal(t, "SEARCH($__range_s)", target["expression"], "saved target must remain untouched")
	}
}
