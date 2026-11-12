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

// Dashboards saved before schema version 33 reference a datasource by name, as
// a plain string or through a datasource variable whose value is the name. A
// panel with no reference at all runs on the default datasource.
func TestRunSinglePanelQuery_LegacyDatasourceReferences(t *testing.T) {
	const promDS = `{"id":1,"uid":"prom-uid","name":"Prometheus","type":"prometheus"}`
	tests := []struct {
		name       string
		datasource interface{}
		omit       bool
		dsVariable string
		wantErr    string
	}{
		{name: "datasource name", datasource: "Prometheus"},
		{name: "variable holding a datasource name", datasource: "$ds", dsVariable: "Prometheus"},
		{name: "variable holding a datasource UID", datasource: "${ds}", dsVariable: "prom-uid"},
		{name: "null datasource", datasource: nil},
		{name: "no datasource", omit: true},
		{name: "unknown name", datasource: "Missing", wantErr: "could not resolve datasource 'Missing'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sentUID, sentExpr string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/datasources/uid/prom-uid", "/api/datasources/name/Prometheus":
					_, _ = io.WriteString(w, promDS)
				case "/api/datasources":
					_, _ = io.WriteString(w, `[{"id":1,"uid":"prom-uid","name":"Prometheus","type":"prometheus","isDefault":true}]`)
				case "/api/frontend/settings":
					_, _ = io.WriteString(w, `{"datasources":{"Prometheus":`+promDS+`}}`)
				case "/api/ds/query":
					var payload struct {
						Queries []map[string]interface{} `json:"queries"`
					}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					require.NotEmpty(t, payload.Queries)
					sentUID = safeString(safeObject(payload.Queries[0], "datasource"), "uid")
					sentExpr = safeString(payload.Queries[0], "expr")
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {}}})
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"message":"Data source not found"}`)
				}
			}))
			defer server.Close()

			panel := map[string]interface{}{
				"id":      float64(1),
				"title":   "requests",
				"targets": []interface{}{map[string]interface{}{"refId": "A", "expr": "up"}},
			}
			if !tt.omit {
				panel["datasource"] = tt.datasource
			}
			db := map[string]interface{}{"panels": []interface{}{panel}}
			if tt.dsVariable != "" {
				db["templating"] = map[string]interface{}{"list": []interface{}{
					map[string]interface{}{"name": "ds", "type": "datasource", "current": map[string]interface{}{"value": tt.dsVariable}},
				}}
			}

			result, err := runSinglePanelQuery(enforceTestCtx(server, false), singlePanelQueryParams{
				DB:      db,
				PanelID: 1,
				Start:   "2024-01-15T10:00:00Z",
				End:     "2024-01-15T11:00:00Z",
			})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "prom-uid", result.DatasourceUID)
			assert.Equal(t, "prometheus", result.DatasourceType)
			assert.Equal(t, "prom-uid", sentUID)
			assert.Equal(t, "up", sentExpr)
		})
	}
}
