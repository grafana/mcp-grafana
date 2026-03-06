//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVictoriaLogsTools(t *testing.T) {
	t.Run("list victorialogs field names", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listVictoriaLogsFieldNames(ctx, ListVictoriaLogsFieldNamesParams{
			DatasourceUID: "victorialogs",
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result (may be empty if no data)")
	})

	t.Run("list victorialogs field values", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listVictoriaLogsFieldValues(ctx, ListVictoriaLogsFieldValuesParams{
			DatasourceUID: "victorialogs",
			FieldName:     "_msg",
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result (may be empty if no data)")

		// If we got results, check that they have the expected structure
		for _, entry := range result {
			assert.NotEmpty(t, entry.Value, "Field value should not be empty")
			assert.GreaterOrEqual(t, entry.Hits, int64(0), "Hits should be non-negative")
		}
	})

	t.Run("query victorialogs", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryVictoriaLogs(ctx, QueryVictoriaLogsParams{
			DatasourceUID: "victorialogs",
			Query:         "*",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")

		// If we got results, check that they have the expected structure
		for _, entry := range result.Data {
			assert.NotNil(t, entry, "Log entry should not be nil")
		}
	})

	t.Run("query victorialogs with no results", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryVictoriaLogs(ctx, QueryVictoriaLogsParams{
			DatasourceUID: "victorialogs",
			Query:         `non_existent_field_xyz123:"impossible_value_abc789"`,
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")
		assert.Equal(t, 0, len(result.Data), "Empty results should have length 0")
	})

	t.Run("query victorialogs hits", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryVictoriaLogsHits(ctx, QueryVictoriaLogsHitsParams{
			DatasourceUID: "victorialogs",
			Query:         "*",
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")

		// If we got results, check that they have the expected structure
		for _, entry := range result.Data {
			assert.GreaterOrEqual(t, entry.Total, int64(0), "Total should be non-negative")
			assert.NotNil(t, entry.Timestamps, "Timestamps should not be nil")
			assert.NotNil(t, entry.Values, "Values should not be nil")
			assert.Equal(t, len(entry.Timestamps), len(entry.Values), "Timestamps and Values should have same length")
		}
	})

	t.Run("query victorialogs streams", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryVictoriaLogsStreams(ctx, QueryVictoriaLogsStreamsParams{
			DatasourceUID: "victorialogs",
			Query:         "*",
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")

		// If we got results, check that they have the expected structure
		for _, entry := range result.Data {
			assert.GreaterOrEqual(t, entry.Hits, int64(0), "Hits should be non-negative")
		}
	})
}
