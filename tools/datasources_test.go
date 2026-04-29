// Requires a Grafana instance running on localhost:3000,
// with a Prometheus datasource provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
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
		assert.Equal(t, result.Total, result.Healthy+result.Unhealthy)
		assert.Len(t, result.Results, result.Total)
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

	t.Run("update name", func(t *testing.T) {
		newName := "Integration Test DS Updated"
		result, err := updateDatasource(ctx, UpdateDatasourceParams{UID: testUID, Name: &newName})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.NotNil(t, result.Datasource)
		assert.Equal(t, newName, result.Datasource.Name)
	})

	t.Run("update url", func(t *testing.T) {
		newURL := "http://prometheus:9090"
		result, err := updateDatasource(ctx, UpdateDatasourceParams{UID: testUID, URL: &newURL})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, newURL, result.Datasource.URL)
	})

	t.Run("health check included in result", func(t *testing.T) {
		newName := "Integration Test DS Health"
		result, err := updateDatasource(ctx, UpdateDatasourceParams{UID: testUID, Name: &newName})
		require.NoError(t, err)
		require.NotNil(t, result.Health)
		assert.Equal(t, testUID, result.Health.UID)
		assert.NotEmpty(t, result.Health.Message)
	})

	t.Run("update non-existent datasource returns error", func(t *testing.T) {
		newName := "Should Fail"
		_, err := updateDatasource(ctx, UpdateDatasourceParams{UID: "non-existent-uid", Name: &newName})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}
