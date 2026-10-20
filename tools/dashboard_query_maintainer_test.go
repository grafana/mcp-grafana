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

func TestDashboardCaptureReferencesAreNotVariables(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, dsType := range []string{"prometheus", "loki"} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, dsType), func(t *testing.T) {
				query := `label_replace(up{job="$job"}, "dst", "$1", "src", "(.*)")`
				db := preparationFixture(v2, map[string]interface{}{
					"name": "job", "type": "custom", "current": map[string]interface{}{"value": "api"},
				}, query)
				setPreparationDatasource(db, v2, "metrics", dsType)
				queries, err := inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
				require.NoError(t, err)
				require.Len(t, queries, 1)
				require.Empty(t, queries[0].Warnings)
				require.Len(t, queries[0].RequiredVariables, 1)
				assert.Equal(t, "job", queries[0].RequiredVariables[0].Name)
				assert.Equal(t, `label_replace(up{job="api"}, "dst", "$1", "src", "(.*)")`, queries[0].ProcessedQuery)
			})
		}
	}
}

func TestDashboardUIDOnlyInheritsMatchingPanelType(t *testing.T) {
	for _, uid := range []string{"postgres-uid", "other-uid"} {
		t.Run(uid, func(t *testing.T) {
			panel := map[string]interface{}{
				"datasource": map[string]interface{}{"uid": "postgres-uid", "type": "postgres"},
				"targets": []interface{}{map[string]interface{}{
					"refId": "A", "rawSql": "SELECT 1", "datasource": map[string]interface{}{"uid": uid},
				}},
			}
			queries := extractPanelQueries(panel)
			require.Len(t, queries, 1)
			assert.Equal(t, uid, queries[0].Datasource.UID)
			if uid != "postgres-uid" {
				assert.Empty(t, queries[0].Datasource.Type, "a different UID must not inherit an unrelated type")
				return
			}
			assert.Equal(t, "postgres", queries[0].Datasource.Type)
			queries = prepareInspectedQueries(t.Context(), map[string]interface{}{}, DashboardPanelQueriesParams{Variables: map[string]string{}}, queries)
			require.Empty(t, queries[0].Warnings, "matching saved references should prepare without a datasource API client")
			assert.Equal(t, "SELECT 1", queries[0].ProcessedQuery)
		})
	}
}

func TestDashboardIndependentAllQueriesSurviveUnrelatedWarning(t *testing.T) {
	for _, dependent := range []bool{false, true} {
		t.Run(fmt.Sprint(dependent), func(t *testing.T) {
			calls := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/datasources/uid/postgres-uid" {
					_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"postgres"}`))
					return
				}
				require.Equal(t, "/api/ds/query", r.URL.Path)
				calls++
				frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []string{"east"}))}
				_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
			}))
			t.Cleanup(ts.Close)
			db := preparationFixture(false, map[string]interface{}{
				"name": "bad", "type": "custom", "current": map[string]interface{}{"value": "$__all"},
			}, "SELECT ${bad}, ${good:sqlstring}")
			query := "SELECT 'east'"
			if dependent {
				query = "SELECT ${bad}"
			}
			appendCustomAllFixtureVariable(db, false, map[string]interface{}{
				"name": "good", "type": "query", "current": map[string]interface{}{"value": "$__all"},
				"query": query, "datasource": map[string]interface{}{"uid": "postgres-uid"},
			})
			ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, true)
			prepared, err := prepareDashboardQuery(ctx, db, "SELECT ${bad}, ${good:sqlstring}", nil,
				datasourceInfo{UID: "postgres-uid", Type: "postgres"}, nil, "", "", "", "", nil)
			require.NoError(t, err)
			require.NotEmpty(t, prepared.Warnings, "the unresolved selection still blocks panel execution")
			if dependent {
				assert.Zero(t, calls, "an actual failed dependency must block option execution")
				assert.NotContains(t, prepared.Variables, "good")
			} else {
				assert.Equal(t, 1, calls)
				assert.Equal(t, []string{"east"}, prepared.Variables["good"])
				require.Len(t, prepared.Warnings, 1, "do not add a misleading dependency warning for good")
			}
		})
	}
}

func TestDashboardNullOptionDatasourceWarnsWithoutQuerying(t *testing.T) {
	db := preparationFixture(false, map[string]interface{}{
		"name": "choice", "type": "query", "current": map[string]interface{}{"value": "$__all"},
		"query": "SELECT 'east'", "datasource": nil,
	}, "SELECT ${choice:sqlstring}")
	ctx := context.WithValue(t.Context(), variableQueriesKey{}, true)
	queries, err := inspectPreparationFixture(ctx, db, false, DashboardPanelQueriesParams{Variables: map[string]string{}})
	require.NoError(t, err)
	require.Len(t, queries, 1)
	require.Len(t, queries[0].Warnings, 1)
	assert.Contains(t, queries[0].Warnings[0], "datasource UID")
	assert.Contains(t, queries[0].Warnings[0], "supply explicit values")
	assert.Empty(t, queries[0].ProcessedQuery)
}
