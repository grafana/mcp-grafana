//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
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

func TestTemplateVariableValuesDoNotRescanData(t *testing.T) {
	variables := templateVariableValues{
		"region": {"$zone"}, "zone": {"${region}"},
		"1": {"numeric"}, "with-dash": {"dash"},
	}
	for _, query := range []string{"${region:sqlstring}, ${zone:sqlstring}", "[[region:sqlstring]], [[zone:sqlstring]]"} {
		assert.Equal(t, "'$zone', '${region}'", substituteTemplateVariableValues(query, variables))
	}
	assert.Equal(t, "$zone ${region} $unknown", substituteTemplateVariableValues("$region $zone $unknown", variables))
	assert.Equal(t, "numeric dash", substituteTemplateVariableValues("${1} [[with-dash]]", variables))
}

func TestDashboardNumericVariableReferences(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, reference := range []string{"$1", "${1}", "${1:raw}", "[[1]]", "[[1:raw]]"} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, reference), func(t *testing.T) {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "/api/datasources/uid/postgres-uid", r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"postgres"}`))
				}))
				t.Cleanup(ts.Close)
				db := preparationFixture(v2, map[string]interface{}{
					"name": "1", "type": "custom", "current": map[string]interface{}{"value": "postgres-uid"},
				}, "SELECT 1")
				setPreparationDatasource(db, v2, reference, "loki")
				queries, err := inspectPreparationFixture(enforceTestCtx(ts, false), db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
				require.NoError(t, err)
				require.Len(t, queries, 1)
				require.Empty(t, queries[0].Warnings)
				assert.Equal(t, datasourceInfo{UID: "postgres-uid", Type: "postgres"}, queries[0].Datasource)
			})
		}
		for _, reference := range []string{"${1:sqlstring}", "[[1:sqlstring]]"} {
			t.Run(fmt.Sprintf("v2=%t/All/%s", v2, reference), func(t *testing.T) {
				db := preparationFixture(v2, map[string]interface{}{
					"name": "1", "type": "custom", "current": map[string]interface{}{"value": "$__all"},
					"options": []interface{}{map[string]interface{}{"value": "east"}, map[string]interface{}{"value": "west"}},
				}, "SELECT "+reference)
				queries, err := inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
				require.NoError(t, err)
				require.Len(t, queries, 1)
				require.Empty(t, queries[0].Warnings)
				assert.Equal(t, "SELECT 'east','west'", queries[0].ProcessedQuery)
			})
		}
	}
}

func TestDashboardMacroTextInVariableValues(t *testing.T) {
	const literal = "$__from ${__to} $__interval ${__range_s}"
	for _, v2 := range []bool{false, true} {
		for _, dsType := range []string{"postgres", "mysql", "mssql", "grafana-bigquery-datasource", "grafana-clickhouse-datasource"} {
			for _, selection := range []string{"saved", "SQL All", "custom All"} {
				t.Run(fmt.Sprintf("v2=%t/%s/%s", v2, dsType, selection), func(t *testing.T) {
					wantValue := "'" + literal + "'"
					variable := map[string]interface{}{"name": "choice", "type": "custom", "current": map[string]interface{}{"value": literal}}
					if selection == "SQL All" {
						variable["type"] = "query"
						variable["current"] = map[string]interface{}{"value": "$__all"}
						variable["query"] = "SELECT ${region:sqlstring} /* $__from */"
						variable["datasource"] = map[string]interface{}{"uid": "postgres-uid"}
					} else if selection == "custom All" {
						variable["current"] = map[string]interface{}{"value": "$__all"}
						variable["allValue"] = "'${__from}'"
						wantValue = "'1704067200000'"
					}
					want := "SELECT " + wantValue + ", 1704067200000 LIMIT 1"
					optionCalls, panelCalls := 0, 0
					ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if r.URL.Path == "/api/datasources/uid/postgres-uid" {
							_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"postgres"}`))
							return
						}
						if r.URL.Path == "/api/datasources/uid/panel-uid" {
							_ = json.NewEncoder(w).Encode(map[string]interface{}{"uid": "panel-uid", "type": dsType})
							return
						}
						require.Equal(t, "/api/ds/query", r.URL.Path)
						var payload map[string]interface{}
						require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
						target := safeArray(payload, "queries")[0].(map[string]interface{})
						if safeObject(target, "datasource")["uid"] == "postgres-uid" {
							optionCalls++
							assert.Equal(t, "SELECT '"+literal+"' /* 1704067200000 */", target["rawSql"])
						} else {
							panelCalls++
							assert.Equal(t, want, target["rawSql"])
						}
						frames := data.Frames{data.NewFrame("", data.NewField("__value", nil, []string{literal}))}
						_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
					}))
					t.Cleanup(ts.Close)
					db := preparationFixture(v2, variable, "SELECT ${choice:sqlstring}, $__from LIMIT 1")
					region := map[string]interface{}{"name": "region", "type": "custom", "current": map[string]interface{}{"value": literal}}
					if v2 {
						db["variables"] = append(safeArray(db, "variables"), map[string]interface{}{"kind": "CustomVariable", "spec": region})
					} else {
						variables := safeObject(db, "templating")
						variables["list"] = append(safeArray(variables, "list"), region)
					}
					setPreparationDatasource(db, v2, "panel-uid", dsType)
					ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, true)
					args := DashboardPanelQueriesParams{Start: "1704067200000", End: "1704070800000"}
					inspected, err := inspectPreparationFixture(ctx, db, v2, args)
					require.NoError(t, err)
					require.Empty(t, inspected[0].Warnings)
					assert.Equal(t, want, inspected[0].ProcessedQuery)
					result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, IsV2: v2, PanelID: 1, Start: args.Start, End: args.End})
					require.NoError(t, err)
					assert.Equal(t, want, result.Query)
					assert.Equal(t, 1, panelCalls)
					wantOptions := 0
					if selection == "SQL All" {
						wantOptions = 2
					}
					assert.Equal(t, wantOptions, optionCalls)
				})
			}
		}
	}
}

