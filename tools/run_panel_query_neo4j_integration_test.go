//go:build integration

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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

// neo4jPluginInstalled reports whether Grafana has the Neo4j plugin. GF_INSTALL_PLUGINS
// installs it in the background after Grafana starts, so it can lag behind the health check.
func neo4jPluginInstalled(ctx context.Context) bool {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"/api/plugins/"+Neo4jDatasourceType+"/settings", nil)
	if err != nil {
		return false
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	} else if cfg.BasicAuth != nil {
		password, _ := cfg.BasicAuth.Password()
		req.SetBasicAuth(cfg.BasicAuth.Username(), password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

// createNeo4jTestDatasource adds a Neo4j datasource pointing at the compose neo4j service
// and removes it when the test ends.
func createNeo4jTestDatasource(t *testing.T) {
	t.Helper()
	ctx := newTestContext()
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	_, err := c.Datasources.AddDataSource(&models.AddDataSourceCommand{
		Name:   "Neo4j Panel Query Test",
		UID:    neo4jIntegrationDatasourceUID,
		Type:   Neo4jDatasourceType,
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

// createNeo4jTestDashboard creates a dashboard with Neo4j panels and removes it when the test
// ends. label is unique per run, so a write that is ever accepted cannot leak into later runs.
func createNeo4jTestDashboard(t *testing.T, label string) {
	t.Helper()
	ctx := newTestContext()

	dashJSON := map[string]interface{}{
		"uid":   neo4jIntegrationDashboardUID,
		"title": "Neo4j Panel Query Integration Test",
		"panels": []interface{}{
			neo4jTestPanel(1, "Generated rows", "UNWIND range(1, ${rows}) AS n RETURN n"),
			neo4jTestPanel(2, "Write attempt", fmt.Sprintf("CREATE (x:%s) RETURN x", label)),
			neo4jTestPanel(3, "Written nodes", fmt.Sprintf("MATCH (x:%s) RETURN count(x) AS nodes", label)),
			neo4jTestPanel(4, "Time range", "UNWIND [$__from, ${__to}] AS t RETURN t"),
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

// neo4jColumn returns the first column of refId A's first frame.
func neo4jColumn(results interface{}) ([]interface{}, error) {
	responses, ok := results.(backend.Responses)
	if !ok {
		return nil, fmt.Errorf("expected backend.Responses, got %T", results)
	}
	frames := responses["A"].Frames
	if len(frames) == 0 || frames[0] == nil || len(frames[0].Fields) == 0 {
		return nil, fmt.Errorf("refId A has no fields")
	}
	field := frames[0].Fields[0]
	values := make([]interface{}, field.Len())
	for i := range values {
		values[i], _ = field.ConcreteAt(i)
	}
	return values, nil
}

// requireNeo4jColumn runs one panel and returns its first column, failing on any error.
func requireNeo4jColumn(t *testing.T, params RunPanelQueryParams) (*PanelQueryResult, []interface{}) {
	t.Helper()
	result, err := runPanelQuery(newTestContext(), params)
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Len(t, params.PanelIDs, 1)
	panel := result.Results[params.PanelIDs[0]]
	require.NotNil(t, panel)
	column, err := neo4jColumn(panel.Results)
	require.NoError(t, err)
	return panel, column
}

func TestRunPanelQuery_Neo4j_E2E(t *testing.T) {
	ctx := newTestContext()
	require.Eventually(t, func() bool { return neo4jPluginInstalled(ctx) }, 2*time.Minute, 2*time.Second,
		"Grafana has no %s plugin; check the plugin install in the Grafana logs", Neo4jDatasourceType)

	label := fmt.Sprintf("McpGrafanaTest%d", time.Now().UnixNano())
	createNeo4jTestDatasource(t)
	createNeo4jTestDashboard(t, label)

	// CI waits for Grafana only; Neo4j may still be starting.
	require.Eventually(t, func() bool {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{DashboardUID: neo4jIntegrationDashboardUID, PanelIDs: []int{1}})
		if err != nil || len(result.Errors) > 0 || result.Results[1] == nil {
			return false
		}
		column, err := neo4jColumn(result.Results[1].Results)
		return err == nil && len(column) == 3
	}, 2*time.Minute, 2*time.Second, "neo4j did not return rows")

	t.Run("cypher panel with variable override", func(t *testing.T) {
		panel, column := requireNeo4jColumn(t, RunPanelQueryParams{
			DashboardUID: neo4jIntegrationDashboardUID,
			PanelIDs:     []int{1},
			Variables:    map[string]string{"rows": "5"},
		})
		assert.Equal(t, Neo4jDatasourceType, panel.DatasourceType)
		assert.Equal(t, "UNWIND range(1, 5) AS n RETURN n", panel.Query)
		assert.Len(t, column, 5)
	})

	t.Run("time range macros", func(t *testing.T) {
		start := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		panel, column := requireNeo4jColumn(t, RunPanelQueryParams{
			DashboardUID: neo4jIntegrationDashboardUID,
			PanelIDs:     []int{4},
			Start:        start.Format(time.RFC3339),
			End:          end.Format(time.RFC3339),
		})
		from, to := strconv.FormatInt(start.UnixMilli(), 10), strconv.FormatInt(end.UnixMilli(), 10)
		assert.Equal(t, "UNWIND ["+from+", "+to+"] AS t RETURN t", panel.Query)
		require.Len(t, column, 2)
		assert.EqualValues(t, start.UnixMilli(), column[0])
		assert.EqualValues(t, end.UnixMilli(), column[1])
	})

	t.Run("write query is rejected", func(t *testing.T) {
		result, err := runPanelQuery(ctx, RunPanelQueryParams{
			DashboardUID: neo4jIntegrationDashboardUID,
			PanelIDs:     []int{2},
		})
		require.NoError(t, err)
		require.Contains(t, result.Errors, 2)
		assert.Contains(t, result.Errors[2], "Neo.ClientError.Statement.AccessMode", "the plugin runs Cypher in read access mode")

		_, column := requireNeo4jColumn(t, RunPanelQueryParams{
			DashboardUID: neo4jIntegrationDashboardUID,
			PanelIDs:     []int{3},
		})
		require.Len(t, column, 1)
		assert.EqualValues(t, 0, column[0], "no node was created")
	})
}
