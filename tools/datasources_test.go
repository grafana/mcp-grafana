// Requires a Grafana instance running on localhost:3000,
// with a Prometheus datasource provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"encoding/json"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatasourcesTools(t *testing.T) {
	t.Run("list datasources", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listDatasources(ctx, ListDatasourcesParams{})
		require.NoError(t, err)

		// Verify the core datasources provisioned in the test environment are present.
		uids := make(map[string]bool, len(result.Datasources))
		for _, ds := range result.Datasources {
			uids[ds.UID] = true
		}
		assert.True(t, uids["prometheus"], "prometheus datasource should be provisioned")
		assert.True(t, uids["loki"], "loki datasource should be provisioned")
		assert.True(t, uids["graphite"], "graphite datasource should be provisioned")
		assert.True(t, uids["tempo"], "tempo datasource should be provisioned")
		assert.True(t, uids["elasticsearch"], "elasticsearch datasource should be provisioned")
		assert.True(t, uids["opensearch"], "opensearch datasource should be provisioned")
		assert.True(t, uids["influxdb-flux"], "influxdb-flux datasource should be provisioned")
		assert.True(t, uids["influxdb-influxql"], "influxdb-influxql datasource should be provisioned")
	})

	t.Run("list datasources for type", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listDatasources(ctx, ListDatasourcesParams{Type: "Prometheus"})
		require.NoError(t, err)
		// Only two Prometheus datasources are provisioned in the test environment.
		assert.Len(t, result.Datasources, 2)
	})

	t.Run("list datasources by name", func(t *testing.T) {
		ctx := newTestContext()

		// Case-insensitive substring match on the datasource Name field.
		// The provisioned names are "InfluxDB Flux" and "InfluxDB InfluxQL";
		// the lowercase query proves case-insensitivity.
		result, err := listDatasources(ctx, ListDatasourcesParams{Name: "influxdb"})
		require.NoError(t, err)
		uids := make([]string, len(result.Datasources))
		for i, ds := range result.Datasources {
			uids[i] = ds.UID
		}
		assert.ElementsMatch(t, []string{"influxdb-flux", "influxdb-influxql"}, uids)

		// Name composes with the type filter (AND). "Custom Time" is provisioned
		// for both an elasticsearch and an opensearch datasource, so the name
		// substring alone is ambiguous across types; adding the type narrows the
		// match to just the elasticsearch one.
		result, err = listDatasources(ctx, ListDatasourcesParams{Type: "elasticsearch", Name: "custom"})
		require.NoError(t, err)
		require.Len(t, result.Datasources, 1)
		assert.Equal(t, "elasticsearch-custom-time", result.Datasources[0].UID)

		// No match returns an empty list.
		result, err = listDatasources(ctx, ListDatasourcesParams{Name: "does-not-exist"})
		require.NoError(t, err)
		assert.Empty(t, result.Datasources)
	})

	t.Run("get datasource by uid", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getDatasource(ctx, GetDatasourceParams{
			UID: "prometheus",
		})
		require.NoError(t, err)
		assert.Equal(t, "Prometheus", result.Name)
	})

	t.Run("get datasource by uid - not found", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getDatasource(ctx, GetDatasourceParams{
			UID: "non-existent-datasource",
		})
		require.Error(t, err)
		require.Nil(t, result)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("get datasource by name", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getDatasource(ctx, GetDatasourceParams{
			Name: "Prometheus",
		})
		require.NoError(t, err)
		assert.Equal(t, "Prometheus", result.Name)
	})

	t.Run("get datasource - neither provided", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getDatasource(ctx, GetDatasourceParams{})
		require.Error(t, err)
		require.Nil(t, result)
		assert.Contains(t, err.Error(), "either uid or name must be provided")
	})
}

func TestCreateDatasourceTools(t *testing.T) {
	t.Run("create datasource", func(t *testing.T) {
		ctx := newTestContext()

		toolResult, err := createDatasource(ctx, CreateDatasourceParams{
			Name:           "mcp-test-prometheus",
			Type:           "prometheus",
			URL:            "http://prometheus:9090",
			SchemaReviewed: true,
		})
		require.NoError(t, err)
		require.NotNil(t, toolResult)
		assert.False(t, toolResult.IsError)

		require.Len(t, toolResult.Content, 2)
		text, ok := toolResult.Content[0].(*mcp.TextContent)
		require.True(t, ok)

		var result CreateDatasourceResult
		require.NoError(t, json.Unmarshal([]byte(text.Text), &result))
		assert.Equal(t, "mcp-test-prometheus", result.Name)
		assert.NotEmpty(t, result.UID)

		configPageURL := "http://localhost:3000/connections/datasources/edit/" + result.UID
		assert.Contains(t, result.NextSteps, configPageURL)

		link, ok := toolResult.Content[1].(*mcp.ResourceLink)
		require.True(t, ok)
		assert.Equal(t, configPageURL, link.URI)
		assert.Equal(t, result.Name, link.Name)

		c := mcpgrafana.GrafanaClientFromContext(ctx)
		t.Cleanup(func() {
			_, _ = c.Datasources.DeleteDataSourceByUID(result.UID)
		})
	})

	t.Run("create datasource - basicAuth flag", func(t *testing.T) {
		ctx := newTestContext()

		toolResult, err := createDatasource(ctx, CreateDatasourceParams{
			Name:           "mcp-test-prometheus-basicauth",
			Type:           "prometheus",
			BasicAuth:      true,
			SchemaReviewed: true,
		})
		require.NoError(t, err)
		require.NotNil(t, toolResult)
		assert.False(t, toolResult.IsError)

		require.Len(t, toolResult.Content, 2)
		text, ok := toolResult.Content[0].(*mcp.TextContent)
		require.True(t, ok)

		var result CreateDatasourceResult
		require.NoError(t, json.Unmarshal([]byte(text.Text), &result))
		assert.Equal(t, "mcp-test-prometheus-basicauth", result.Name)
		assert.NotEmpty(t, result.UID)

		configPageURL := "http://localhost:3000/connections/datasources/edit/" + result.UID
		assert.Contains(t, result.NextSteps, configPageURL)

		link, ok := toolResult.Content[1].(*mcp.ResourceLink)
		require.True(t, ok)
		assert.Equal(t, configPageURL, link.URI)
		assert.Equal(t, result.Name, link.Name)

		c := mcpgrafana.GrafanaClientFromContext(ctx)
		t.Cleanup(func() {
			_, _ = c.Datasources.DeleteDataSourceByUID(result.UID)
		})
	})
}

