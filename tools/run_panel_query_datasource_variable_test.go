//go:build unit

package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A datasource variable saved without a value (as grafana.com dashboards such as
// Node Exporter Full are) resolves the way Grafana resolves it on load: the first
// datasource of the variable's plugin type, sorted by name, matching its regex.
func TestRunSinglePanelQuery_DatasourceVariableWithoutValue(t *testing.T) {
	const list = `[
		{"id":1,"uid":"loki-uid","name":"A logs","type":"loki","isDefault":true},
		{"id":2,"uid":"zeta-uid","name":"Zeta","type":"prometheus"},
		{"id":3,"uid":"alpha-uid","name":"alpha","type":"prometheus"},
		{"id":4,"uid":"prod-uid","name":"PROD metrics","type":"prometheus"}
	]`
	tests := []struct {
		name, regex, want, wantErr string
		pluginID                   string
		v2                         bool
		current                    map[string]interface{}
	}{
		{name: "first by name, case-insensitive", pluginID: "prometheus", want: "alpha-uid"},
		// Kubernetes / Views / Global (15757) saves {"text": "", "value": ""}.
		{name: "empty current value", pluginID: "prometheus", current: map[string]interface{}{"text": "", "value": ""}, want: "alpha-uid"},
		{name: "regex filters names", pluginID: "prometheus", regex: "/^Z/", want: "zeta-uid"},
		{name: "regex flags", pluginID: "prometheus", regex: "/prod/i", want: "prod-uid"},
		{name: "bare regex", pluginID: "prometheus", regex: "^Zeta$", want: "zeta-uid"},
		{name: "schema v2 variable", pluginID: "prometheus", v2: true, want: "alpha-uid"},
		{name: "no datasource of the type", pluginID: "tempo", wantErr: "datasource variable '${ds}' not found"},
		{name: "no name matches the regex", pluginID: "prometheus", regex: "/^nope/", wantErr: "datasource variable '${ds}' not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sentUID string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/datasources":
					_, _ = io.WriteString(w, list)
				case "/api/datasources/uid/alpha-uid", "/api/datasources/uid/zeta-uid", "/api/datasources/uid/prod-uid":
					uid := r.URL.Path[len("/api/datasources/uid/"):]
					_, _ = io.WriteString(w, `{"uid":"`+uid+`","type":"prometheus"}`)
				case "/api/ds/query":
					var payload struct {
						Queries []map[string]interface{} `json:"queries"`
					}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					sentUID = safeString(safeObject(payload.Queries[0], "datasource"), "uid")
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {}}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			current := tt.current
			if current == nil {
				current = map[string]interface{}{}
			}
			variable := map[string]interface{}{"name": "ds", "type": "datasource", "query": tt.pluginID, "current": current}
			if tt.regex != "" {
				variable["regex"] = tt.regex
			}
			db := map[string]interface{}{
				"templating": map[string]interface{}{"list": []interface{}{variable}},
				"panels": []interface{}{map[string]interface{}{
					"id": float64(1), "title": "load",
					"datasource": map[string]interface{}{"type": "prometheus", "uid": "${ds}"},
					"targets":    []interface{}{map[string]interface{}{"refId": "A", "expr": "up"}},
				}},
			}
			params := singlePanelQueryParams{DB: db, PanelID: 1, Start: "2024-01-15T10:00:00Z", End: "2024-01-15T11:00:00Z"}
			if tt.v2 {
				spec := map[string]interface{}{"name": "ds", "pluginId": tt.pluginID, "current": map[string]interface{}{}}
				params.IsV2 = true
				params.DB = map[string]interface{}{
					"variables": []interface{}{map[string]interface{}{"kind": "DatasourceVariable", "spec": spec}},
					"elements": map[string]interface{}{"panel-1": map[string]interface{}{"kind": "Panel", "spec": map[string]interface{}{
						"id": float64(1), "title": "load",
						"data": map[string]interface{}{"spec": map[string]interface{}{"queries": []interface{}{
							map[string]interface{}{"spec": map[string]interface{}{"refId": "A", "query": map[string]interface{}{
								"group": "prometheus", "datasource": map[string]interface{}{"name": "${ds}"},
								"spec": map[string]interface{}{"expr": "up"},
							}}},
						}}},
					}}},
				}
			}

			result, err := runSinglePanelQuery(enforceTestCtx(server, false), params)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.DatasourceUID)
			assert.Equal(t, tt.want, sentUID)
		})
	}
}
