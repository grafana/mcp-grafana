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
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise saved selections through the public inspector instead of retaining
// the old extraction-only interpolation implementation for helper tests.
func inspectLegacyPanelForTest(t *testing.T, panel map[string]interface{}, variables map[string]VariableInfo) []panelQuery {
	t.Helper()
	definitions := []interface{}{}
	for name, variable := range variables {
		definitions = append(definitions, map[string]interface{}{
			"name": name, "type": "custom", "current": map[string]interface{}{"value": variable.CurrentValue},
			"options": []interface{}{map[string]interface{}{"value": variable.DefaultValue}},
		})
	}
	db := map[string]interface{}{"panels": []interface{}{panel}, "templating": map[string]interface{}{"list": definitions}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/dashboards/uid/test":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"dashboard": db})
		case strings.HasPrefix(r.URL.Path, "/api/datasources/uid/"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"uid": strings.TrimPrefix(r.URL.Path, "/api/datasources/uid/"), "type": safeString(safeObject(panel, "datasource"), "type"),
			})
		default:
			t.Errorf("unexpected inspection request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	queries, err := GetDashboardPanelQueriesTool(enforceTestCtx(ts, false), DashboardPanelQueriesParams{UID: "test", Variables: map[string]string{}})
	require.NoError(t, err)
	for _, query := range queries {
		require.Empty(t, query.Warnings)
	}
	return queries
}

func servePreparationDashboard(w http.ResponseWriter, r *http.Request, db map[string]interface{}, v2 bool) bool {
	if v2 && serveDashboardDiscovery(w, r, "v1beta1", "v2beta1") {
		return true
	}
	var response map[string]interface{}
	switch {
	case r.URL.Path == "/api/dashboards/uid/test":
		response = map[string]interface{}{"dashboard": db}
	case v2 && strings.Contains(r.URL.Path, "/v1beta1/"):
		response = map[string]interface{}{"status": map[string]interface{}{"conversion": map[string]interface{}{"storedVersion": "v2beta1"}}}
	case v2 && strings.Contains(r.URL.Path, "/v2beta1/"):
		response = map[string]interface{}{"apiVersion": "dashboard.grafana.app/v2beta1", "spec": db}
	default:
		return false
	}
	_ = json.NewEncoder(w).Encode(response)
	return true
}

func TestDashboardSQLInactiveExpressionAliases(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, tc := range []struct{ selected, unused string }{
			{"rawSql", "target"}, {"rawSql", "rawSQL"}, {"query", "rawSql"}, {"expr", "rawSQL"}, {"expression", "target"},
		} {
			t.Run(fmt.Sprintf("v2=%t/%s/%s", v2, tc.selected, tc.unused), func(t *testing.T) {
				db := preparationFixture(v2, map[string]interface{}{
					"name": "choice", "type": "custom", "current": map[string]interface{}{"value": "$__all"},
				}, "")
				target := preparationPanelTarget(db, v2)
				delete(target, "rawSql")
				target[tc.selected] = "SELECT 1"
				target[tc.unused] = "$choice"
				calls := 0
				ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if servePreparationDashboard(w, r, db, v2) {
						return
					}
					if !assert.Equal(t, "/api/ds/query", r.URL.Path) {
						http.NotFound(w, r)
						return
					}
					var payload map[string]interface{}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					sent := safeArray(payload, "queries")[0].(map[string]interface{})
					assert.Equal(t, "SELECT 1", sent["rawSql"])
					var query sqlutil.Query
					// The plugin SDK uses a numeric format enum. PostgreSQL's
					// format is a string, so isolate SQL decoding from that field.
					delete(sent, "format")
					encoded, err := json.Marshal(sent)
					require.NoError(t, err)
					require.NoError(t, json.Unmarshal(encoded, &query))
					assert.Equal(t, "SELECT 1", query.RawSQL)
					calls++
					frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []int64{1}))}
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
				}))
				t.Cleanup(ts.Close)
				ctx := enforceTestCtx(ts, false)
				if v2 {
					ctx = mcpgrafana.WithKubernetesClient(ctx, &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()})
				}
				inspected, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{UID: "test", Variables: map[string]string{}})
				require.NoError(t, err)
				require.Len(t, inspected, 1)
				require.Empty(t, inspected[0].Warnings)
				assert.Empty(t, inspected[0].RequiredVariables)
				assert.Equal(t, "SELECT 1", inspected[0].ProcessedQuery)
				result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: "test", PanelIDs: []int{1}})
				require.NoError(t, err)
				require.Empty(t, result.Errors)
				assert.Equal(t, 1, calls)
				assert.Equal(t, "$choice", target[tc.unused], "the saved target is unchanged")
			})
		}
	}
}