func TestDashboardOptionValuesRemainLiteral(t *testing.T) {
	var sent []string
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
		sent = append(sent, safeString(target, "rawSql"))
		frames := data.Frames{data.NewFrame("", data.NewField("__value", nil, []string{"selected"}))}
		_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
	}))
	defer ts.Close()
	ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, true)
	db := preparationFixture(false, map[string]interface{}{
		"name": "choice", "type": "query", "current": map[string]interface{}{"value": "$__all"},
		"query":      "SELECT ${region:sqlstring}, ${zone:sqlstring}",
		"datasource": map[string]interface{}{"uid": "postgres-uid"},
	}, "SELECT ${choice:sqlstring}")
	vars := safeObject(db, "templating")
	vars["list"] = append(safeArray(vars, "list"),
		map[string]interface{}{"name": "region", "type": "custom", "current": map[string]interface{}{"value": "$zone"}},
		map[string]interface{}{"name": "zone", "type": "custom", "current": map[string]interface{}{"value": "$region"}})
	for range 2 {
		prepared, err := prepareDashboardQuery(ctx, db, "SELECT ${choice:sqlstring}", nil, datasourceInfo{UID: "postgres-uid", Type: "postgres"}, nil, "1704067200000", "1704070800000", "", "", nil)
		require.NoError(t, err)
		require.Empty(t, prepared.Warnings)
	}
	counts := map[string]int{}
	for _, query := range sent {
		counts[query]++
	}
	t.Logf("Option SQL counts: %#v", counts)
	assert.Equal(t, 2, counts["SELECT '$zone', '$region'"], "ordinary saved values must remain data")
}

