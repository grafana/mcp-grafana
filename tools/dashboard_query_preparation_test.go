//go:build unit

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Existing extraction tests retain their preparation assertions through the
// same helper used by the public inspection handler.
func prepareTestPanelQueries(t *testing.T, panel map[string]interface{}, variables map[string]VariableInfo) []panelQuery {
	t.Helper()
	list := make([]interface{}, 0, len(variables))
	for name, variable := range variables {
		list = append(list, map[string]interface{}{
			"name": name, "current": map[string]interface{}{"value": variable.CurrentValue},
		})
	}
	queries, err := preparePanelQueries(map[string]interface{}{"templating": map[string]interface{}{"list": list}},
		DashboardPanelQueriesParams{Variables: map[string]string{}}, extractPanelQueries(panel), variables)
	require.NoError(t, err)
	return queries
}

func TestDashboardInspectionExecutionPreparationParity(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, tc := range []struct {
			name, query, want string
			currentText       string
			omitCurrentValue  bool
			defaultValue      string
			current           interface{}
			overrides         map[string]string
			all               bool
			kind, fallback    string
			datasource        string
			sourceCurrent     string
			raw, defaultRange bool
			multi             bool
			datasourceType    string
			targetFields      map[string]interface{}
			requiredNames     []string
		}{
			{name: "multi-value sqlstring", query: "SELECT ${choice:sqlstring}", current: []interface{}{"east", "west"}, want: "SELECT 'east','west'"},
			{name: "multi-value unformatted", query: "SELECT * FROM t WHERE region IN ($choice)", current: []interface{}{"east", "west"}, multi: true, want: "SELECT * FROM t WHERE region IN ('east','west')"},
			{name: "multi-value single selection", query: "SELECT * FROM t WHERE region IN (${choice})", current: []interface{}{"east"}, multi: true, want: "SELECT * FROM t WHERE region IN ('east')"},
			{name: "multi-value quoted value", query: "SELECT * FROM t WHERE region IN ([[choice]])", current: []interface{}{"O'Reilly", "west"}, multi: true, want: "SELECT * FROM t WHERE region IN ('O''Reilly','west')"},
			{name: "multi-value override", query: "SELECT * FROM t WHERE region IN ($choice)", current: []interface{}{"east", "west"}, multi: true, overrides: map[string]string{"choice": "north"}, want: "SELECT * FROM t WHERE region IN ('north')"},
			{name: "multi-value MySQL", query: "SELECT * FROM t WHERE region IN ($choice)", current: []interface{}{"east", "west"}, multi: true, datasourceType: "mysql", want: "SELECT * FROM t WHERE region IN ('east','west')"},
			{name: "multi-value MSSQL", query: "SELECT * FROM t WHERE region IN ($choice)", current: []interface{}{"east", "west"}, multi: true, datasourceType: "mssql", want: "SELECT * FROM t WHERE region IN ('east','west')"},
			{name: "multi-value explicit format kept", query: "SELECT '${choice:glob}'", current: []interface{}{"east", "west"}, multi: true, want: "SELECT '{east,west}'"},
			{name: "multi-value empty selection", query: "SELECT * FROM t WHERE region IN ($choice)", current: []interface{}{}, multi: true, want: "SELECT * FROM t WHERE region IN ()"},
			{name: "multi-value glob", query: "SELECT '${choice:glob}'", current: []interface{}{"east", "west"}, want: "SELECT '{east,west}'"},
			{name: "bracketed sqlstring", query: "SELECT [[choice:sqlstring]]", current: []interface{}{"east", "west"}, want: "SELECT 'east','west'"},
			{name: "quoted value", query: "SELECT ${choice:sqlstring}", current: "O'Reilly", want: "SELECT 'O''Reilly'"},
			{name: "BigQuery quoted value", query: "SELECT ${choice:sqlstring}", current: "O'Brien", datasourceType: "grafana-bigquery-datasource", targetFields: map[string]interface{}{"location": "$choice", "preservedField": "${choice:sqlstring}"}, want: `SELECT "O'Brien"`},
			{name: "BigQuery quotes and backslash", query: "SELECT ${choice:sqlstring}", current: `say "hi"\path`, datasourceType: "grafana-bigquery-datasource", want: `SELECT "say \"hi\"\\path"`},
			{name: "BigQuery newline", query: "SELECT ${choice:sqlstring}", current: "line1\nline2\t", datasourceType: "grafana-bigquery-datasource", want: `SELECT "line1\nline2\t"`},
			{name: "BigQuery multi-value sqlstring", query: "SELECT ${choice:sqlstring}", current: []interface{}{"O'Brien", `a\b`}, datasourceType: "grafana-bigquery-datasource", want: `SELECT "O'Brien","a\\b"`},
			{name: "BigQuery bracketed sqlstring", query: "SELECT [[choice:sqlstring]]", current: "O'Brien", datasourceType: "grafana-bigquery-datasource", want: `SELECT "O'Brien"`},
			{name: "BigQuery override", query: "SELECT ${choice:sqlstring}", current: "saved", overrides: map[string]string{"choice": "O'Brien"}, datasourceType: "grafana-bigquery-datasource", want: `SELECT "O'Brien"`},
			{name: "BigQuery empty string", query: "SELECT ${choice:sqlstring}", current: "", datasourceType: "grafana-bigquery-datasource", want: `SELECT ""`},
			{name: "BigQuery glob unchanged", query: "SELECT '${choice:glob}'", current: []interface{}{"east", "west"}, datasourceType: "grafana-bigquery-datasource", want: "SELECT '{east,west}'"},
			{name: "BigQuery raw inspection", query: "SELECT ${choice:sqlstring}", current: "O'Brien", datasourceType: "grafana-bigquery-datasource", raw: true},
			{name: "quoted list is saved data", query: "SELECT ${choice:sqlstring}", current: "'east','west'", want: "SELECT '''east'',''west'''"},
			{name: "override", query: "SELECT ${choice:sqlstring}", current: "saved", overrides: map[string]string{"choice": "override"}, want: "SELECT 'override'"},
			{name: "empty", query: "SELECT '${choice}'", current: "", want: "SELECT ''"},
			{name: "null", query: "SELECT '${choice}'", want: "SELECT ''"},
			{name: "null with All label", query: "SELECT '${choice}'", currentText: "All", want: "SELECT ''"},
			{name: "saved option metadata", query: "SELECT ${choice:sqlstring}", current: "saved", defaultValue: "east", want: "SELECT 'saved'"},
			{name: "time macros", query: "SELECT $__range_s, $__interval_ms", want: "SELECT 3600, 36000"},
			{name: "default time range", query: "SELECT $__range_s", want: "SELECT 3600", defaultRange: true},
			{name: "constant fallback", query: "SELECT ${choice:sqlstring}", kind: "Constant", fallback: "constant", want: "SELECT 'constant'"},
			{name: "textbox fallback", query: "SELECT ${choice:sqlstring}", kind: "Textbox", fallback: "text", want: "SELECT 'text'"},
			{name: "capture reference", query: "SELECT '$1'", want: "SELECT '$1'"},
			{name: "All scalar", query: "SELECT ${choice:sqlstring}", current: "$__all", all: true},
			{name: "text-only All", query: "SELECT ${choice:sqlstring}", currentText: "All", omitCurrentValue: true, all: true},
			{name: "text-only All override", query: "SELECT ${choice:sqlstring}", currentText: "All", omitCurrentValue: true, overrides: map[string]string{"choice": "east"}, want: "SELECT 'east'"},
			{name: "All array", query: "SELECT ${choice:sqlstring}", current: []interface{}{"$__all"}, all: true},
			{name: "All with stale array entries", query: "SELECT ${choice:sqlstring}", current: []interface{}{"$__all", "east"}, all: true},
			{name: "bracketed All", query: "SELECT [[choice:sqlstring]]", current: "$__all", all: true},
			{name: "All override", query: "SELECT ${choice:sqlstring}", current: "$__all", overrides: map[string]string{"choice": "east"}, want: "SELECT 'east'"},
			{name: "explicit All override", query: "SELECT ${choice:sqlstring}", current: "east", overrides: map[string]string{"choice": "$__all"}, all: true},
			{name: "All override without definition", query: "SELECT $other", overrides: map[string]string{"other": "$__all"}, all: true},
			{name: "unreferenced All", query: "SELECT 1", current: "$__all", want: "SELECT 1"},
			{name: "datasource variable", query: "SELECT 1", datasource: "$source", want: "SELECT 1"},
			{name: "All datasource variable", query: "SELECT 1", datasource: "$source", sourceCurrent: "$__all", all: true},
			{name: "All datasource override", query: "SELECT 1", datasource: "$source", sourceCurrent: "$__all", overrides: map[string]string{"source": "postgres-uid"}, want: "SELECT 1"},
			{name: "All SQL location", query: "SELECT 1", current: "$__all", targetFields: map[string]interface{}{"location": "$choice"}, datasourceType: "grafana-bigquery-datasource", all: true},
			{name: "All nested target", query: "SELECT 1", current: "$__all", targetFields: map[string]interface{}{"connectionArgs": map[string]interface{}{"project": "$choice"}}, datasourceType: "grafana-bigquery-datasource", all: true},
			{name: "SQL location override", query: "SELECT 1", current: "$__all", targetFields: map[string]interface{}{"location": "$choice"}, datasourceType: "grafana-bigquery-datasource", overrides: map[string]string{"choice": "us-east1"}, want: "SELECT 1"},
			{name: "InfluxQL macros", query: "SELECT mean(value) FROM cpu GROUP BY time($__interval)", datasourceType: "influxdb", targetFields: map[string]interface{}{"queryType": "influxql"}, want: "SELECT mean(value) FROM cpu GROUP BY time($__interval)"},
			{name: "Flux macros", query: "from(bucket: \"metrics\") |> aggregateWindow(every: ${__interval}, fn: mean)", datasourceType: "influxdb", targetFields: map[string]interface{}{"queryType": "flux"}, want: "from(bucket: \"metrics\") |> aggregateWindow(every: ${__interval}, fn: mean)"},
			{name: "InfluxQL macro and variable", query: "SELECT mean(value) FROM $choice GROUP BY time($__interval)", current: "cpu", datasourceType: "influxdb", targetFields: map[string]interface{}{"queryType": "influxql"}, want: "SELECT mean(value) FROM cpu GROUP BY time($__interval)", requiredNames: []string{"choice"}},
			{name: "CloudWatch macro and variable", query: "SELECT $choice, $__interval_ms", current: "CPUUtilization", datasourceType: "cloudwatch", want: "SELECT CPUUtilization, 36000", requiredNames: []string{"choice"}},
			{name: "CloudWatch macros", query: "SELECT $__interval_ms", datasourceType: "cloudwatch", want: "SELECT 36000"},
			{name: "ClickHouse interval macros", query: "SELECT $__interval, $__interval_ms, $__interval_s LIMIT 100", datasourceType: "grafana-clickhouse-datasource", want: "SELECT 3s, 3000, $__interval_s LIMIT 100"},
			{name: "ClickHouse time macros", query: "SELECT $__from, $__to FROM logs WHERE $__timeFilter(ts) LIMIT 100", datasourceType: "grafana-clickhouse-datasource", want: "SELECT 1704067200000, 1704070800000 FROM logs WHERE ts >= toDateTime(1704067200) AND ts <= toDateTime(1704070800) LIMIT 100"},
			{name: "ClickHouse macro and variable", query: "SELECT $choice, $__interval FROM logs WHERE $__timeFilter(ts) LIMIT 100", current: "value", datasourceType: "grafana-clickhouse-datasource", want: "SELECT value, 3s FROM logs WHERE ts >= toDateTime(1704067200) AND ts <= toDateTime(1704070800) LIMIT 100", requiredNames: []string{"choice"}},
			{name: "raw inspection", query: "SELECT ${choice:sqlstring}", current: "$__all", raw: true},
		} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, tc.name), func(t *testing.T) {
				variable := map[string]interface{}{"name": "choice", "type": "query", "datasource": nil,
					"current": map[string]interface{}{"value": tc.current}}
				if tc.multi {
					variable["multi"] = true
				}
				current := safeObject(variable, "current")
				if tc.currentText != "" {
					current["text"] = tc.currentText
				}
				if tc.omitCurrentValue {
					delete(current, "value")
				}
				if tc.defaultValue != "" {
					variable["options"] = []interface{}{map[string]interface{}{"value": tc.defaultValue}}
				}
				kind := "Query"
				if tc.kind != "" {
					kind = tc.kind
					variable["type"] = strings.ToLower(kind)
					variable["query"] = tc.fallback
					delete(variable, "current")
				}
				if kind == "Textbox" {
					kind = "Text"
				}
				datasourceType := tc.datasourceType
				if datasourceType == "" {
					datasourceType = "postgres"
				}
				expressionField := "rawSql"
				switch datasourceType {
				case "influxdb":
					expressionField = "query"
				case "cloudwatch":
					expressionField = "expression"
				}
				target := map[string]interface{}{expressionField: tc.query}
				for key, value := range tc.targetFields {
					target[key] = value
				}
				ds := map[string]interface{}{"uid": "postgres-uid", "type": datasourceType}
				panelUID := "postgres-uid"
				if tc.datasource != "" {
					panelUID = tc.datasource
				}
				db := map[string]interface{}{
					"templating": map[string]interface{}{"list": []interface{}{variable}},
					"panels": []interface{}{map[string]interface{}{
						"id": float64(1), "datasource": map[string]interface{}{"uid": panelUID, "type": datasourceType},
						"targets": []interface{}{target},
					}},
				}
				target["refId"] = "A"
				target["datasource"] = map[string]interface{}{"uid": panelUID}
				if v2 {
					db = map[string]interface{}{
						"variables": []interface{}{map[string]interface{}{"kind": kind + "Variable", "spec": variable}},
						"elements": map[string]interface{}{"panel": map[string]interface{}{"kind": "Panel", "spec": map[string]interface{}{
							"id": float64(1), "data": map[string]interface{}{"spec": map[string]interface{}{"queries": []interface{}{
								map[string]interface{}{"spec": map[string]interface{}{"refId": "A", "query": map[string]interface{}{
									"group": datasourceType, "datasource": map[string]interface{}{"name": panelUID}, "spec": target,
								}}},
							}}},
						}}},
					}
				}
				if tc.datasource != "" {
					sourceValue := tc.sourceCurrent
					if sourceValue == "" {
						sourceValue = "postgres-uid"
					}
					source := map[string]interface{}{"name": "source", "type": "datasource", "current": map[string]interface{}{"value": sourceValue}}
					if v2 {
						db["variables"] = append(safeArray(db, "variables"), map[string]interface{}{"kind": "DatasourceVariable", "spec": source})
					} else {
						safeObject(db, "templating")["list"] = append(safeArray(safeObject(db, "templating"), "list"), source)
					}
				}
				var submitted string
				queryCalls, metadataCalls := 0, 0
				ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if v2 && serveDashboardDiscovery(w, r, "v1beta1", "v2beta1") {
						return
					}
					switch {
					case r.URL.Path == "/api/datasources/uid/postgres-uid":
						metadataCalls++
						_ = json.NewEncoder(w).Encode(ds)
					case r.URL.Path == "/api/dashboards/uid/test":
						_ = json.NewEncoder(w).Encode(map[string]interface{}{"dashboard": db})
					case v2 && strings.Contains(r.URL.Path, "/v1beta1/"):
						_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": map[string]interface{}{"conversion": map[string]interface{}{"storedVersion": "v2beta1"}}})
					case v2 && strings.Contains(r.URL.Path, "/v2beta1/"):
						_ = json.NewEncoder(w).Encode(map[string]interface{}{"apiVersion": "dashboard.grafana.app/v2beta1", "spec": db})
					case r.URL.Path == "/api/ds/query":
						queryCalls++
						var payload map[string]interface{}
						require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
						submittedTarget := safeArray(payload, "queries")[0].(map[string]interface{})
						submitted = safeString(submittedTarget, expressionField)
						if tc.name == "SQL location override" {
							assert.Equal(t, "us-east1", submittedTarget["location"])
						}
						if tc.name == "BigQuery quoted value" {
							assert.Equal(t, "O'Brien", submittedTarget["location"], "non-SQL target fields must not be quoted as SQL literals")
							assert.Equal(t, "'O''Brien'", submittedTarget["preservedField"], "non-SQL target fields keep generic interpolation")
						}
						frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []int64{1}))}
						_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
					default:
						metadataCalls++
						t.Errorf("unexpected request: %s", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(ts.Close)
				ctx := enforceTestCtx(ts, false)
				if v2 {
					ctx = mcpgrafana.WithKubernetesClient(ctx, &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()})
				}
				variables := tc.overrides
				if variables == nil {
					variables = map[string]string{}
				}
				args := DashboardPanelQueriesParams{UID: "test", Variables: variables, Start: "1704067200000", End: "1704070800000"}
				if tc.defaultRange {
					args.Start, args.End = "", ""
				}
				if tc.raw {
					args = DashboardPanelQueriesParams{UID: "test"}
				}
				queries, err := GetDashboardPanelQueriesTool(ctx, args)
				require.NoError(t, err)
				require.Len(t, queries, 1)
				encoded, err := json.Marshal(queries[0])
				require.NoError(t, err)
				var publicQuery map[string]interface{}
				require.NoError(t, json.Unmarshal(encoded, &publicQuery))
				assert.NotContains(t, publicQuery, "target", "expression targets keep their existing response shape")
				assert.NotContains(t, publicQuery, "rawTarget")
				assert.Zero(t, queryCalls, "inspection must not execute option queries")
				assert.Zero(t, metadataCalls, "inspection must not resolve datasource types via the API")
				if tc.raw {
					assert.Equal(t, tc.query, queries[0].Query)
					assert.Empty(t, queries[0].ProcessedQuery)
					assert.Empty(t, queries[0].Warnings)
					return
				}
				if tc.all {
					require.Len(t, queries[0].Warnings, 1)
					assert.Contains(t, queries[0].Warnings[0], "Supply an explicit value")
					assert.Empty(t, queries[0].ProcessedQuery)
					result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: "test", PanelIDs: []int{1}, Variables: tc.overrides, Start: args.Start, End: args.End})
					require.NoError(t, err)
					assert.Equal(t, queries[0].Warnings[0], result.Errors[1])
					assert.Empty(t, result.Results)
					assert.Zero(t, queryCalls, "All selections must be rejected before execution")
					assert.Zero(t, metadataCalls, "All selections must be rejected before datasource lookup")
					return
				}
				require.Empty(t, queries[0].Warnings)
				if tc.defaultValue != "" {
					require.Len(t, queries[0].RequiredVariables, 1)
					assert.Equal(t, tc.defaultValue, queries[0].RequiredVariables[0].DefaultValue)
				}
				if strings.Contains(tc.name, "macros") || tc.defaultRange {
					assert.Empty(t, queries[0].RequiredVariables, "frontend macros are not dashboard variables")
				}
				if tc.requiredNames != nil {
					var names []string
					for _, variable := range queries[0].RequiredVariables {
						names = append(names, variable.Name)
					}
					assert.ElementsMatch(t, tc.requiredNames, names)
				}
				assert.Equal(t, tc.want, queries[0].ProcessedQuery)
				assert.Equal(t, datasourceInfo{UID: "postgres-uid", Type: datasourceType}, queries[0].Datasource)
				result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: "test", PanelIDs: []int{1}, Variables: tc.overrides, Start: args.Start, End: args.End})
				require.NoError(t, err)
				require.Empty(t, result.Errors)
				assert.Equal(t, queries[0].ProcessedQuery, submitted)
				assert.Equal(t, 1, queryCalls)
				assert.Equal(t, queries[0].Datasource.UID, result.Results[1].DatasourceUID)
			})
		}
	}
}

