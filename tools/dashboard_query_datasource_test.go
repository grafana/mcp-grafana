//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardOptionDatasourceUIDsRemainLiteral(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, reference := range []string{"literal-$region", "$selector", "${selector:raw}", "[[selector]]"} {
			for _, region := range []string{"missing", "saved", "all"} {
				t.Run(fmt.Sprintf("v2=%t/%s/%s", v2, reference, region), func(t *testing.T) {
					optionCalls := 0
					ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if r.URL.Path == "/api/datasources/uid/literal-$region" {
							_ = json.NewEncoder(w).Encode(map[string]interface{}{"uid": "literal-$region", "type": "postgres"})
							return
						}
						if !assert.Equal(t, "/api/ds/query", r.URL.Path) {
							http.NotFound(w, r)
							return
						}
						var payload map[string]interface{}
						if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload)) {
							return
						}
						target := safeArray(payload, "queries")[0].(map[string]interface{})
						assert.Equal(t, "literal-$region", safeString(safeObject(target, "datasource"), "uid"))
						assert.Equal(t, "SELECT 'east' AS __value", target["rawSql"])
						optionCalls++
						frames := data.Frames{data.NewFrame("", data.NewField("__value", nil, []string{"east"}))}
						_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
					}))
					t.Cleanup(ts.Close)
					db := preparationFixture(v2, map[string]interface{}{
						"name": "choice", "type": "query", "current": map[string]interface{}{"value": "$__all"},
						"query": "SELECT 'east' AS __value", "datasource": map[string]interface{}{"uid": reference},
					}, "SELECT ${choice:sqlstring}")
					if v2 {
						variable := safeObject(safeArray(db, "variables")[0].(map[string]interface{}), "spec")
						safeObject(variable, "query")["datasource"] = map[string]interface{}{"name": reference}
					}
					appendCustomAllFixtureVariable(db, v2, map[string]interface{}{
						"name": "selector", "type": "custom", "current": map[string]interface{}{"value": "literal-$region"},
					})
					if region != "missing" {
						variable := map[string]interface{}{"name": "region", "type": "custom", "current": map[string]interface{}{"value": "prod"}}
						if region == "all" {
							variable = map[string]interface{}{"name": "region", "type": "query", "current": map[string]interface{}{"value": "$__all"}, "query": "SELECT unrelated", "datasource": map[string]interface{}{"uid": "unused"}}
						}
						appendCustomAllFixtureVariable(db, v2, variable)
					}
					ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, true)
					queries, err := inspectPreparationFixture(ctx, db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
					require.NoError(t, err)
					require.Len(t, queries, 1)
					require.Empty(t, queries[0].Warnings)
					assert.Equal(t, "SELECT 'east'", queries[0].ProcessedQuery)
					assert.Equal(t, 1, optionCalls, "unrelated All options must not run")
				})
			}
		}
	}
}

func TestDashboardDatasourceUIDValuesRemainLiteral(t *testing.T) {
	// Grafana before v12 permitted nonstandard UIDs through provisioning and
	// its REST API. A dollar sign in a selected UID is data, not a template.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/datasources/uid/literal-$region", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"uid": "literal-$region", "type": "postgres"})
	}))
	t.Cleanup(ts.Close)
	ctx := enforceTestCtx(ts, false)
	for _, reference := range []string{"${ds}", "$ds", "[[ds:raw]]"} {
		for range 20 {
			resolved, err := resolvePanelDatasource(ctx, datasourceInfo{UID: reference, Type: "postgres"},
				map[string]string{"ds": "literal-$region", "region": "prod"}, "", "")
			require.NoError(t, err)
			assert.Equal(t, "literal-$region", resolved.UID)
		}
	}
}

func TestDashboardSavedAndOverriddenUIDsSkipUnusedAll(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, override := range []bool{false, true} {
			t.Run(fmt.Sprintf("v2=%t/override=%t", v2, override), func(t *testing.T) {
				calls := 0
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					assert.Equal(t, "/api/datasources/uid/literal-$region", r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"uid": "literal-$region", "type": "postgres"})
				}))
				t.Cleanup(ts.Close)
				db := preparationFixture(v2, map[string]interface{}{
					"name": "region", "type": "query", "current": map[string]interface{}{"value": "$__all"},
					"query": "SELECT region", "datasource": map[string]interface{}{"uid": "postgres-uid"},
				}, "SELECT 1")
				if v2 {
					db = templatingV1FromV2(db)
				}
				source := datasourceInfo{UID: "literal-$region", Type: "postgres"}
				overrideUID := ""
				wantCalls := 0
				if override {
					source.UID = "$missing"
					overrideUID = "literal-$region"
					wantCalls = 1
				}
				prepared, err := prepareDashboardQuery(enforceTestCtx(ts, false), db, "SELECT 1", nil, source,
					nil, "", "", overrideUID, "postgres", nil)
				require.NoError(t, err)
				require.Empty(t, prepared.Warnings, "a literal UID must not resolve the unrelated region All variable")
				assert.Equal(t, "literal-$region", prepared.Datasource.UID)
				assert.Equal(t, wantCalls, calls, "only an explicit override requires authoritative type lookup")
			})
		}
	}
}

func TestDashboardLiteralSavedUIDWithoutVariableDefinition(t *testing.T) {
	resolved, err := resolvePanelDatasource(t.Context(), datasourceInfo{UID: "literal-$region", Type: "postgres"}, nil, "", "")
	require.NoError(t, err)
	assert.Equal(t, "literal-$region", resolved.UID)
}

func TestDashboardMissingTargetTypeCannotBypassLokiEnforcement(t *testing.T) {
	lookups := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/datasources/uid/loki-uid", r.URL.Path, "preparation must look up the target instead of executing SQL")
		lookups++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"uid": "loki-uid", "type": "loki"})
	}))
	t.Cleanup(ts.Close)
	panel := map[string]interface{}{"datasource": map[string]interface{}{"uid": "loki-uid", "type": "postgres"}}
	target := map[string]interface{}{"expr": `{app="secret"}`, "datasource": map[string]interface{}{"uid": "loki-uid"}}
	source := extractPanelDatasource(panel, target)
	require.Equal(t, "postgres", source.Type, "raw inspection inherits a matching panel reference")
	prepared, err := prepareDashboardQuery(enforceTestCtx(ts, true), map[string]interface{}{}, `{app="secret"}`, target, source,
		nil, "", "", "", "", nil)
	require.NoError(t, err)
	assert.Equal(t, "loki", prepared.Datasource.Type, "execution must select the native Loki path where matchers are enforced")
	assert.Equal(t, 1, lookups)
}
