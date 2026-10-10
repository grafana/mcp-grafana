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

func preparationFixture(v2 bool, variable map[string]interface{}, query string) map[string]interface{} {
	ds := map[string]interface{}{"uid": "postgres-uid", "type": "grafana-postgresql-datasource"}
	if !v2 {
		return map[string]interface{}{
			"templating": map[string]interface{}{"list": []interface{}{variable}},
			"panels": []interface{}{map[string]interface{}{
				"id": float64(1), "title": "Preparation", "datasource": ds,
				"targets": []interface{}{map[string]interface{}{"refId": "A", "rawSql": query}},
			}},
		}
	}
	variableSpec := map[string]interface{}{}
	for k, v := range variable {
		variableSpec[k] = v
	}
	variableSpec["query"] = map[string]interface{}{
		"kind": "DataQuery", "group": ds["type"], "datasource": map[string]interface{}{"name": ds["uid"]},
		"spec": map[string]interface{}{"rawSql": variable["query"]},
	}
	kind := "QueryVariable"
	if variable["type"] == "custom" {
		kind = "CustomVariable"
	}
	return map[string]interface{}{
		"variables": []interface{}{map[string]interface{}{"kind": kind, "spec": variableSpec}},
		"elements": map[string]interface{}{"panel": map[string]interface{}{"kind": "Panel", "spec": map[string]interface{}{
			"id": float64(1), "title": "Preparation",
			"data": map[string]interface{}{"spec": map[string]interface{}{"queries": []interface{}{
				map[string]interface{}{"spec": map[string]interface{}{"refId": "A", "query": map[string]interface{}{
					"group": ds["type"], "datasource": map[string]interface{}{"name": ds["uid"]},
					"spec": map[string]interface{}{"rawSql": query},
				}}},
			}}},
		}}},
	}
}

func inspectPreparationFixture(ctx context.Context, db map[string]interface{}, v2 bool, args DashboardPanelQueriesParams) ([]panelQuery, error) {
	if v2 {
		rawArgs := args
		rawArgs.Variables = nil
		queries, err := getPanelQueriesV2(db, rawArgs)
		if err != nil {
			return nil, err
		}
		return prepareInspectedQueries(ctx, templatingV1FromV2(db), args, queries), nil
	}
	queries := extractPanelQueries(collectAllPanels(db)[0], nil, nil)
	return prepareInspectedQueries(ctx, db, args, queries), nil
}

func TestDashboardQueryPreparationParity(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, tc := range []struct {
			name        string
			current     interface{}
			override    string
			allValue    string
			want        string
			optionCalls int
		}{
			{name: "multi value", current: []interface{}{"O'Reilly", "east,west"}, want: "'O''Reilly','east,west'"},
			{name: "quote inside value", current: "'quoted'", want: "'''quoted'''"},
			{name: "quoted list override", current: "old", override: "'O''Reilly', 'east,west'", want: "'O''Reilly','east,west'"},
			{name: "JSON list override", current: "old", override: `["O'Reilly","east,west"]`, want: "'O''Reilly','east,west'"},
			{name: "All from SQL", current: []interface{}{"$__all"}, want: "'O''Reilly','east,west'", optionCalls: 2},
			{name: "All override", current: "old", override: "$__all", want: "'O''Reilly','east,west'", optionCalls: 2},
			{name: "custom All", current: "$__all", allValue: "'custom'", want: "'custom'"},
		} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, tc.name), func(t *testing.T) {
				var queries []string
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/api/datasources/uid/postgres-uid" {
						_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"grafana-postgresql-datasource"}`))
						return
					}
					require.Equal(t, "/api/ds/query", r.URL.Path)
					var payload map[string]interface{}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					query := safeArray(payload, "queries")[0].(map[string]interface{})
					queries = append(queries, safeString(query, "rawSql"))
					assert.Equal(t, "1704067200000", payload["from"])
					assert.Equal(t, "1704070800000", payload["to"])
					first, second := "O'Reilly", "east,west"
					frames := data.Frames{data.NewFrame("", data.NewField("__text", nil, []string{"First", "Second", "Null"}), data.NewField("__value", nil, []*string{&first, &second, nil}))}
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": backend.DataResponse{Frames: frames}}})
				}))
				t.Cleanup(ts.Close)
				ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, true)
				variable := map[string]interface{}{
					"name": "choice", "type": "query", "multi": true, "includeAll": true, "allValue": tc.allValue,
					"current": map[string]interface{}{"value": tc.current},
					"query":   "SELECT option /* $__from $__to */", "datasource": map[string]interface{}{"uid": "postgres-uid"},
				}
				db := preparationFixture(v2, variable, "SELECT ${choice:sqlstring} /* $__from $__to $__range_s $__interval */ WHERE $__timeFilter(ts)")
				overrides := map[string]string{}
				if tc.override != "" {
					overrides["choice"] = tc.override
				}
				args := DashboardPanelQueriesParams{Variables: overrides, Start: "2024-01-01T00:00:00Z", End: "2024-01-01T01:00:00Z"}
				inspected, err := inspectPreparationFixture(ctx, db, v2, args)
				require.NoError(t, err)
				require.Len(t, inspected, 1)
				require.Empty(t, inspected[0].Warnings)
				result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, IsV2: v2, PanelID: 1, Variables: overrides, Start: args.Start, End: args.End})
				require.NoError(t, err)
				want := "SELECT " + tc.want + " /* 1704067200000 1704070800000 3600 36s */ WHERE $__timeFilter(ts)"
				assert.Equal(t, want, inspected[0].ProcessedQuery)
				assert.Equal(t, want, result.Query)
				assert.Equal(t, want, queries[len(queries)-1], "actual request must equal inspection")
				assert.Equal(t, inspected[0].Datasource.UID, result.DatasourceUID)
				assert.Equal(t, inspected[0].Datasource.Type, result.DatasourceType)
				assert.Len(t, queries, tc.optionCalls+1)
			})
		}
	}
}

func TestDashboardVariableOptionFailures(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(fmt.Sprintf("queriesAllowed=%t", allow), func(t *testing.T) {
			calls := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/api/datasources/uid/postgres-uid" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"grafana-postgresql-datasource"}`))
					return
				}
				http.Error(w, "option query failed", http.StatusBadRequest)
			}))
			t.Cleanup(ts.Close)
			ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, allow)
			db := preparationFixture(false, map[string]interface{}{
				"name": "choice", "type": "query", "current": map[string]interface{}{"value": "$__all"},
				"query": "SELECT bad", "datasource": map[string]interface{}{"uid": "postgres-uid"},
			}, "SELECT ${choice:sqlstring}")
			inspected, err := inspectPreparationFixture(ctx, db, false, DashboardPanelQueriesParams{Variables: map[string]string{}})
			require.NoError(t, err)
			require.NotEmpty(t, inspected[0].Warnings)
			assert.Empty(t, inspected[0].ProcessedQuery)
			assert.Contains(t, strings.Join(inspected[0].Warnings, " "), "supply explicit values")
			_, err = runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, PanelID: 1})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Could not resolve All")
			if !allow {
				assert.Zero(t, calls, "disabled option queries must perform no network requests")
			}
		})
	}
}

