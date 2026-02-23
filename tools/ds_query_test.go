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

func TestQueryDataSourceTools(t *testing.T) {
	t.Run("query prometheus datasource - instant query", func(t *testing.T) {
		ctx := newTestContext()
		params := QueryDataSourceParams{
			Queries: []DataSourceQuery{
				{
					RefID: "A",
					Datasource: DataSourceRef{
						Type: "prometheus",
						UID:  "prometheus",
					},
					Expr:      "up",
					QueryType: "instant",
				},
			},
			From: "now-1h",
			To:   "now",
		}

		result, err := dsQuery(ctx, params)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify the result structure
		resultMap, ok := result.(map[string]interface{})
		require.True(t, ok, "Result should be a map")

		// Check if results array exists
		results, exists := resultMap["results"]
		require.True(t, exists, "Results field should exist")
		require.NotNil(t, results, "Results should not be nil")
	})

	t.Run("query with invalid datasource UID", func(t *testing.T) {
		ctx := newTestContext()
		params := QueryDataSourceParams{
			Queries: []DataSourceQuery{
				{
					RefID: "A",
					Datasource: DataSourceRef{
						Type: "prometheus",
						UID:  "non-existent-datasource",
					},
					Expr:      "up",
					QueryType: "instant",
				},
			},
			From: "now-1h",
			To:   "now",
		}

		result, err := dsQuery(ctx, params)
		require.Error(t, err)
		require.Nil(t, result)
		assert.Contains(t, err.Error(), "HTTP error", "Should contain HTTP error message")
	})
}