func TestDashboardSettingsFailureCachedWithinInspection(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/frontend/settings", r.URL.Path)
		calls++
		http.Error(w, "settings unavailable", http.StatusServiceUnavailable)
	}))
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	db := preparationFixture(false, map[string]interface{}{"name": "choice", "type": "custom", "current": map[string]interface{}{"value": "saved"}}, "")
	var queries []panelQuery
	for range 4 {
		queries = append(queries, panelQuery{Datasource: datasourceInfo{UID: "cloudwatch", Type: "cloudwatch"}, Target: map[string]interface{}{"metricName": "CPUUtilization", "alias": "$choice", "label": "chosen"}})
	}
	prepared := prepareInspectedQueries(ctx, db, DashboardPanelQueriesParams{Variables: map[string]string{}}, queries)
	for _, p := range prepared {
		require.Empty(t, p.Warnings)
	}
	t.Logf("Settings requests for one inspection of four panels: %d", calls)
	assert.Equal(t, 1, calls, "optional settings failure should be reused within one tool call")
	prepareInspectedQueries(ctx, db, DashboardPanelQueriesParams{Variables: map[string]string{}}, queries)
	assert.Equal(t, 2, calls, "later calls must retry failed settings")
}

func TestDashboardSettingsFailureCachedWithinExecution(t *testing.T) {
	db := preparationFixture(false, map[string]interface{}{"name": "choice", "type": "custom", "current": map[string]interface{}{"value": "saved"}}, "")
	setPreparationDatasource(db, false, "cloudwatch", "cloudwatch")
	target := preparationPanelTarget(db, false)
	delete(target, "rawSql")
	target["metricName"], target["alias"], target["label"] = "CPUUtilization", "$choice", "chosen"
	first := collectAllPanels(db)[0]
	for _, id := range []float64{2, 3, 4} {
		panel := maps.Clone(first)
		panel["id"] = id
		db["panels"] = append(safeArray(db, "panels"), panel)
	}
	settingsCalls, panelCalls := 0, 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/dashboards/uid/test":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"dashboard": db, "meta": map[string]interface{}{}})
		case "/api/frontend/settings":
			settingsCalls++
			http.Error(w, "settings unavailable", http.StatusServiceUnavailable)
		case "/api/ds/query":
			panelCalls++
			_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	ctx := enforceTestCtx(ts, false)
	for call := 1; call <= 2; call++ {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: "test", PanelIDs: []int{1, 2, 3, 4}})
		require.NoError(t, err)
		require.Empty(t, result.Errors)
		require.Len(t, result.Results, 4)
		assert.Equal(t, call, settingsCalls)
		assert.Equal(t, 4*call, panelCalls)
	}
}

