// Requires a Grafana instance running on localhost:3000,
// with a Prometheus datasource provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"encoding/json"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
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

func TestCreateDatasourceTools(t *testing.T) {
	t.Run("create datasource", func(t *testing.T) {
		ctx := newTestContext()

		toolResult, err := createDatasource(ctx, CreateDatasourceParams{
			Name: "mcp-test-prometheus",
			Type: "prometheus",
			URL:  "http://prometheus:9090",
		})
		require.NoError(t, err)
		require.NotNil(t, toolResult)
		assert.False(t, toolResult.IsError)

		require.Len(t, toolResult.Content, 1)
		text, ok := toolResult.Content[0].(mcp.TextContent)
		require.True(t, ok)

		var result CreateDatasourceResult
		require.NoError(t, json.Unmarshal([]byte(text.Text), &result))
		assert.Equal(t, "mcp-test-prometheus", result.Name)
		assert.NotEmpty(t, result.UID)

		c := mcpgrafana.GrafanaClientFromContext(ctx)
		t.Cleanup(func() {
			_, _ = c.Datasources.DeleteDataSourceByUID(result.UID)
		})
	})

	t.Run("create datasource - basicAuth blocked", func(t *testing.T) {
		ctx := newTestContext()

		toolResult, err := createDatasource(ctx, CreateDatasourceParams{
			Name:      "should-not-be-created",
			Type:      "prometheus",
			BasicAuth: true,
		})
		require.NoError(t, err)
		require.NotNil(t, toolResult)
		assert.True(t, toolResult.IsError)

		text, ok := toolResult.Content[0].(mcp.TextContent)
		require.True(t, ok)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(text.Text), &payload))
		assert.Equal(t, "credential_policy_redirect", payload["outcome"])
		assert.Equal(t, "basic_auth_enabled_via_mcp_disallowed", payload["reason"])
	})

	t.Run("create datasource - secureJsonData blocked", func(t *testing.T) {
		ctx := newTestContext()

		toolResult, err := createDatasource(ctx, CreateDatasourceParams{
			Name:           "should-not-be-created",
			Type:           "prometheus",
			SecureJSONData: map[string]string{"password": "s3cr3t"},
		})
		require.NoError(t, err)
		require.NotNil(t, toolResult)
		assert.True(t, toolResult.IsError)

		text, ok := toolResult.Content[0].(mcp.TextContent)
		require.True(t, ok)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(text.Text), &payload))
		assert.Equal(t, "credential_policy_redirect", payload["outcome"])
		assert.Equal(t, "secure_json_data_found", payload["reason"])
	})
}
