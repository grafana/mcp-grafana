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

func TestDashboardCloudWatchAliasDependencies(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, mode := range []struct {
			name, version  string
			flags          map[string]bool
			settingsStatus int
			aliasUsed      [4]bool // absent, null, empty, nonempty label
		}{
			{"modern", "13.2.3", map[string]bool{}, 200, [4]bool{true, true, false, false}},
			{"v9 dynamic", "9.0.0", map[string]bool{"cloudWatchDynamicLabels": true}, 200, [4]bool{true, false, false, false}},
			{"v9.0.2 dynamic", "9.0.2", map[string]bool{"cloudWatchDynamicLabels": true}, 200, [4]bool{true, false, false, false}},
			{"v9.0.3 dynamic", "9.0.3", map[string]bool{"cloudWatchDynamicLabels": true}, 200, [4]bool{true, true, false, false}},
			{"v9 legacy", "9.0.0", map[string]bool{}, 200, [4]bool{true, true, true, true}},
			{"v9.1 dynamic", "9.1.0", map[string]bool{"cloudWatchDynamicLabels": true}, 200, [4]bool{true, true, false, false}},
			{"v9 missing flags", "9.0.0", nil, 200, [4]bool{true, true, true, true}},
			{"unknown version", "", map[string]bool{}, 200, [4]bool{true, true, true, true}},
			{"unavailable settings", "", nil, 403, [4]bool{true, true, true, true}},
		} {
			for labelIndex, label := range []interface{}{nil, nil, "", "chosen-label"} {
				for _, selection := range []string{"ordinary", "$__all"} {
					for _, allow := range []bool{false, true} {
						t.Run(fmt.Sprintf("v2=%t/%s/label=%d/%s/allow=%t", v2, mode.name, labelIndex, selection, allow), func(t *testing.T) {
							optionCalls, panelCalls, settingsCalls := 0, 0, 0
							ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								w.Header().Set("Content-Type", "application/json")
								switch r.URL.Path {
								case "/api/frontend/settings":
									settingsCalls++
									w.WriteHeader(mode.settingsStatus)
									_ = json.NewEncoder(w).Encode(map[string]interface{}{"buildInfo": map[string]string{"version": mode.version}, "featureToggles": mode.flags})
								case "/api/datasources/uid/postgres-uid":
									_, _ = w.Write([]byte(`{"uid":"postgres-uid","type":"postgres"}`))
								case "/api/ds/query":
									var payload map[string]interface{}
									require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
									target := safeArray(payload, "queries")[0].(map[string]interface{})
									if target["rawSql"] == "SELECT options" {
										optionCalls++
									} else {
										panelCalls++
										wantAlias := "ordinary"
										if selection == "$__all" {
											wantAlias = "$choice"
											if mode.aliasUsed[labelIndex] {
												wantAlias = "resolved"
											}
										}
										assert.Equal(t, wantAlias, target["alias"])
										gotLabel, exists := target["label"]
										assert.Equal(t, labelIndex != 0, exists)
										assert.Equal(t, label, gotLabel)
									}
									frames := data.Frames{data.NewFrame("", data.NewField("__value", nil, []string{"resolved"}))}
									_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
								default:
									t.Errorf("unexpected request %s", r.URL.Path)
									http.NotFound(w, r)
								}
							}))
							t.Cleanup(ts.Close)
							ctx := context.WithValue(enforceTestCtx(ts, false), variableQueriesKey{}, allow)
							db := preparationFixture(v2, map[string]interface{}{
								"name": "choice", "type": "query", "current": map[string]interface{}{"value": selection},
								"query": "SELECT options", "datasource": map[string]interface{}{"uid": "postgres-uid"},
							}, "")
							setPreparationDatasource(db, v2, "cloudwatch-uid", "cloudwatch")
							target := preparationPanelTarget(db, v2)
							delete(target, "rawSql")
							target["namespace"] = "Test/Application"
							target["metricName"] = "CPUUtilization"
							target["alias"] = "$choice"
							if labelIndex != 0 {
								target["label"] = label
							}
							args := DashboardPanelQueriesParams{Start: "2024-01-01T00:00:00Z", End: "2024-01-01T01:00:00Z"}
							inspected, err := inspectPreparationFixture(ctx, db, v2, args)
							require.NoError(t, err)
							require.Len(t, inspected, 1)
							assert.Len(t, inspected[0].RequiredVariables, boolInt(mode.aliasUsed[labelIndex]))
							_, err = runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, IsV2: v2, PanelID: 1, Start: args.Start, End: args.End})
							if selection == "$__all" && mode.aliasUsed[labelIndex] && !allow {
								require.ErrorContains(t, err, "variable option queries are disabled")
								require.NotEmpty(t, inspected[0].Warnings)
								assert.Zero(t, panelCalls)
							} else {
								require.NoError(t, err)
								require.Empty(t, inspected[0].Warnings)
								assert.Equal(t, 1, panelCalls)
							}
							wantOptionCalls := 0
							if selection == "$__all" && mode.aliasUsed[labelIndex] && allow {
								wantOptionCalls = 2
							}
							assert.Equal(t, wantOptionCalls, optionCalls)
							if labelIndex == 0 {
								assert.Zero(t, settingsCalls, "absent label always uses alias")
							} else if mode.settingsStatus == 200 {
								assert.Equal(t, 1, settingsCalls, "reuse frontend settings across inspection and execution")
							}
							assert.Equal(t, "$choice", target["alias"], "saved query must remain intact")
						})
					}
				}
			}
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestDashboardCloudWatchSharedAliasDependency(t *testing.T) {
	for _, field := range []string{"label", "dimensions"} {
		t.Run(field, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/frontend/settings", r.URL.Path)
				_, _ = w.Write([]byte(`{"buildInfo":{"version":"13.2.3"}}`))
			}))
			t.Cleanup(ts.Close)
			db := preparationFixture(false, map[string]interface{}{
				"name": "choice", "type": "query", "query": "SELECT options",
				"current": map[string]interface{}{"value": "$__all"},
			}, "")
			setPreparationDatasource(db, false, "cloudwatch-uid", "cloudwatch")
			target := preparationPanelTarget(db, false)
			delete(target, "rawSql")
			target["metricName"] = "CPUUtilization"
			target["alias"] = "$choice"
			target["label"] = ""
			if field == "label" {
				target["label"] = "$choice"
			} else {
				target["dimensions"] = map[string]interface{}{"ServiceName": []interface{}{"$choice"}}
			}
			queries, err := inspectPreparationFixture(enforceTestCtx(ts, false), db, false, DashboardPanelQueriesParams{Variables: map[string]string{}})
			require.NoError(t, err)
			require.Len(t, queries, 1)
			assert.Len(t, queries[0].RequiredVariables, 1)
			require.NotEmpty(t, queries[0].Warnings, "an active field must still resolve the variable shared with the ignored alias")
			assert.Contains(t, queries[0].Warnings[0], "variable option queries are disabled")
		})
	}
}