func TestDashboardMixedPanelPreparationParity(t *testing.T) {
	var submitted string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/datasources/uid/postgres-uid" {
			_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"postgres"}`))
			return
		}
		require.Equal(t, "/api/ds/query", r.URL.Path)
		var payload map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		submitted = safeString(safeArray(payload, "queries")[0].(map[string]interface{}), "rawSql")
		frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []int64{1}))}
		_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
	}))
	defer ts.Close()
	ctx := enforceTestCtx(ts, false)
	db := preparationFixture(false, map[string]interface{}{"name": "unused", "type": "custom"}, "SELECT $__from")
	panel := collectAllPanels(db)[0]
	panel["datasource"] = map[string]interface{}{"uid": "-- Mixed --", "type": "datasource"}
	preparationPanelTarget(db, false)["datasource"] = map[string]interface{}{"uid": "postgres-uid"}
	args := DashboardPanelQueriesParams{Variables: map[string]string{}, Start: "1704067200000", End: "1704070800000"}
	inspected, err := inspectPreparationFixture(ctx, db, false, args)
	require.NoError(t, err)
	require.Len(t, inspected, 1)
	require.Empty(t, inspected[0].Warnings)
	result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, PanelID: 1, Start: args.Start, End: args.End})
	require.NoError(t, err)
	t.Logf("Inspection: type=%s SQL=%q, execution: type=%s SQL=%q, submitted=%q", inspected[0].Datasource.Type, inspected[0].ProcessedQuery, result.DatasourceType, result.Query, submitted)
	assert.Equal(t, result.DatasourceType, inspected[0].Datasource.Type)
	assert.Equal(t, submitted, inspected[0].ProcessedQuery)
}

func TestDashboardRawDatasourceTypeFallback(t *testing.T) {
	for _, targetUID := range []string{"same", "other"} {
		panel := map[string]interface{}{
			"id": 1, "datasource": map[string]interface{}{"uid": "same", "type": "prometheus"},
			"targets": []interface{}{map[string]interface{}{"expr": "up", "datasource": map[string]interface{}{"uid": targetUID}}},
		}
		queries := extractPanelQueries(panel, nil, nil)
		require.Len(t, queries, 1)
		wantType := ""
		if targetUID == "same" {
			wantType = "prometheus"
		}
		assert.Equal(t, datasourceInfo{UID: targetUID, Type: wantType}, queries[0].Datasource)
		info, err := extractPanelInfo(panel, 0)
		require.NoError(t, err)
		assert.Equal(t, wantType, info.DatasourceType)
	}
}

func TestDashboardDollarQuotedOptionSQL(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, query := range []string{"SELECT $$option$$ AS __value", "SELECT $tag$option$tag$ AS __value", "SELECT $tag$${region}$tag$ AS __value", "SELECT '$100' AS __value", "SELECT '$missing' AS __value", "SELECT '${missing}' AS __value"} {
			for _, object := range []bool{false, true} {
				t.Run(fmt.Sprintf("v2=%t/object=%t/%s", v2, object, query), func(t *testing.T) {
					calls := 0
					ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if r.URL.Path == "/api/datasources/uid/postgres-uid" {
							_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"postgres"}`))
							return
						}
						require.Equal(t, "/api/ds/query", r.URL.Path)
						calls++
						var payload map[string]interface{}
						require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
						target := safeArray(payload, "queries")[0].(map[string]interface{})
						assert.Equal(t, strings.ReplaceAll(query, "${region}", "east"), target["rawSql"])
						frames := data.Frames{data.NewFrame("", data.NewField("__value", nil, []string{"option"}))}
						_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
					}))
					t.Cleanup(ts.Close)
					var option interface{} = query
					if object {
						option = map[string]interface{}{"rawSql": query}
					}
					db := preparationFixture(v2, map[string]interface{}{
						"name": "choice", "type": "query", "current": map[string]interface{}{"value": "$__all"},
						"query": option, "datasource": map[string]interface{}{"uid": "postgres-uid"},
					}, "SELECT ${choice:sqlstring}")
					if v2 && object {
						spec := safeObject(safeArray(db, "variables")[0].(map[string]interface{}), "spec")
						safeObject(spec, "query")["spec"] = option
					}
					ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, true)
					inspected, err := inspectPreparationFixture(ctx, db, v2, DashboardPanelQueriesParams{Variables: map[string]string{"region": "east"}})
					require.NoError(t, err)
					require.Empty(t, inspected[0].Warnings)
					assert.Equal(t, "SELECT 'option'", inspected[0].ProcessedQuery)
					assert.Equal(t, 1, calls)
				})
			}
		}
	}
}

func TestDashboardEncodedAllOverride(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, override := range []string{`["$__all"]`, "'$__all'", "$__all"} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, override), func(t *testing.T) {
				db := preparationFixture(v2, map[string]interface{}{
					"name": "choice", "type": "custom", "multi": true,
					"current": map[string]interface{}{"value": "east"},
					"options": []interface{}{map[string]interface{}{"value": "east"}, map[string]interface{}{"value": "west"}},
				}, "SELECT ${choice:sqlstring}")
				variables := map[string]string{"choice": override}
				inspected, err := inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{Variables: variables})
				require.NoError(t, err)
				require.Empty(t, inspected[0].Warnings)
				assert.Equal(t, "SELECT 'east','west'", inspected[0].ProcessedQuery)
				if v2 {
					db = templatingV1FromV2(db)
				}
				prepared, err := prepareDashboardQuery(t.Context(), db, "SELECT ${choice:sqlstring}", nil, datasourceInfo{UID: "postgres-uid", Type: "postgres"}, variables, "", "", "", "", nil)
				require.NoError(t, err)
				require.Empty(t, prepared.Warnings)
				assert.Equal(t, inspected[0].ProcessedQuery, prepared.Query)
			})
		}
	}
}
