//go:build unit

package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Expected values follow Grafana's interpolateQueryExpr (Prometheus, Loki) and
// interpolateVariable (SQL datasources) for multi-value and include-All variables.
func TestSubstituteQueryTemplateVariableValuesMultiValue(t *testing.T) {
	multi := map[string]bool{"job": true}
	tests := []struct {
		name, datasourceType, query, want string
		values                            []string
		multi                             map[string]bool
	}{
		{name: "Prometheus groups several values", datasourceType: "prometheus", query: `up{job=~"$job"}`, values: []string{"api", "web"}, multi: multi, want: `up{job=~"(api|web)"}`},
		{name: "Prometheus single selection is escaped, not grouped", datasourceType: "prometheus", query: `up{job=~"${job}"}`, values: []string{"api.v1"}, multi: multi, want: `up{job=~"api\\.v1"}`},
		{name: "Prometheus regex metacharacters", datasourceType: "prometheus", query: `up{job=~"[[job]]"}`, values: []string{"a+b", "c(d)|e", "$^*{}[]?"}, multi: multi, want: `up{job=~"(a\\+b|c\\(d\\)\\|e|\\$\\^\\*\\{\\}\\[\\]\\?)"}`},
		{name: "Prometheus backslash", datasourceType: "prometheus", query: `up{path=~"$job"}`, values: []string{`C:\tmp`, "x"}, multi: multi, want: `up{path=~"(C:\\\\tmp|x)"}`},
		{name: "Prometheus leaves single quotes unescaped", datasourceType: "prometheus", query: `up{name=~"$job"}`, values: []string{"O'Brien", "b"}, multi: multi, want: `up{name=~"(O'Brien|b)"}`},
		{name: "Loki joins without a group", datasourceType: "loki", query: `{job=~"$job"}`, values: []string{"api", "web.1"}, multi: multi, want: `{job=~"api|web\\.1"}`},
		{name: "PostgreSQL quotes each value", datasourceType: "grafana-postgresql-datasource", query: "WHERE job IN ($job)", values: []string{"api", "O'Reilly"}, multi: multi, want: "WHERE job IN ('api','O''Reilly')"},
		{name: "legacy postgres type", datasourceType: "postgres", query: "WHERE job IN ($job)", values: []string{"api"}, multi: multi, want: "WHERE job IN ('api')"},
		{name: "single-value variable keeps the raw value", datasourceType: "prometheus", query: `up{job=~"$job"}`, values: []string{"api.v1"}, want: `up{job=~"api.v1"}`},
		{name: "explicit format wins", datasourceType: "prometheus", query: `up{job=~"${job:pipe}"}`, values: []string{"api", "web"}, multi: multi, want: `up{job=~"{api,web}"}`},
		{name: "other datasources keep the first value", datasourceType: "influxdb", query: `WHERE job = '$job'`, values: []string{"api", "web"}, multi: multi, want: `WHERE job = 'api'`},
		{name: "other datasources keep the first value: stackdriver", datasourceType: "stackdriver", query: `x{a=~"$job"}`, values: []string{"a", "b"}, multi: multi, want: `x{a=~"a"}`},
		{name: "empty selection keeps the empty value", datasourceType: "prometheus", query: `up{job=~"$job"}`, values: []string{}, multi: multi, want: `up{job=~""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := substituteQueryTemplateVariableValues(tt.query, templateVariableValues{"job": tt.values}, tt.datasourceType, tt.multi)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMultiValueTemplateVariables(t *testing.T) {
	db := map[string]interface{}{"templating": map[string]interface{}{"list": []interface{}{
		map[string]interface{}{"name": "multi", "multi": true},
		map[string]interface{}{"name": "all", "includeAll": true},
		map[string]interface{}{"name": "single", "multi": false},
		map[string]interface{}{"name": "unset"},
	}}}
	assert.Equal(t, map[string]bool{"multi": true, "all": true}, multiValueTemplateVariables(db))
	assert.Empty(t, multiValueTemplateVariables(map[string]interface{}{}))
}

// multiValuePanelDashboard has one panel on the given datasource whose expr
// references the multi-value variable job, with two values selected.
func multiValuePanelDashboard(uid, dsType, expr string) map[string]interface{} {
	return map[string]interface{}{
		"templating": map[string]interface{}{"list": []interface{}{
			map[string]interface{}{"name": "job", "type": "query", "multi": true,
				"current": map[string]interface{}{"value": []interface{}{"api", "web.1"}}},
		}},
		"panels": []interface{}{map[string]interface{}{
			"id": float64(1), "title": "p",
			"datasource": map[string]interface{}{"uid": uid, "type": dsType},
			"targets":    []interface{}{map[string]interface{}{"refId": "A", "expr": expr}},
		}},
	}
}

// The selected values reach the datasource, formatted as Grafana sends them.
func TestRunSinglePanelQuery_MultiValueVariableReachesDatasource(t *testing.T) {
	tests := []struct {
		name, uid, dsType, expr, want, body string
	}{
		{name: "Prometheus", uid: "prom", dsType: "prometheus", expr: `sum(rate(http_requests_total{job=~"$job"}[5m]))`,
			want: `sum(rate(http_requests_total{job=~"(api|web\\.1)"}[5m]))`,
		},
		{name: "Loki", uid: "loki", dsType: "loki", expr: `{job=~"$job"}`,
			want: `{job=~"api|web\\.1"}`,
			body: `{"status":"success","data":{"resultType":"streams","result":[]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/api/datasources/uid/"+tt.uid:
					_, _ = io.WriteString(w, `{"id":1,"uid":"`+tt.uid+`","type":"`+tt.dsType+`"}`)
				case r.URL.Path == "/api/ds/query": // Prometheus
					var payload struct {
						Queries []map[string]interface{} `json:"queries"`
					}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					require.NotEmpty(t, payload.Queries)
					sent = append(sent, safeString(payload.Queries[0], "expr"))
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {}}})
				case strings.HasSuffix(r.URL.Path, "/api/v1/query_range"): // Loki
					require.NoError(t, r.ParseForm())
					sent = append(sent, r.Form.Get("query"))
					_, _ = io.WriteString(w, tt.body)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			_, err := runSinglePanelQuery(enforceTestCtx(server, false), singlePanelQueryParams{
				DB:      multiValuePanelDashboard(tt.uid, tt.dsType, tt.expr),
				PanelID: 1,
				Start:   "2024-01-15T10:00:00Z",
				End:     "2024-01-15T11:00:00Z",
			})
			require.NoError(t, err)
			require.NotEmpty(t, sent, "no range query reached the datasource")
			assert.Equal(t, tt.want, sent[len(sent)-1])
		})
	}
}
