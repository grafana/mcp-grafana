//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const neo4jTestDatasourceType = "neo4j-datasource"

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

func neo4jPanel() map[string]interface{} {
	return map[string]interface{}{
		"id":    5,
		"title": "Accounts per system",
		"datasource": map[string]interface{}{
			"uid":  "neo4j",
			"type": neo4jTestDatasourceType,
		},
		"targets": []interface{}{
			map[string]interface{}{
				"refId":       "A",
				"cypherQuery": "MATCH (a:Account {system: '${system}'}) RETURN count(a) AS accounts",
				"Format":      "table",
			},
		},
	}
}

func TestNormalizeDatasourceType_Neo4j(t *testing.T) {
	assert.Equal(t, "neo4j", normalizeDatasourceType(neo4jTestDatasourceType))
	assert.Equal(t, "neo4j", normalizeDatasourceType("Acme-Neo4j-Datasource"))
	assert.False(t, usesStructuredTarget(neo4jTestDatasourceType), "Neo4j targets carry a Cypher string")
}

func TestExtractPanelInfo_Neo4j(t *testing.T) {
	info, err := extractPanelInfo(neo4jPanel(), 0)
	require.NoError(t, err)
	assert.Equal(t, "neo4j", info.DatasourceUID)
	assert.Equal(t, neo4jTestDatasourceType, info.DatasourceType)
	assert.Equal(t, "MATCH (a:Account {system: '${system}'}) RETURN count(a) AS accounts", info.Query)
	require.NotNil(t, info.RawTarget)
	assert.Equal(t, "table", info.RawTarget["Format"])
}

func TestExecuteNeo4jPanelQuery(t *testing.T) {
	var payload map[string]interface{}
	ts := neo4jDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	info, err := extractPanelInfo(neo4jPanel(), 0)
	require.NoError(t, err)

	variables := templateVariableValues{"system": {"ad"}}
	results, err := executeNeo4jPanelQuery(ctx, "neo4j", neo4jTestDatasourceType, info, "now-1h", "now", variables)
	require.NoError(t, err)
	assert.NotNil(t, results)

	queries, ok := payload["queries"].([]interface{})
	require.True(t, ok)
	require.Len(t, queries, 1)
	q, ok := queries[0].(map[string]interface{})
	require.True(t, ok)
	ds, ok := q["datasource"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "neo4j", ds["uid"])
	assert.Equal(t, neo4jTestDatasourceType, ds["type"])
	assert.Equal(t, "A", q["refId"])
	assert.Equal(t, "MATCH (a:Account {system: 'ad'}) RETURN count(a) AS accounts", q["cypherQuery"])
	assert.Equal(t, "table", q["Format"])
	assert.NotEmpty(t, payload["from"])
	assert.NotEmpty(t, payload["to"])
}

func TestExecuteNeo4jPanelQuery_PerRefIDError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{"A":{"status":500,"error":"Writing in read access mode not allowed","errorSource":"downstream","frames":[]}}}`))
	}))
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	info, err := extractPanelInfo(neo4jPanel(), 0)
	require.NoError(t, err)

	_, err = executeNeo4jPanelQuery(ctx, "neo4j", neo4jTestDatasourceType, info, "now-1h", "now", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refId A (downstream): Writing in read access mode not allowed")
}

func TestExecuteNeo4jPanelQuery_NilTarget(t *testing.T) {
	_, err := executeNeo4jPanelQuery(context.Background(), "neo4j", neo4jTestDatasourceType, &panelInfo{}, "now-1h", "now", nil)
	require.Error(t, err)
	_, err = executeNeo4jPanelQuery(context.Background(), "neo4j", neo4jTestDatasourceType, nil, "now-1h", "now", nil)
	require.Error(t, err)
}

func TestRunSinglePanelQuery_Neo4jEmptyResult(t *testing.T) {
	var payload map[string]interface{}
	ts := neo4jDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	panel := neo4jPanel()
	panel["id"] = float64(5) // JSON-decoded dashboards carry numbers as float64
	db := map[string]interface{}{
		"panels": []interface{}{panel},
		"templating": map[string]interface{}{"list": []interface{}{
			map[string]interface{}{"name": "system", "type": "textbox", "current": map[string]interface{}{"value": "default"}},
		}},
	}
	result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
		DB: db, PanelID: 5, Start: "now-1h", End: "now",
		Variables: map[string]string{"system": "ad"},
	})
	require.NoError(t, err)
	assert.Equal(t, neo4jTestDatasourceType, result.DatasourceType)
	assert.Equal(t, "MATCH (a:Account {system: 'ad'}) RETURN count(a) AS accounts", result.Query, "the substituted Cypher is reported as the query")
	require.NotEmpty(t, result.Hints, "an empty /api/ds/query response yields hints")
	assert.Contains(t, strings.Join(result.Hints, "\n"), "the query field shows the substituted Cypher")
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
								"group":      neo4jTestDatasourceType,
								"datasource": map[string]interface{}{"name": "neo4j"},
								"spec": map[string]interface{}{
									"cypherQuery": "MATCH (a:Account) RETURN count(a) AS accounts",
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
	assert.Equal(t, neo4jTestDatasourceType, info.DatasourceType)
	assert.Equal(t, "MATCH (a:Account) RETURN count(a) AS accounts", info.Query)
	require.NotNil(t, info.RawTarget)
	assert.Equal(t, "A", info.RawTarget["refId"])
}
