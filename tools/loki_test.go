//go:build integration

package tools

import (
	"strings"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
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

	t.Run("query loki logs backward compatibility - no masker in context", func(t *testing.T) {
		ctx := newTestContext()

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
		})
		require.NoError(t, err)

		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
			assert.NotContains(t, entry.Line, "[MASKED:", "Logs should not be masked when no masker in context")
		}
	})

	t.Run("query loki logs backward compatibility - nil masker explicitly set", func(t *testing.T) {
		ctx := newTestContext()
		ctx = mcpgrafana.WithMasker(ctx, nil)

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
		})
		require.NoError(t, err)

		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
			assert.NotContains(t, entry.Line, "[MASKED:", "Logs should not be masked when nil masker in context")
		}
	})

	t.Run("query loki logs backward compatibility - query returns same structure", func(t *testing.T) {
		ctx := newTestContext()

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         5,
			Direction:     "backward",
		})
		require.NoError(t, err)

		if len(result) > 0 {
			entry := result[0]
			assert.NotEmpty(t, entry.Timestamp, "Timestamp should be set")
			assert.NotNil(t, entry.Labels, "Labels should not be nil")
			assert.Nil(t, entry.Value, "Value should be nil for log queries")
		}
	})

	t.Run("query loki logs with masker in context - single pattern", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"ip_address"},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		ctx := newTestContext()
		ctx = mcpgrafana.WithMasker(ctx, masker)

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
		})
		require.NoError(t, err)

		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
		}
	})

	t.Run("query loki logs with masker in context - multiple patterns", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email", "ip_address", "mac_address"},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)
		assert.Equal(t, 3, masker.PatternCount(), "Should have 3 patterns configured")

		ctx := newTestContext()
		ctx = mcpgrafana.WithMasker(ctx, masker)

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
		})
		require.NoError(t, err)

		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
		}
	})

	t.Run("query loki logs with masker in context - all builtin patterns", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{
				"email", "phone", "credit_card", "ip_address",
				"mac_address", "api_key", "jwt_token",
			},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)
		assert.Equal(t, 7, masker.PatternCount(), "Should have all 7 builtin patterns")

		ctx := newTestContext()
		ctx = mcpgrafana.WithMasker(ctx, masker)

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
		})
		require.NoError(t, err)

		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
		}
	})

	t.Run("query loki logs with masker - preserves entry fields", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"ip_address"},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		ctx := newTestContext()
		ctx = mcpgrafana.WithMasker(ctx, masker)

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         5,
		})
		require.NoError(t, err)

		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Timestamp should be preserved")
			assert.NotNil(t, entry.Labels, "Labels should be preserved")
			assert.Nil(t, entry.Value, "Value should remain nil for log queries")
		}
	})

	t.Run("query loki logs with masker - empty entries handled", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		ctx := newTestContext()
		ctx = mcpgrafana.WithMasker(ctx, masker)

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container="non-existent-masking-test-12345"}`,
			Limit:         10,
		})
		require.NoError(t, err)

		assert.NotNil(t, result, "Empty results should be an empty slice, not nil")
		assert.Equal(t, 0, len(result), "Empty results should have length 0")
	})

	t.Run("query loki logs with masker - masking format validation", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"ip_address"},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		ctx := newTestContext()
		ctx = mcpgrafana.WithMasker(ctx, masker)

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         50,
		})
		require.NoError(t, err)

		// If any entry contains [MASKED:, verify the format is valid
		for _, entry := range result {
			if strings.Contains(entry.Line, "[MASKED:") {
				assert.NotContains(t, entry.Line, "[MASKED:]", "Should not have empty pattern type")
				assert.NotContains(t, entry.Line, "[MASKED: ", "Should not have trailing space in mask")
			}
		}
	})
}
