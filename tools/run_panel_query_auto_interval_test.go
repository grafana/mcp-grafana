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

// An interval variable set to "auto" is stored as "$__auto_interval_<name>".
// Grafana replaces it with an interval computed from the time range, the
// variable's step count and its minimum (MySQL Overview, 7362, does this), so
// the query sent to the datasource must carry that interval, not the placeholder.
func TestRunSinglePanelQuery_AutoIntervalVariable(t *testing.T) {
	tests := []struct {
		name, expr, start, end, want string
		variable                     map[string]interface{}
	}{
		{
			name: "auto_count and auto_min from the dashboard", expr: "rate(q[$interval])",
			start: "2024-01-15T10:00:00Z", end: "2024-01-15T11:00:00Z", want: "rate(q[20s])",
			variable: map[string]interface{}{"auto_count": float64(200), "auto_min": "1s"},
		},
		{
			name: "Grafana's defaults: 30 steps, 10s minimum", expr: "rate(q[$interval])",
			start: "2024-01-15T10:00:00Z", end: "2024-01-15T11:00:00Z", want: "rate(q[2m])",
			variable: map[string]interface{}{},
		},
		{
			name: "the minimum wins on a short range", expr: "rate(q[${interval}])",
			start: "2024-01-15T10:00:00Z", end: "2024-01-15T10:05:00Z", want: "rate(q[10s])",
			variable: map[string]interface{}{"auto_count": float64(200), "auto_min": "10s"},
		},
		{
			name: "a day rounds to an hour", expr: "rate(q[$interval])",
			start: "2024-01-15T00:00:00Z", end: "2024-01-16T00:00:00Z", want: "rate(q[1h])",
			variable: map[string]interface{}{},
		},
		{
			name: "legacy $__auto_interval references", expr: "rate(q[$__auto_interval_interval]) + rate(q[$__auto_interval])",
			start: "2024-01-15T10:00:00Z", end: "2024-01-15T11:00:00Z", want: "rate(q[2m]) + rate(q[2m])",
			variable: map[string]interface{}{},
		},
		{
			name: "a fixed selection is left alone", expr: "rate(q[$interval])",
			start: "2024-01-15T10:00:00Z", end: "2024-01-15T11:00:00Z", want: "rate(q[5m])",
			variable: map[string]interface{}{"current": map[string]interface{}{"text": "5m", "value": "5m"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sentExpr string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/datasources/uid/prom-uid":
					_, _ = io.WriteString(w, `{"uid":"prom-uid","type":"prometheus"}`)
				case "/api/ds/query":
					var payload struct {
						Queries []map[string]interface{} `json:"queries"`
					}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					sentExpr = safeString(payload.Queries[0], "expr")
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {}}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			variable := map[string]interface{}{
				"name": "interval", "type": "interval", "auto": true,
				"query":   "1m,5m,1h",
				"current": map[string]interface{}{"text": "auto", "value": "$__auto_interval_interval"},
			}
			for k, v := range tt.variable {
				variable[k] = v
			}
			db := map[string]interface{}{
				"templating": map[string]interface{}{"list": []interface{}{variable}},
				"panels": []interface{}{map[string]interface{}{
					"id": float64(1), "title": "rate",
					"datasource": map[string]interface{}{"type": "prometheus", "uid": "prom-uid"},
					"targets":    []interface{}{map[string]interface{}{"refId": "A", "expr": tt.expr}},
				}},
			}
			_, err := runSinglePanelQuery(enforceTestCtx(server, false), singlePanelQueryParams{DB: db, PanelID: 1, Start: tt.start, End: tt.end})
			require.NoError(t, err)
			assert.Equal(t, tt.want, sentExpr)
		})
	}
}

// grafanaCalculateInterval matches Grafana's rangeutil.calculateInterval output.
func TestGrafanaCalculateInterval(t *testing.T) {
	assert.Equal(t, "20s", grafanaCalculateInterval(3600e9, 200, "1s"))
	assert.Equal(t, "500ms", grafanaCalculateInterval(60e9, 100, "1ms"))
	assert.Equal(t, "1d", grafanaCalculateInterval(7*24*3600e9, 7, "10s"))
	assert.Equal(t, "1y", grafanaCalculateInterval(10*365*24*3600e9, 2, "10s"))
}
