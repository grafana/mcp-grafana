//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardOptionTargetFields(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("v2=%t", v2), func(t *testing.T) {
			var sent []map[string]interface{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/datasources/uid/bigquery-uid" {
					_, _ = w.Write([]byte(`{"uid":"bigquery-uid","type":"grafana-bigquery-datasource"}`))
					return
				}
				require.Equal(t, "/api/ds/query", r.URL.Path)
				var payload map[string]interface{}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				target := safeArray(payload, "queries")[0].(map[string]interface{})
				sent = append(sent, target)
				frames := data.Frames{data.NewFrame("", data.NewField("__value", nil, []string{safeString(target, "dataset")}))}
				_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
			}))
			t.Cleanup(ts.Close)
			ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, true)
			cache := make(variableOptionsCache)
			// The SQL and datasource are identical. Only the target's dataset varies.
			for _, dataset := range []string{"first", "second", "first"} {
				optionTarget := map[string]interface{}{
					"rawSql": "SELECT option", "project": "test-project", "dataset": dataset,
					"location": "${region}", "extra": map[string]interface{}{"nested": []interface{}{"${region}"}},
				}
				db := preparationFixture(v2, map[string]interface{}{
					"name": "choice", "type": "query", "current": map[string]interface{}{"value": "$__all"},
					"query": optionTarget, "datasource": map[string]interface{}{"uid": "bigquery-uid"},
				}, "SELECT ${choice:sqlstring}")
				region := map[string]interface{}{
					"name": "region", "type": "custom", "current": map[string]interface{}{"value": "$__all"}, "allValue": "us-east1",
				}
				if v2 {
					spec := safeObject(safeArray(db, "variables")[0].(map[string]interface{}), "spec")
					spec["query"] = map[string]interface{}{
						"kind": "DataQuery", "group": "grafana-bigquery-datasource",
						"datasource": map[string]interface{}{"name": "bigquery-uid"}, "spec": optionTarget,
					}
					db["variables"] = append(safeArray(db, "variables"), map[string]interface{}{"kind": "CustomVariable", "spec": region})
					db = templatingV1FromV2(db)
				} else {
					templating := safeObject(db, "templating")
					templating["list"] = append(safeArray(templating, "list"), region)
				}
				prepared, err := prepareDashboardQuery(ctx, db, "SELECT ${choice:sqlstring}", nil,
					datasourceInfo{UID: "bigquery-uid", Type: "grafana-bigquery-datasource"}, nil,
					"1704067200000", "1704070800000", "", "", cache)
				require.NoError(t, err)
				require.Empty(t, prepared.Warnings)
				assert.Equal(t, "SELECT '"+dataset+"'", prepared.Query)
				assert.Equal(t, "${region}", optionTarget["location"], "the saved query must remain untouched")
				assert.Equal(t, []interface{}{"${region}"}, safeObject(optionTarget, "extra")["nested"])
			}
			require.Len(t, sent, 2, "different datasets need separate queries, identical targets reuse the result")
			for i, target := range sent {
				assert.Equal(t, "SELECT option", target["rawSql"])
				assert.Equal(t, "test-project", target["project"])
				assert.Equal(t, []string{"first", "second"}[i], target["dataset"])
				assert.Equal(t, "us-east1", target["location"])
				assert.Equal(t, []interface{}{"us-east1"}, safeObject(target, "extra")["nested"])
				assert.Equal(t, float64(SQLFormatTable), target["format"])
				assert.Equal(t, true, target["rawQuery"])
			}
		})
	}
}

func TestDashboardOptionTargetUnresolvedDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, location, warning string
	}{
		{name: "missing variable", location: "${missing}", warning: `option query still contains variable "missing"`},
		{name: "cyclic dependency", location: "${choice}", warning: "cyclic dependency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				http.Error(w, "unexpected request", http.StatusBadRequest)
			}))
			t.Cleanup(ts.Close)
			ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, true)
			db := preparationFixture(false, map[string]interface{}{
				"name": "choice", "type": "query", "current": map[string]interface{}{"value": "$__all"},
				"query":      map[string]interface{}{"rawSql": "SELECT option", "location": tc.location},
				"datasource": map[string]interface{}{"uid": "bigquery-uid"},
			}, "SELECT ${choice:sqlstring}")
			prepared, err := prepareDashboardQuery(ctx, db, "SELECT ${choice:sqlstring}", nil,
				datasourceInfo{UID: "bigquery-uid", Type: "grafana-bigquery-datasource"}, nil, "", "", "", "", nil)
			require.NoError(t, err)
			assert.Contains(t, strings.Join(prepared.Warnings, " "), tc.warning)
			assert.Zero(t, calls, "unresolved target fields must prevent option query submission")
		})
	}
}