func TestCheckDatasourceHealthTool(t *testing.T) {
	t.Run("check health of provisioned prometheus datasource", func(t *testing.T) {
		ctx := newTestContext()
		result, err := checkDatasourceHealth(ctx, CheckDatasourceHealthParams{UID: "prometheus"})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "prometheus", result.UID)
		assert.NotEmpty(t, result.Message)
	})

	t.Run("check health of non-existent datasource returns error", func(t *testing.T) {
		ctx := newTestContext()
		_, err := checkDatasourceHealth(ctx, CheckDatasourceHealthParams{UID: "non-existent-uid"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "non-existent-uid")
	})
}

func TestCheckDatasourcesHealthTool(t *testing.T) {
	t.Run("check all datasources returns summary", func(t *testing.T) {
		ctx := newTestContext()
		result, err := checkDatasourcesHealth(ctx, BulkCheckDatasourceHealthParams{})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Greater(t, result.Total, 0)
		assert.Len(t, result.Results, result.Healthy+result.Unhealthy)
		assert.LessOrEqual(t, len(result.Results), result.Total)
	})

	t.Run("filter by type returns only matching datasources", func(t *testing.T) {
		ctx := newTestContext()
		result, err := checkDatasourcesHealth(ctx, BulkCheckDatasourceHealthParams{Type: "prometheus"})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Greater(t, result.Total, 0)
		for _, r := range result.Results {
			assert.Equal(t, "prometheus", r.Type)
		}
	})

	t.Run("explicit uids checks only those datasources", func(t *testing.T) {
		ctx := newTestContext()
		result, err := checkDatasourcesHealth(ctx, BulkCheckDatasourceHealthParams{UIDs: []string{"prometheus", "loki"}})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 2, result.Total)
		uids := make([]string, len(result.Results))
		for i, r := range result.Results {
			uids[i] = r.UID
		}
		assert.ElementsMatch(t, []string{"prometheus", "loki"}, uids)
	})
}

func TestUpdateDatasourceTool(t *testing.T) {
	ctx := newTestContext()
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	// Create a temporary datasource to test against so we don't modify provisioned ones.
	dsAccess := models.DsAccess("proxy")
	addResp, err := c.Datasources.AddDataSource(&models.AddDataSourceCommand{
		Name:   "Integration Test DS",
		Type:   "prometheus",
		Access: dsAccess,
		URL:    "http://prometheus:9090",
	})
	require.NoError(t, err)
	require.NotNil(t, addResp.Payload)
	testUID := addResp.Payload.Datasource.UID

	t.Cleanup(func() {
		_, _ = c.Datasources.DeleteDataSourceByUID(testUID)
	})

	// parseUpdate unwraps the JSON text content of a successful update call.
	parseUpdate := func(t *testing.T, res *mcp.CallToolResult) UpdateDatasourceResult {
		t.Helper()
		require.NotNil(t, res)
		require.Len(t, res.Content, 1)
		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		var r UpdateDatasourceResult
		require.NoError(t, json.Unmarshal([]byte(text.Text), &r))
		return r
	}

	t.Run("first call returns schema guidance", func(t *testing.T) {
		res, err := updateDatasource(ctx, UpdateDatasourceParams{UID: testUID})
		require.NoError(t, err)
		require.Len(t, res.Content, 1)
		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		var guidance map[string]any
		require.NoError(t, json.Unmarshal([]byte(text.Text), &guidance))
		assert.Equal(t, "prometheus", guidance["type"])
		assert.NotEmpty(t, guidance["fields"])
	})

	t.Run("update name", func(t *testing.T) {
		newName := "Integration Test DS Updated"
		res, err := updateDatasource(ctx, UpdateDatasourceParams{UID: testUID, Name: &newName, SchemaReviewed: true})
		require.NoError(t, err)
		require.NotNil(t, res)
	})

	t.Run("update url", func(t *testing.T) {
		newURL := "http://prometheus:9090"
		res, err := updateDatasource(ctx, UpdateDatasourceParams{UID: testUID, URL: &newURL, SchemaReviewed: true})
		require.NoError(t, err)
		require.NotNil(t, res)
	})

	t.Run("health check included in result", func(t *testing.T) {
		newName := "Integration Test DS Health"
		res, err := updateDatasource(ctx, UpdateDatasourceParams{UID: testUID, Name: &newName, SchemaReviewed: true})
		require.NoError(t, err)
		result := parseUpdate(t, res)
		require.NotNil(t, result.Health)
		assert.Equal(t, testUID, result.Health.UID)
		assert.NotEmpty(t, result.Health.Message)
	})

	t.Run("update non-existent datasource returns error", func(t *testing.T) {
		newName := "Should Fail"
		_, err := updateDatasource(ctx, UpdateDatasourceParams{UID: "non-existent-uid", Name: &newName, SchemaReviewed: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}
