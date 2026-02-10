//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLokiTools(t *testing.T) {
	t.Run("list loki label names", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listLokiLabelNames(ctx, ListLokiLabelNamesParams{
			DatasourceUID: "loki",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result, "Should have at least one label name")
	})

	t.Run("get loki label values", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listLokiLabelValues(ctx, ListLokiLabelValuesParams{
			DatasourceUID: "loki",
			LabelName:     "container",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result, "Should have at least one container label value")
	})

	t.Run("query loki stats", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryLokiStats(ctx, QueryLokiStatsParams{
			DatasourceUID: "loki",
			LogQL:         `{container="grafana"}`,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result")

		// We can't assert on specific values as they will vary,
		// but we can check that the structure is correct
		assert.GreaterOrEqual(t, result.Streams, 0, "Should have a valid streams count")
		assert.GreaterOrEqual(t, result.Chunks, 0, "Should have a valid chunks count")
		assert.GreaterOrEqual(t, result.Entries, 0, "Should have a valid entries count")
		assert.GreaterOrEqual(t, result.Bytes, 0, "Should have a valid bytes count")
	})

	t.Run("query loki logs", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
		})
		require.NoError(t, err)

		// We can't assert on specific log content as it will vary,
		// but we can check that the structure is correct
		// If we got logs, check that they have the expected structure
		for _, entry := range result.Data {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
		}
	})

	t.Run("query loki logs with no results", func(t *testing.T) {
		ctx := newTestContext()
		// Use a query that's unlikely to match any logs
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container="non-existent-container-name-123456789"}`,
			Limit:         10,
		})
		require.NoError(t, err)

		// Should return an empty result, not nil
		assert.NotNil(t, result, "Result should not be nil")
		assert.Equal(t, 0, len(result.Data), "Empty results should have length 0")
	})

	t.Run("query loki patterns", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryLokiPatterns(ctx, QueryLokiPatternsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result (may be empty if no patterns detected)")

		// If we got patterns, check that they have the expected structure
		for _, pattern := range result {
			assert.NotEmpty(t, pattern.Pattern, "Pattern should have a pattern string")
			// TotalCount should be non-negative
			assert.GreaterOrEqual(t, pattern.TotalCount, int64(0), "TotalCount should be non-negative")
		}
	})

	t.Run("query loki metrics instant", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `sum by(container) (count_over_time({container=~".+"}[5m]))`,
			QueryType:     "instant",
		})
		require.NoError(t, err)
		// Instant metric queries may return empty results if no data matches
		assert.NotNil(t, result, "Result should not be nil")

		// If we got results, verify the structure
		for _, entry := range result.Data {
			assert.NotNil(t, entry.Labels, "Metric sample should have labels")
			assert.NotNil(t, entry.Value, "Instant metric should have a single value")
			assert.Nil(t, entry.Values, "Instant metric should not have Values array")
			assert.Empty(t, entry.Line, "Metric query should not have log line")
		}
	})

	t.Run("query loki metrics range", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `sum by(container) (count_over_time({container=~".+"}[5m]))`,
			QueryType:     "range",
			StepSeconds:   60,
		})
		require.NoError(t, err)
		// Range metric queries may return empty results if no data matches
		assert.NotNil(t, result, "Result should not be nil")

		// If we got results, verify the structure
		for _, entry := range result.Data {
			assert.NotNil(t, entry.Labels, "Metric series should have labels")
			assert.NotEmpty(t, entry.Values, "Range metric should have Values array")
			assert.Nil(t, entry.Value, "Range metric should not have single Value")
			assert.Empty(t, entry.Line, "Metric query should not have log line")

			// Verify each metric value has timestamp and value
			for _, mv := range entry.Values {
				assert.NotEmpty(t, mv.Timestamp, "Metric value should have timestamp")
				// Value can be 0, so we don't assert on its value
			}
		}
	})

	t.Run("query loki logs backward compatibility", func(t *testing.T) {
		// Test that existing queries without queryType still work (default to range)
		ctx := newTestContext()
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         5,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")

		// Verify log entries have expected structure
		for _, entry := range result.Data {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have timestamp")
			assert.NotEmpty(t, entry.Line, "Log entry should have log line")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
			assert.Nil(t, entry.Value, "Log entry should not have metric value")
			assert.Nil(t, entry.Values, "Log entry should not have metric values array")
		}
	})
}