func TestDashboardCustomAllDatasource(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, kind := range []string{"custom", "datasource"} {
			for _, override := range []string{"", "override-uid"} {
				t.Run(fmt.Sprintf("v2=%t/kind=%s/override=%s", v2, kind, override), func(t *testing.T) {
					wantUID := "postgres-uid"
					variables := map[string]string{}
					if override != "" {
						wantUID = override
						variables["choice"] = override
					}
					var panelCalls int
					ts := httptest.NewServer(withFrontendSettings(wantUID, func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if r.URL.Path == "/api/datasources/uid/"+wantUID {
							_ = json.NewEncoder(w).Encode(map[string]interface{}{"uid": wantUID, "type": "postgres"})
							return
						}
						if r.URL.Path != "/api/ds/query" {
							http.NotFound(w, r)
							return
						}
						panelCalls++
						var payload map[string]interface{}
						require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
						target := safeArray(payload, "queries")[0].(map[string]interface{})
						assert.Equal(t, wantUID, safeObject(target, "datasource")["uid"])
						assert.Equal(t, "postgres", safeObject(target, "datasource")["type"], "the resolved datasource type wins")
						frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []int64{1}))}
						_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
					}))
					t.Cleanup(ts.Close)
					db := preparationFixture(v2, map[string]interface{}{
						"name": "choice", "type": kind, "current": map[string]interface{}{"value": "$__all"}, "allValue": "postgres-uid",
					}, "SELECT 1")
					if v2 {
						if kind == "datasource" {
							safeArray(db, "variables")[0].(map[string]interface{})["kind"] = "DatasourceVariable"
						}
						panel := collectAllPanelsV2(db)[0]
						pq := safeArray(safeObject(safeObject(panel, "data"), "spec"), "queries")[0].(map[string]interface{})
						query := safeObject(safeObject(pq, "spec"), "query")
						query["datasource"] = map[string]interface{}{"name": "$choice"}
						query["group"] = "loki"
					} else {
						collectAllPanels(db)[0]["datasource"] = map[string]interface{}{"uid": "$choice", "type": "loki"}
					}
					ctx := enforceTestCtx(ts, false)
					inspected, err := inspectPreparationFixture(ctx, db, v2, DashboardPanelQueriesParams{Variables: variables})
					require.NoError(t, err)
					require.Len(t, inspected, 1)
					require.Empty(t, inspected[0].Warnings)
					assert.Equal(t, wantUID, inspected[0].Datasource.UID)
					assert.Equal(t, "postgres", inspected[0].Datasource.Type)
					result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, IsV2: v2, PanelID: 1, Variables: variables})
					require.NoError(t, err)
					assert.Equal(t, inspected[0].Datasource.UID, result.DatasourceUID)
					assert.Equal(t, inspected[0].ProcessedQuery, result.Query)
					assert.Equal(t, 1, panelCalls)
				})
			}
		}
	}
}

func TestDashboardDatasourceOverrideSkipsUnusedAll(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, usage := range []string{"datasource only", "expression", "target field"} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, usage), func(t *testing.T) {
				panelCalls, optionCalls := 0, 0
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/datasources/uid/override-uid":
						_, _ = w.Write([]byte(`{"uid":"override-uid","type":"postgres"}`))
					case "/api/ds/query":
						panelCalls++
						var payload map[string]interface{}
						require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
						target := safeArray(payload, "queries")[0].(map[string]interface{})
						assert.Equal(t, "override-uid", safeObject(target, "datasource")["uid"])
						assert.Equal(t, "SELECT 1", target["rawSql"])
						frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []int64{1}))}
						_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
					default:
						optionCalls++
						http.Error(w, "original datasource is inaccessible", http.StatusForbidden)
					}
				}))
				t.Cleanup(ts.Close)
				query := "SELECT 1"
				if usage == "expression" {
					query = "SELECT ${choice:sqlstring}"
				}
				db := preparationFixture(v2, map[string]interface{}{
					"name": "choice", "type": "query", "current": map[string]interface{}{"value": "$__all"},
					"query": "SELECT datasource_uid", "datasource": map[string]interface{}{"uid": "inaccessible-options"},
				}, query)
				target := preparationPanelTarget(db, v2)
				if usage == "target field" {
					target["location"] = "${choice}"
				}
				if v2 {
					panel := collectAllPanelsV2(db)[0]
					pq := safeArray(safeObject(safeObject(panel, "data"), "spec"), "queries")[0].(map[string]interface{})
					safeObject(safeObject(pq, "spec"), "query")["datasource"] = map[string]interface{}{"name": "$choice"}
				} else {
					target["datasource"] = map[string]interface{}{"uid": "$choice", "type": "postgres"}
				}
				ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, false)
				result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
					DB: db, IsV2: v2, PanelID: 1, DsUID: "override-uid", DsType: "postgres",
				})
				if usage == "datasource only" {
					require.NoError(t, err)
					assert.Equal(t, "override-uid", result.DatasourceUID)
					assert.Equal(t, 1, panelCalls)
				} else {
					require.ErrorContains(t, err, "variable option queries are disabled")
					assert.Zero(t, panelCalls, "overriding the datasource must not skip actual query dependencies")
				}
				assert.Zero(t, optionCalls)
				if !v2 {
					assert.Equal(t, "$choice", safeObject(target, "datasource")["uid"], "the saved target is not changed")
				}
			})
		}
	}
}

