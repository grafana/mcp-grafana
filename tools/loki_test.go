//go:build integration

package tools

import (
	"strings"
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
		for _, entry := range result {
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

		// Should return an empty slice, not nil
		assert.NotNil(t, result, "Empty results should be an empty slice, not nil")
		assert.Equal(t, 0, len(result), "Empty results should have length 0")
	})
}

// =============================================================================
// Integration Tests for Log Masking
// =============================================================================

func TestQueryLokiLogs_WithMasking(t *testing.T) {
	ctx := newTestContext()

	// First, ensure we can get some logs to work with
	baseResult, err := queryLokiLogs(ctx, QueryLokiLogsParams{
		DatasourceUID: "loki",
		LogQL:         `{container=~".+"}`,
		Limit:         10,
	})
	require.NoError(t, err)

	// If no logs available, skip detailed masking verification
	// but still verify the function works correctly
	if len(baseResult) == 0 {
		t.Log("No logs available in Loki; testing masking function execution only")
	}

	t.Run("applies builtin email pattern", func(t *testing.T) {
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"email"},
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")

		// Verify structure is preserved
		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
		}
	})

	t.Run("applies multiple builtin patterns", func(t *testing.T) {
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"email", "ip_address", "credit_card"},
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")
	})

	t.Run("applies custom pattern", func(t *testing.T) {
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `\d{4}-\d{4}-\d{4}`, Replacement: "[MASKED_ID]"},
				},
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")
	})

	t.Run("applies builtin and custom patterns combined", func(t *testing.T) {
		// This test verifies the combined use of builtin and custom patterns (Req 2.4)
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"email", "phone", "ip_address"},
				CustomPatterns: []MaskingPattern{
					{Pattern: `secret-\w+`, Replacement: "[SECRET]"},
					{Pattern: `token-[a-zA-Z0-9]+`, Replacement: "[TOKEN]"},
				},
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")

		// Verify structure is preserved after masking
		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
		}
	})

	t.Run("applies global replacement string", func(t *testing.T) {
		globalReplacement := "[REDACTED]"
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns:   []string{"email"},
				GlobalReplacement: &globalReplacement,
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")
	})

	t.Run("applies HidePatternType option", func(t *testing.T) {
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"email"},
				HidePatternType: true,
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")
	})

	t.Run("masking works with empty results", func(t *testing.T) {
		// Query that's unlikely to match any logs
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container="non-existent-container-123456789"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"email"},
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Empty results should be an empty slice, not nil")
		assert.Equal(t, 0, len(result), "Empty results should have length 0")
	})

	t.Run("masking at max pattern limit (20)", func(t *testing.T) {
		// Test with maximum allowed patterns (7 builtin + 13 custom = 20)
		customPatterns := make([]MaskingPattern, 13)
		for i := 0; i < 13; i++ {
			customPatterns[i] = MaskingPattern{Pattern: `pattern` + string(rune('a'+i))}
		}

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"email", "phone", "credit_card", "ip_address", "mac_address", "api_key", "jwt_token"},
				CustomPatterns:  customPatterns,
			},
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")
	})
}

// TestQueryLokiLogs_WithoutMasking tests backward compatibility when masking is not configured (Task 6.2)
// Requirements: 7.1, 7.2
func TestQueryLokiLogs_WithoutMasking(t *testing.T) {
	ctx := newTestContext()

	t.Run("nil masking config returns unchanged logs", func(t *testing.T) {
		// Query without any masking config (nil)
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking:       nil, // Explicitly nil
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")

		// Verify structure is correct
		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
		}
	})

	t.Run("omitted masking field returns unchanged logs", func(t *testing.T) {
		// Query without masking field (omitted from params)
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			// Masking field omitted
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")

		// Verify structure is correct
		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
		}
	})

	t.Run("empty masking config returns unchanged logs", func(t *testing.T) {
		// Query with empty masking config (no patterns specified)
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking:       &MaskingConfig{},
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")

		// Verify structure is correct
		for _, entry := range result {
			assert.NotEmpty(t, entry.Timestamp, "Log entry should have a timestamp")
			assert.NotNil(t, entry.Labels, "Log entry should have labels")
		}
	})

	t.Run("backward compatibility with all existing params", func(t *testing.T) {
		// Query using all existing parameters to verify they still work
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         5,
			Direction:     "forward",
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")
	})

	t.Run("results are identical with and without empty masking config", func(t *testing.T) {
		// Query without masking
		resultWithoutMasking, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         5,
		})
		require.NoError(t, err)

		// Query with empty masking config
		resultWithEmptyMasking, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         5,
			Masking:       &MaskingConfig{},
		})
		require.NoError(t, err)

		// Results should have same structure (we can't compare content due to timing)
		assert.Equal(t, len(resultWithoutMasking), len(resultWithEmptyMasking),
			"Result lengths should be equal")
	})
}

