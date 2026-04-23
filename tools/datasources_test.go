// Requires a Grafana instance running on localhost:3000,
// with a Prometheus datasource provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

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