func TestDashboardExpressionTargetAllPreparationParity(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, allowQueries := range []bool{false, true} {
			t.Run(fmt.Sprintf("v2=%t/allowQueries=%t", v2, allowQueries), func(t *testing.T) {
				optionCalls, panelCalls := 0, 0
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/api/datasources/uid/postgres-uid" {
						_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"postgres"}`))
						return
					}
					require.Equal(t, "/api/ds/query", r.URL.Path)
					var payload map[string]interface{}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					target := safeArray(payload, "queries")[0].(map[string]interface{})
					if target["rawSql"] == "SELECT options" {
						optionCalls++
					} else {
						panelCalls++
						assert.Equal(t, "SELECT 1", target["rawSql"])
						assert.Equal(t, "us-east1", target["location"])
						assert.Equal(t, []interface{}{"us-east1"}, safeObject(target, "extra")["nested"])
					}
					frames := data.Frames{data.NewFrame("", data.NewField("__value", nil, []string{"us-east1"}))}
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
				}))
				t.Cleanup(ts.Close)
				db := preparationFixture(v2, map[string]interface{}{
					"name": "choice", "type": "query", "current": map[string]interface{}{"value": "$__all"},
					"query": "SELECT options", "datasource": map[string]interface{}{"uid": "postgres-uid"},
				}, "SELECT 1")
				target := preparationPanelTarget(db, v2)
				target["location"] = "${choice}"
				target["extra"] = map[string]interface{}{"nested": []interface{}{"${choice}"}}
				ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, allowQueries)
				raw, err := inspectPreparationFixture(ctx, db, v2, DashboardPanelQueriesParams{})
				require.NoError(t, err)
				require.Len(t, raw, 1)
				encoded, err := json.Marshal(raw[0])
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), `"target"`, "expression targets remain internal during raw inspection")
				assert.Zero(t, optionCalls)
				inspected, err := inspectPreparationFixture(ctx, db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
				require.NoError(t, err)
				require.Len(t, inspected, 1)
				result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, IsV2: v2, PanelID: 1})
				if allowQueries {
					require.NoError(t, err)
					assert.Empty(t, inspected[0].Warnings)
					assert.Equal(t, result.Query, inspected[0].ProcessedQuery)
					assert.Equal(t, 2, optionCalls, "inspection and execution both resolve the target-only All")
					assert.Equal(t, 1, panelCalls)
				} else {
					require.ErrorContains(t, err, "variable option queries are disabled")
					assert.Contains(t, strings.Join(inspected[0].Warnings, " "), "variable option queries are disabled")
					assert.Empty(t, inspected[0].ProcessedQuery)
					assert.Nil(t, inspected[0].ProcessedTarget)
					assert.Zero(t, optionCalls)
					assert.Zero(t, panelCalls)
				}
				assert.Nil(t, inspected[0].Target)
				assert.Nil(t, inspected[0].ProcessedTarget)
				assert.Equal(t, "${choice}", target["location"])
				assert.Equal(t, []interface{}{"${choice}"}, safeObject(target, "extra")["nested"])
			})
		}
	}
}

func TestDashboardExpressionTargetRequiredVariables(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("v2=%t", v2), func(t *testing.T) {
			db := preparationFixture(v2, map[string]interface{}{
				"name": "choice", "type": "custom", "current": map[string]interface{}{"value": "selected"},
			}, "SELECT ${choice:sqlstring}")
			for name, value := range map[string]string{"project": "test-project", "dataset": "test-dataset", "region": "EU"} {
				variable := map[string]interface{}{"name": name, "type": "custom", "current": map[string]interface{}{"value": value}}
				if v2 {
					db["variables"] = append(safeArray(db, "variables"), map[string]interface{}{"kind": "CustomVariable", "spec": variable})
				} else {
					templating := safeObject(db, "templating")
					templating["list"] = append(safeArray(templating, "list"), variable)
				}
			}
			queryTarget := preparationPanelTarget(db, v2)
			queryTarget["project"] = "${project}"
			queryTarget["dataset"] = "${dataset}"
			queryTarget["location"] = "${region}"
			queryTarget["extra"] = map[string]interface{}{"nested": []interface{}{"${region}", "${choice}"}}
			inspected, err := inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{
				Variables: map[string]string{"region": "US"},
			})
			require.NoError(t, err)
			require.Len(t, inspected, 1)
			require.Empty(t, inspected[0].Warnings)
			assert.Equal(t, "SELECT 'selected'", inspected[0].ProcessedQuery)
			dependencies := map[string]string{}
			for _, variable := range inspected[0].RequiredVariables {
				dependencies[variable.Name] = variable.CurrentValue
			}
			assert.Equal(t, map[string]string{"choice": "selected", "project": "test-project", "dataset": "test-dataset", "region": "US"}, dependencies)
			assert.Len(t, inspected[0].RequiredVariables, 4, "references shared by expression and target are deduplicated")
		})
	}
}
