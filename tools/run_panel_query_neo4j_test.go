//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// neo4jDSQueryServer answers /api/ds/query with one empty frame and captures the request.
func neo4jDSQueryServer(t *testing.T, payload *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/ds/query", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(payload))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[]},"data":{"values":[]}}]}}}`))
	}))
}

func neo4jPanel(cypher string) map[string]interface{} {
	return map[string]interface{}{
		"id":    5,
		"title": "Accounts per system",
		"datasource": map[string]interface{}{
			"uid":  "neo4j",
			"type": Neo4jDatasourceType,
		},
		"targets": []interface{}{
			map[string]interface{}{
				"refId":       "A",
				"cypherQuery": cypher,
				"Format":      "table",
			},
		},
	}
}

const neo4jTestCypher = "MATCH (a:Account {system: '${system}'}) RETURN count(a) AS accounts"

// sentNeo4jQuery returns the single query of a captured /api/ds/query payload.
func sentNeo4jQuery(t *testing.T, payload map[string]interface{}) map[string]interface{} {
	t.Helper()
	queries, ok := payload["queries"].([]interface{})
	require.True(t, ok)
	require.Len(t, queries, 1)
	q, ok := queries[0].(map[string]interface{})
	require.True(t, ok)
	return q
}

func TestNormalizeDatasourceType_Neo4j(t *testing.T) {
	assert.Equal(t, Neo4jDatasourceType, normalizeDatasourceType(Neo4jDatasourceType))
	assert.Equal(t, Neo4jDatasourceType, normalizeDatasourceType("Kniepdennis-Neo4j-Datasource"))
	// Read-only depends on this plugin's read access mode, so other Neo4j plugins are not routed.
	assert.NotEqual(t, Neo4jDatasourceType, normalizeDatasourceType("acme-neo4j-datasource"))
	assert.NotEqual(t, Neo4jDatasourceType, normalizeDatasourceType("neo4j"))
	assert.False(t, usesStructuredTarget(Neo4jDatasourceType), "Neo4j targets carry a Cypher string")
}

func TestExtractPanelInfo_Neo4j(t *testing.T) {
	info, err := extractPanelInfo(neo4jPanel(neo4jTestCypher), 0)
	require.NoError(t, err)
	assert.Equal(t, "neo4j", info.DatasourceUID)
	assert.Equal(t, Neo4jDatasourceType, info.DatasourceType)
	assert.Equal(t, neo4jTestCypher, info.Query)
	require.NotNil(t, info.RawTarget)
	assert.Equal(t, "table", info.RawTarget["Format"])
}

func TestExtractPanelInfo_Neo4jIgnoresLeftoverQueryField(t *testing.T) {
	panel := neo4jPanel(neo4jTestCypher)
	panel["targets"].([]interface{})[0].(map[string]interface{})["query"] = "up"
	info, err := extractPanelInfo(panel, 0)
	require.NoError(t, err)
	assert.Equal(t, neo4jTestCypher, info.Query, "cypherQuery is what the plugin runs")
}

func TestExtractPanelInfo_NoQueryListsAllFields(t *testing.T) {
	panel := neo4jPanel("")
	panel["datasource"] = map[string]interface{}{"uid": "prometheus", "type": "prometheus"}
	_, err := extractPanelInfo(panel, 0)
	require.Error(t, err)
	assert.Equal(t, "could not extract query from panel target (checked: "+strings.Join(queryFields, ", ")+")", err.Error())
	assert.Contains(t, err.Error(), "cypherQuery")
}

func TestSubstituteStructuredTargetMacros(t *testing.T) {
	start := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	tests := []struct {
		name, in, want string
	}{
		{"unbraced", "WHERE e.ts > $__from AND e.ts < $__to", "WHERE e.ts > 1705312800000 AND e.ts < 1705316400000"},
		{"braced", "WHERE e.ts > ${__from} AND e.ts < ${__to}", "WHERE e.ts > 1705312800000 AND e.ts < 1705316400000"},
		{"plugin macros kept", "$__fromTime $__toTime_ms", "$__fromTime $__toTime_ms"},
		{"formatted forms kept", "${__from:date:iso}", "${__from:date:iso}"},
		{"range macros still applied", "$__from $__range_s", "1705312800000 3600"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, substituteStructuredTargetMacros(tt.in, start, end))
		})
	}
}