func TestDashboardPreparationResolvesDatasourceType(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/datasources/uid/postgres-uid", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"grafana-postgresql-datasource"}`))
	}))
	t.Cleanup(ts.Close)
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprint(v2), func(t *testing.T) {
			db := preparationFixture(v2, map[string]interface{}{
				"name": "choice", "type": "custom", "current": map[string]interface{}{"value": "postgres-uid"},
			}, "SELECT 1")
			if v2 {
				panel := collectAllPanelsV2(db)[0]
				pq := safeArray(safeObject(safeObject(panel, "data"), "spec"), "queries")[0].(map[string]interface{})
				query := safeObject(safeObject(pq, "spec"), "query")
				query["datasource"] = map[string]interface{}{"name": "$choice"}
				query["group"] = "loki"
			} else {
				collectAllPanels(db)[0]["datasource"] = map[string]interface{}{"uid": "$choice", "type": "loki"}
			}
			queries, err := inspectPreparationFixture(enforceTestCtx(ts, false), db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
			require.NoError(t, err)
			require.Len(t, queries, 1)
			assert.Equal(t, "postgres-uid", queries[0].Datasource.UID)
			assert.Equal(t, "grafana-postgresql-datasource", queries[0].Datasource.Type)
		})
	}
}

func TestInspectDashboardSQLString(t *testing.T) {
	db := postgresSQLStringPanelDashboard([]interface{}{"O'Reilly", "east,west"}, true)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/dashboards/uid/test" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"dashboard": db, "meta": map[string]interface{}{}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	queries, err := GetDashboardPanelQueriesTool(enforceTestCtx(ts, false), DashboardPanelQueriesParams{
		UID: "test", Variables: map[string]string{},
	})
	require.NoError(t, err)
	require.Len(t, queries, 1)
	assert.Equal(t, "SELECT * FROM orders WHERE order_intent_id IN ('O''Reilly','east,west')", queries[0].ProcessedQuery)
}

func TestDashboardAllOptions(t *testing.T) {
	for _, tc := range []struct {
		name, kind, optionQuery, regex, warning string
	}{
		{name: "saved custom options", kind: "custom"},
		{name: "cyclic dependency", kind: "query", optionQuery: "SELECT ${choice:sqlstring}", warning: "cyclic dependency"},
		{name: "unsupported regex", kind: "query", optionQuery: "SELECT 1", regex: "/east/", warning: "regex filtering is not supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			variable := map[string]interface{}{
				"name": "choice", "type": tc.kind, "query": tc.optionQuery, "regex": tc.regex,
				"current": map[string]interface{}{"value": "$__all"},
				"options": []interface{}{
					map[string]interface{}{"value": "$__all"},
					map[string]interface{}{"value": "east"},
					map[string]interface{}{"value": "west"},
				},
			}
			ctx := context.WithValue(t.Context(), variableQueriesKey{}, true)
			db := preparationFixture(false, variable, "SELECT ${choice:sqlstring}")
			prepared, err := prepareDashboardQuery(ctx, db, "SELECT ${choice:sqlstring}", datasourceInfo{UID: "postgres-uid", Type: "postgres"}, nil, "", "", "", "")
			require.NoError(t, err)
			if tc.warning != "" {
				assert.Contains(t, strings.Join(prepared.Warnings, " "), tc.warning)
			} else {
				assert.Empty(t, prepared.Warnings)
				assert.Equal(t, "SELECT 'east','west'", prepared.Query)
			}
		})
	}
}