func TestDashboardPublicVariableSelections(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, tc := range []struct {
			name, current, want string
			selected, defined   bool
			overrides           map[string]string
		}{
			{name: "current before default", current: "east", selected: true, defined: true, want: "east"},
			{name: "missing current uses default", defined: true, want: "west"},
			{name: "explicit empty is data", selected: true, defined: true},
			{name: "override before current", current: "east", selected: true, defined: true, overrides: map[string]string{"choice": "override"}, want: "override"},
			{name: "override without definition", overrides: map[string]string{"choice": "new"}, want: "new"},
		} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, tc.name), func(t *testing.T) {
				variable := map[string]interface{}{"name": "unused", "type": "custom"}
				if tc.defined {
					variable["name"] = "choice"
					variable["options"] = []interface{}{map[string]interface{}{"value": "west"}}
					if tc.selected {
						variable["current"] = map[string]interface{}{"value": tc.current}
					}
				}
				db := preparationFixture(v2, variable, "SELECT ${choice:sqlstring}")
				calls := 0
				ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if servePreparationDashboard(w, r, db, v2) {
						return
					}
					require.Equal(t, "/api/ds/query", r.URL.Path)
					var payload map[string]interface{}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					assert.Equal(t, "SELECT '"+tc.want+"'", safeArray(payload, "queries")[0].(map[string]interface{})["rawSql"])
					calls++
					frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []int64{1}))}
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
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
				inspected, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{UID: "test", Variables: variables})
				require.NoError(t, err)
				require.Len(t, inspected, 1)
				require.Empty(t, inspected[0].Warnings)
				assert.Equal(t, "SELECT '"+tc.want+"'", inspected[0].ProcessedQuery)
				result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: "test", PanelIDs: []int{1}, Variables: variables})
				require.NoError(t, err)
				require.Empty(t, result.Errors)
				assert.Equal(t, inspected[0].ProcessedQuery, result.Results[1].Query)
				assert.Equal(t, 1, calls)
			})
		}
	}
}

func TestDashboardPublicEmptyOverrideName(t *testing.T) {
	db := preparationFixture(false, map[string]interface{}{"name": "unused", "type": "custom"}, "SELECT '$100' AS amount")
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if servePreparationDashboard(w, r, db, false) {
			return
		}
		require.Equal(t, "/api/ds/query", r.URL.Path)
		var payload map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, "SELECT '$100' AS amount", safeArray(payload, "queries")[0].(map[string]interface{})["rawSql"])
		calls++
		frames := data.Frames{data.NewFrame("", data.NewField("amount", nil, []string{"$100"}))}
		_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
	}))
	t.Cleanup(ts.Close)
	ctx := enforceTestCtx(ts, false)
	variables := map[string]string{"": "ignored"}
	inspected, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{UID: "test", Variables: variables})
	require.NoError(t, err)
	require.Len(t, inspected, 1)
	require.Empty(t, inspected[0].Warnings)
	assert.Equal(t, "SELECT '$100' AS amount", inspected[0].ProcessedQuery)
	result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: "test", PanelIDs: []int{1}, Variables: variables})
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	assert.Equal(t, 1, calls)
}