func TestExecuteStructuredPanelQuery_Neo4j(t *testing.T) {
	var payload map[string]interface{}
	ts := neo4jDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	cypher := "MATCH (e:Event {system: '${system}'}) WHERE e.ts > $__from AND e.ts < ${__to} RETURN e"
	info, err := extractPanelInfo(neo4jPanel(cypher), 0)
	require.NoError(t, err)

	variables := templateVariableValues{"system": {"ad"}}
	results, target, err := executeStructuredPanelQuery(ctx, "neo4j", Neo4jDatasourceType, info, "2024-01-15T10:00:00Z", "2024-01-15T11:00:00Z", variables)
	require.NoError(t, err)
	assert.NotNil(t, results)
	want := "MATCH (e:Event {system: 'ad'}) WHERE e.ts > 1705312800000 AND e.ts < 1705316400000 RETURN e"
	assert.Equal(t, want, target["cypherQuery"], "substituted target is returned")

	q := sentNeo4jQuery(t, payload)
	ds, ok := q["datasource"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "neo4j", ds["uid"])
	assert.Equal(t, Neo4jDatasourceType, ds["type"])
	assert.Equal(t, "A", q["refId"])
	assert.Equal(t, want, q["cypherQuery"])
	assert.Equal(t, "table", q["Format"])
	assert.NotEmpty(t, payload["from"])
	assert.NotEmpty(t, payload["to"])
}

func TestRunSinglePanelQuery_Neo4jReportsCypherThatRan(t *testing.T) {
	var payload map[string]interface{}
	ts := neo4jDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	panel := neo4jPanel("MATCH (a:Account {system: '${system}'}) WHERE a.seen > $__from RETURN a LIMIT $__range_s")
	panel["id"] = float64(5) // JSON-decoded dashboards carry numbers as float64
	db := map[string]interface{}{
		"panels": []interface{}{panel},
		"templating": map[string]interface{}{"list": []interface{}{
			map[string]interface{}{"name": "system", "type": "textbox", "current": map[string]interface{}{"value": "default"}},
		}},
	}
	result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
		DB: db, PanelID: 5, Start: "2024-01-15T10:00:00Z", End: "2024-01-15T11:00:00Z",
		Variables: map[string]string{"system": "ad"},
	})
	require.NoError(t, err)
	assert.Equal(t, Neo4jDatasourceType, result.DatasourceType)
	want := "MATCH (a:Account {system: 'ad'}) WHERE a.seen > 1705312800000 RETURN a LIMIT 3600"
	assert.Equal(t, want, result.Query, "variables and macros are both substituted")
	assert.Equal(t, want, sentNeo4jQuery(t, payload)["cypherQuery"], "the reported query is the one sent")
	require.NotEmpty(t, result.Hints, "an empty /api/ds/query response yields hints")
	assert.Contains(t, strings.Join(result.Hints, "\n"), "the query field shows the substituted Cypher")
}

func TestRunSinglePanelQuery_OtherNeo4jPluginNotSupported(t *testing.T) {
	for _, dsType := range []string{"acme-neo4j-datasource", "neo4j"} {
		panel := neo4jPanel("MATCH (n) RETURN n")
		panel["id"] = float64(5)
		panel["datasource"] = map[string]interface{}{"uid": "neo4j", "type": dsType}
		_, err := runSinglePanelQuery(context.Background(), singlePanelQueryParams{
			DB: map[string]interface{}{"panels": []interface{}{panel}}, PanelID: 5, Start: "now-1h", End: "now",
		})
		require.Error(t, err, dsType)
		assert.Contains(t, err.Error(), "not supported by run_panel_query", dsType)
	}
}

func TestExecuteStructuredPanelQuery_Neo4jPerRefIDError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{"A":{"status":500,"error":"Writing in read access mode not allowed","errorSource":"downstream","frames":[]}}}`))
	}))
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	info, err := extractPanelInfo(neo4jPanel(neo4jTestCypher), 0)
	require.NoError(t, err)

	_, _, err = executeStructuredPanelQuery(ctx, "neo4j", Neo4jDatasourceType, info, "now-1h", "now", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refId A (downstream): Writing in read access mode not allowed")
}

func TestExtractPanelInfoV2_Neo4j(t *testing.T) {
	panel := map[string]interface{}{
		"id":    6,
		"title": "Accounts per system (v2)",
		"data": map[string]interface{}{
			"spec": map[string]interface{}{
				"queries": []interface{}{
					map[string]interface{}{
						"spec": map[string]interface{}{
							"refId": "A",
							"query": map[string]interface{}{
								"group":      Neo4jDatasourceType,
								"datasource": map[string]interface{}{"name": "neo4j"},
								"spec": map[string]interface{}{
									"cypherQuery": "MATCH (a:Account) RETURN count(a) AS accounts",
									"query":       "leftover",
									"Format":      "table",
								},
							},
						},
					},
				},
			},
		},
	}
	info, err := extractPanelInfoV2(panel, 0)
	require.NoError(t, err)
	assert.Equal(t, "neo4j", info.DatasourceUID)
	assert.Equal(t, Neo4jDatasourceType, info.DatasourceType)
	assert.Equal(t, "MATCH (a:Account) RETURN count(a) AS accounts", info.Query)
	require.NotNil(t, info.RawTarget)
	assert.Equal(t, "A", info.RawTarget["refId"])
}