func TestRunPanelQueryAllDatasourceOverride(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("v2=%t", v2), func(t *testing.T) {
			variable := map[string]interface{}{"name": "source", "type": "datasource", "current": map[string]interface{}{"value": "$__all"}}
			target := map[string]interface{}{"refId": "A", "namespace": "AWS/EC2", "metricName": "CPUUtilization", "region": "us-east-1",
				"datasource": map[string]interface{}{"uid": "$source", "type": "cloudwatch"}}
			db := map[string]interface{}{
				"templating": map[string]interface{}{"list": []interface{}{variable}},
				"panels": []interface{}{map[string]interface{}{
					"id": float64(1), "targets": []interface{}{target},
				}},
			}
			if v2 {
				db = map[string]interface{}{
					"variables": []interface{}{map[string]interface{}{"kind": "DatasourceVariable", "spec": variable}},
					"elements": map[string]interface{}{"panel": map[string]interface{}{"kind": "Panel", "spec": map[string]interface{}{
						"id": float64(1), "data": map[string]interface{}{"spec": map[string]interface{}{"queries": []interface{}{
							map[string]interface{}{"spec": map[string]interface{}{"refId": "A", "query": map[string]interface{}{
								"group": "cloudwatch", "datasource": map[string]interface{}{"name": "$source"}, "spec": target,
							}}},
						}}},
					}}},
				}
			}
			queryCalls := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/datasources/uid/cloudwatch-override":
					_ = json.NewEncoder(w).Encode(map[string]string{"uid": "cloudwatch-override", "type": "cloudwatch"})
				case "/api/ds/query":
					queryCalls++
					var payload map[string]interface{}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					submitted := safeArray(payload, "queries")[0].(map[string]interface{})
					assert.Equal(t, "cloudwatch-override", safeString(safeObject(submitted, "datasource"), "uid"))
					_, _ = w.Write([]byte(`{"results":{}}`))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(ts.Close)
			params := singlePanelQueryParams{DB: db, IsV2: v2, PanelID: 1, Start: "1704067200000", End: "1704070800000"}
			_, err := runSinglePanelQuery(enforceTestCtx(ts, false), params)
			require.EqualError(t, err, `Variable "source" is set to All. Supply an explicit value in variables.`)
			assert.Zero(t, queryCalls)
			params.DsUID = "cloudwatch-override"
			_, err = runSinglePanelQuery(enforceTestCtx(ts, false), params)
			require.NoError(t, err)
			assert.Equal(t, 1, queryCalls)
			assert.Equal(t, "$source", safeString(safeObject(target, "datasource"), "uid"))
			target["namespace"] = "$source"
			_, err = runSinglePanelQuery(enforceTestCtx(ts, false), params)
			require.EqualError(t, err, `Variable "source" is set to All. Supply an explicit value in variables.`)
			assert.Equal(t, 1, queryCalls, "datasource overrides must not skip All checks in other target fields")
		})
	}
}