func TestDashboardRegisteredSQLAllPreparation(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, allow := range []bool{false, true} {
			t.Run(fmt.Sprintf("v2=%t/allow=%t", v2, allow), func(t *testing.T) {
				db := preparationFixture(v2, map[string]interface{}{
					"name": "choice", "type": "query", "current": map[string]interface{}{"value": "$__all"},
					"query": "SELECT 'east' AS __value", "datasource": map[string]interface{}{"uid": "postgres-uid"},
				}, "SELECT ${choice:sqlstring}")
				options, panels := 0, 0
				ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if servePreparationDashboard(w, r, db, v2) {
						return
					}
					if r.URL.Path == "/api/datasources/uid/postgres-uid" {
						_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"postgres"}`))
						return
					}
					require.Equal(t, "/api/ds/query", r.URL.Path)
					var payload map[string]interface{}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					target := safeArray(payload, "queries")[0].(map[string]interface{})
					if target["rawSql"] == "SELECT 'east' AS __value" {
						options++
						assert.Equal(t, true, target["rawQuery"])
						assert.Equal(t, "table", target["format"])
					} else {
						panels++
						assert.Equal(t, "SELECT 'east'", target["rawSql"])
					}
					frames := data.Frames{data.NewFrame("", data.NewField("__value", nil, []string{"east"}))}
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
				}))
				t.Cleanup(ts.Close)
				ctx := enforceTestCtx(ts, false)
				if v2 {
					ctx = mcpgrafana.WithKubernetesClient(ctx, &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()})
				}
				server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
				AddDashboardTools(server, false, allow)
				AddRunPanelQueryTools(server, true, allow)
				serverTransport, clientTransport := mcp.NewInMemoryTransports()
				serverSession, err := server.Connect(ctx, serverTransport, nil)
				require.NoError(t, err)
				t.Cleanup(func() { _ = serverSession.Close() })
				client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
				session, err := client.Connect(t.Context(), clientTransport, nil)
				require.NoError(t, err)
				t.Cleanup(func() { _ = session.Close() })
				// Raw inspection stays usable without touching datasource APIs,
				// including when the tool advertises the option-query permission.
				raw, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_dashboard_panel_queries", Arguments: map[string]interface{}{"uid": "test"}})
				require.NoError(t, err)
				require.False(t, raw.IsError)
				assert.Zero(t, options)
				assert.Zero(t, panels)
				for _, call := range []mcp.CallToolParams{
					{Name: "get_dashboard_panel_queries", Arguments: map[string]interface{}{"uid": "test", "variables": map[string]string{}}},
					{Name: "run_panel_query", Arguments: map[string]interface{}{"dashboardUid": "test", "panelIds": []int{1}}},
				} {
					result, err := session.CallTool(t.Context(), &call)
					require.NoError(t, err)
					require.False(t, result.IsError)
					encoded, err := json.Marshal(result)
					require.NoError(t, err)
					if allow {
						assert.Contains(t, string(encoded), "SELECT 'east'")
					} else {
						assert.Contains(t, string(encoded), "variable option queries are disabled")
					}
				}
				if allow {
					assert.Equal(t, 2, options, "separate tool calls each query their own options")
					assert.Equal(t, 1, panels)
				} else {
					assert.Zero(t, options)
					assert.Zero(t, panels)
				}
			})
		}
	}
}

func TestDashboardCloudWatchUIDOnlyPublicParity(t *testing.T) {
	for _, tc := range []struct {
		name, panelUID, panelType, targetUID, overrideUID string
		v2                                                bool
	}{
		{name: "v1 same UID", panelUID: "cloudwatch-uid", panelType: "cloudwatch", targetUID: "cloudwatch-uid"},
		{name: "v1 different UID", panelUID: "other", panelType: "postgres", targetUID: "cloudwatch-uid"},
		{name: "v1 Mixed", panelUID: "-- Mixed --", panelType: "datasource", targetUID: "cloudwatch-uid"},
		{name: "v1 variable", panelUID: "cloudwatch-uid", panelType: "cloudwatch", targetUID: "${ds:raw}"},
		{name: "v1 override", panelUID: "old", panelType: "postgres", targetUID: "old", overrideUID: "cloudwatch-uid"},
		{name: "v2 missing group", targetUID: "cloudwatch-uid", v2: true},
		{name: "v2 variable", targetUID: "[[ds]]", v2: true},
		{name: "v2 override", panelType: "postgres", targetUID: "old", overrideUID: "cloudwatch-uid", v2: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := preparationFixture(tc.v2, map[string]interface{}{
				"name": "region", "type": "custom", "current": map[string]interface{}{"value": "us-east-1"},
			}, "")
			appendCustomAllFixtureVariable(db, tc.v2, map[string]interface{}{
				"name": "ds", "type": "custom", "current": map[string]interface{}{"value": "cloudwatch-uid"},
			})
			target := preparationPanelTarget(db, tc.v2)
			delete(target, "rawSql")
			target["region"] = "$region"
			target["namespace"] = "AWS/EC2"
			target["metricName"] = "CPUUtilization"
			target["statistics"] = []interface{}{"Average"}
			if tc.v2 {
				setPreparationDatasource(db, true, tc.targetUID, tc.panelType)
			} else {
				collectAllPanels(db)[0]["datasource"] = map[string]interface{}{"uid": tc.panelUID, "type": tc.panelType}
				target["datasource"] = map[string]interface{}{"uid": tc.targetUID}
				if tc.overrideUID != "" {
					safeObject(target, "datasource")["type"] = tc.panelType
				}
			}
			lookups, panelCalls := 0, 0
			var submitted map[string]interface{}
			ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if servePreparationDashboard(w, r, db, tc.v2) {
					return
				}
				switch {
				case r.URL.Path == "/api/datasources/uid/cloudwatch-uid":
					lookups++
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"uid": "cloudwatch-uid", "type": "cloudwatch"})
				case r.URL.Path == "/api/ds/query":
					var payload map[string]interface{}
					if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload)) {
						return
					}
					submitted = safeArray(payload, "queries")[0].(map[string]interface{})
					panelCalls++
					frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []float64{1}))}
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(ts.Close)
			ctx := enforceTestCtx(ts, false)
			if tc.v2 {
				ctx = mcpgrafana.WithKubernetesClient(ctx, &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()})
			}
			start, end := "1704067200000", "1704070800000"
			// These are the public handlers, including dashboard fetching and
			// extraction, not a direct call to the preparation helper.
			var inspected []panelQuery
			if tc.overrideUID == "" {
				var err error
				inspected, err = GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{UID: "test", Variables: map[string]string{}, Start: start, End: end})
				require.NoError(t, err)
				require.Len(t, inspected, 1)
				require.Empty(t, inspected[0].Warnings)
				require.NotNil(t, inspected[0].ProcessedTarget)
				assert.Equal(t, "cloudwatch", inspected[0].Datasource.Type)
				assert.Equal(t, "$region", inspected[0].Target["region"], "inspection preserves the saved template")
			}
			result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: "test", PanelIDs: []int{1}, Start: start, End: end, DatasourceUID: tc.overrideUID})
			require.NoError(t, err)
			require.Empty(t, result.Errors)
			require.Contains(t, result.Results, 1)
			assert.Equal(t, "cloudwatch", result.Results[1].DatasourceType)
			assert.Equal(t, "cloudwatch-uid", result.Results[1].DatasourceUID)
			assert.Equal(t, 1, panelCalls)
			assert.Positive(t, lookups, "routing must use the authoritative datasource type")
			assert.Equal(t, "us-east-1", submitted["region"])
			assert.Equal(t, "CPUUtilization", submitted["metricName"])
			if len(inspected) > 0 {
				assert.Equal(t, inspected[0].ProcessedTarget, queryTargetFields(submitted))
			}
			assert.Equal(t, "$region", target["region"], "execution must not mutate the saved template")
		})
	}
}

func TestDashboardExpressionRequiredAfterTypeLookup(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, dsType := range []string{"postgres", "prometheus", "loki", "influxdb"} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, dsType), func(t *testing.T) {
				db := preparationFixture(v2, map[string]interface{}{"name": "unused", "type": "custom"}, "")
				setPreparationDatasource(db, v2, "actual-uid", "")
				target := preparationPanelTarget(db, v2)
				delete(target, "rawSql")
				target["metricName"] = "CPUUtilization"
				if !v2 {
					target["datasource"] = map[string]interface{}{"uid": "actual-uid"}
				}
				lookups, queries := 0, 0
				ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if servePreparationDashboard(w, r, db, v2) {
						return
					}
					switch {
					case r.URL.Path == "/api/datasources/uid/actual-uid":
						lookups++
						_ = json.NewEncoder(w).Encode(map[string]interface{}{"uid": "actual-uid", "type": dsType})
					default:
						queries++
						t.Errorf("an expression datasource must not receive an empty query: %s", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(ts.Close)
				ctx := enforceTestCtx(ts, true)
				if v2 {
					ctx = mcpgrafana.WithKubernetesClient(ctx, &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()})
				}
				result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: "test", PanelIDs: []int{1}})
				require.NoError(t, err)
				assert.Empty(t, result.Results)
				assert.Contains(t, result.Errors[1], "could not extract query")
				assert.Equal(t, 1, lookups)
				assert.Zero(t, queries)
			})
		}
	}
}

func TestDashboardUIDOnlyPublicLokiEnforcement(t *testing.T) {
	db := panelWithType("loki", "postgres")
	target := preparationPanelTarget(db, false)
	delete(target, "rawSql")
	target["datasource"] = map[string]interface{}{"uid": "loki"}
	var submitted string
	proxyCalls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/dashboards/uid/test":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"dashboard": db})
		case "/api/datasources/uid/loki":
			_, _ = w.Write([]byte(`{"uid":"loki","type":"loki"}`))
		case "/api/datasources/proxy/uid/loki/loki/api/v1/query_range":
			proxyCalls++
			submitted = r.URL.Query().Get("query")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
		default:
			t.Errorf("UID-only Loki targets must use the native guarded executor, got %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	result, err := runPanelQuery(enforceTestCtx(ts, true), RunPanelQueryParams{DashboardUID: "test", PanelIDs: []int{1}})
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Contains(t, result.Results, 1)
	assert.Equal(t, "loki", result.Results[1].DatasourceType)
	assert.Equal(t, 1, proxyCalls)
	assert.Contains(t, submitted, `agent_safe="true"`)
	assert.Contains(t, submitted, `app="secret"`)
}