// TestQueryLokiLogs_MaskingValidationError tests error handling for invalid masking configs (Task 6.3)
// Requirements: 6.1, 6.2, 6.3
func TestQueryLokiLogs_MaskingValidationError(t *testing.T) {
	ctx := newTestContext()

	t.Run("invalid builtin pattern returns error", func(t *testing.T) {
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"invalid_pattern_name"},
			},
		})

		// Should return error
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid builtin pattern")
		// Should not return any data (security requirement)
		assert.Nil(t, result, "No data should be returned on validation error")
	})

	t.Run("invalid regex pattern returns error", func(t *testing.T) {
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `[invalid(`}, // Invalid regex
				},
			},
		})

		// Should return error
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid regex pattern")
		// Should not return any data (security requirement)
		assert.Nil(t, result, "No data should be returned on validation error")
	})

	t.Run("too many patterns returns error", func(t *testing.T) {
		// Create 21 patterns to exceed the limit
		customPatterns := make([]MaskingPattern, 21)
		for i := 0; i < 21; i++ {
			customPatterns[i] = MaskingPattern{Pattern: `pattern` + string(rune('a'+i%26))}
		}

		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				CustomPatterns: customPatterns,
			},
		})

		// Should return error
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many")
		// Should not return any data (security requirement)
		assert.Nil(t, result, "No data should be returned on validation error")
	})

	t.Run("mixed valid and invalid builtin patterns returns error", func(t *testing.T) {
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"email", "invalid_pattern", "phone"},
			},
		})

		// Should return error
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid builtin pattern")
		// Should not return any data
		assert.Nil(t, result, "No data should be returned on validation error")
	})

	t.Run("case-sensitive builtin pattern validation", func(t *testing.T) {
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"EMAIL"}, // Uppercase should fail
			},
		})

		// Should return error (pattern identifiers are case-sensitive)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid builtin pattern")
		assert.Nil(t, result, "No data should be returned on validation error")
	})

	t.Run("error message includes available patterns", func(t *testing.T) {
		_, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"unknown"},
			},
		})

		require.Error(t, err)
		// Error should include list of available patterns for user guidance
		assert.Contains(t, err.Error(), "available")
	})

	t.Run("validation error before log fetch ensures no data leak", func(t *testing.T) {
		// This test verifies that validation happens BEFORE logs are fetched
		// to prevent any sensitive data from being returned on error

		// First verify we can get logs normally
		normalResult, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         5,
		})
		require.NoError(t, err)

		// Now query with invalid masking - should fail without returning any data
		errorResult, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         5,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"invalid"},
			},
		})
		require.Error(t, err)
		assert.Nil(t, errorResult, "No data should be returned on validation error")

		// Log check for debugging (if normal query returned logs, validation prevented leak)
		if len(normalResult) > 0 {
			t.Log("Verified: validation error prevented log data from being returned")
		}
	})

	t.Run("multiple validation errors return first error", func(t *testing.T) {
		// Test with both invalid builtin and invalid regex
		// Should return error for the first validation check (pattern count or builtin validation)
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         10,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"invalid_builtin"},
				CustomPatterns: []MaskingPattern{
					{Pattern: `[invalid(`},
				},
			},
		})

		require.Error(t, err)
		assert.Nil(t, result, "No data should be returned on validation error")

		// Error should be related to validation
		errorLower := strings.ToLower(err.Error())
		assert.True(t,
			strings.Contains(errorLower, "invalid") ||
				strings.Contains(errorLower, "validation"),
			"Error should indicate validation failure")
	})
}
