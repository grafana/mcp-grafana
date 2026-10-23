//go:build integration

package tools

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const (
	neo4jIntegrationDatasourceUID = "neo4j-panel-test"
	neo4jIntegrationDashboardUID  = "neo4j-panel-test"
)

// createNeo4jTestDatasource adds a Neo4j datasource pointing at the compose neo4j service
// and removes it when the test ends. The plugin is installed via GF_INSTALL_PLUGINS.
func createNeo4jTestDatasource(t *testing.T) {
	t.Helper()
	ctx := newTestContext()
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	_, err := c.Datasources.AddDataSource(&models.AddDataSourceCommand{
		Name:   "Neo4j Panel Query Test",
		UID:    neo4jIntegrationDatasourceUID,
		Type:   "kniepdennis-neo4j-datasource",
		Access: models.DsAccess("proxy"),
		JSONData: models.JSON(map[string]interface{}{
			"url":      "neo4j://neo4j:7687",
			"database": "neo4j",
			"username": "neo4j",
		}),
		SecureJSONData: map[string]string{"password": "mcptests-password"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = c.Datasources.DeleteDataSourceByUID(neo4jIntegrationDatasourceUID)
	})
}

func neo4jTestPanel(id int, title, cypher string) map[string]interface{} {
	return map[string]interface{}{
		"id":    float64(id),
		"title": title,
		"type":  "table",
		// No type: run_panel_query resolves it from the datasource UID.
		"datasource": map[string]interface{}{"uid": neo4jIntegrationDatasourceUID},
		"targets": []interface{}{
			map[string]interface{}{"refId": "A", "cypherQuery": cypher, "Format": "table"},
		},
	}
}

// createNeo4jTestDashboard creates a dashboard with Neo4j panels and removes it when the test ends.
func createNeo4jTestDashboard(t *testing.T) {
	t.Helper()
	ctx := newTestContext()

	dashJSON := map[string]interface{}{
		"uid":   neo4jIntegrationDashboardUID,
		"title": "Neo4j Panel Query Integration Test",
		"panels": []interface{}{
			neo4jTestPanel(1, "Generated rows", "UNWIND range(1, ${rows}) AS n RETURN n"),
			neo4jTestPanel(2, "Write attempt", "CREATE (x:McpGrafanaTest) RETURN x"),
			neo4jTestPanel(3, "Written nodes", "MATCH (x:McpGrafanaTest) RETURN count(x) AS nodes"),
		},
		"templating": map[string]interface{}{
			"list": []interface{}{
				map[string]interface{}{
					"name":    "rows",
					"type":    "textbox",
					"current": map[string]interface{}{"value": "3"},
				},
			},
		},
	}

	raw, err := json.Marshal(dashJSON)
	require.NoError(t, err)
	var dashboard map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &dashboard))

	_, err = updateDashboard(ctx, UpdateDashboardParams{Dashboard: dashboard, Overwrite: true})
	require.NoError(t, err)
	t.Cleanup(func() {
		c := mcpgrafana.GrafanaClientFromContext(ctx)
		_, _ = c.Dashboards.DeleteDashboardByUID(neo4jIntegrationDashboardUID)
	})
}

// neo4jRowCount returns the number of rows in the first frame of refId A.
func neo4jRowCount(t *testing.T, results interface{}) int {
	t.Helper()
	responses, ok := results.(backend.Responses)
	require.True(t, ok, "expected backend.Responses, got %T", results)
	require.NotEmpty(t, responses["A"].Frames)
	return responses["A"].Frames[0].Rows()
}

func TestRunPanelQuery_Neo4j_E2E(t *testing.T) {
	createNeo4jTestDatasource(t)
	createNeo4jTestDashboard(t)
	ctx := newTestContext()

	// CI waits for Grafana only; Neo4j may still be starting.
	require.Eventually(t, func() bool {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: neo4jIntegrationDashboardUID, PanelIDs: []int{1}})
		return err == nil && len(result.Errors) == 0
	}, 2*time.Minute, 2*time.Second, "neo4j did not become ready")

	t.Run("cypher panel with variable override", func(t *testing.T) {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID: neo4jIntegrationDashboardUID,
			PanelIDs:     []int{1},
			Variables:    map[string]string{"rows": "5"},
		})
		require.NoError(t, err)
		require.Empty(t, result.Errors)
		require.Contains(t, result.Results, 1)
		panel := result.Results[1]
		assert.Equal(t, "neo4j", normalizeDatasourceType(panel.DatasourceType))
		assert.Equal(t, "UNWIND range(1, 5) AS n RETURN n", panel.Query)
		assert.Equal(t, 5, neo4jRowCount(t, panel.Results))
	})

	t.Run("write query is rejected", func(t *testing.T) {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID: neo4jIntegrationDashboardUID,
			PanelIDs:     []int{2, 3},
		})
		require.NoError(t, err)
		require.Contains(t, result.Errors, 2)
		assert.Contains(t, result.Errors[2], "Neo.ClientError.Statement.AccessMode", "the plugin runs Cypher in read access mode")
		require.Contains(t, result.Results, 3)
		responses := result.Results[3].Results.(backend.Responses)
		nodes, ok := responses["A"].Frames[0].Fields[0].ConcreteAt(0)
		require.True(t, ok)
		assert.EqualValues(t, 0, nodes, "no node was created")
	})
}
