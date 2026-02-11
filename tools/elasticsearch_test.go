//go:build integration

package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestElasticsearchTools(t *testing.T) {
	t.Run("query elasticsearch with match all", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "test-logs-*",
			Query:         "*",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result")
		assert.NotEmpty(t, result, "Should return at least one document from seeded data")

		// Verify document structure from seeded data
		for _, doc := range result {
			assert.NotEmpty(t, doc.Index, "Document should have an index")
			assert.NotEmpty(t, doc.ID, "Document should have an ID")
			assert.NotNil(t, doc.Source, "Document should have source fields")

			// Seeded documents should have these fields
			assert.Contains(t, doc.Source, "message", "Document should have a message field")
			assert.Contains(t, doc.Source, "level", "Document should have a level field")
			assert.Contains(t, doc.Source, "service", "Document should have a service field")
		}
	})

	t.Run("query elasticsearch with time range", func(t *testing.T) {
		ctx := newTestContext()
		now := time.Now().UTC()
		startTime := now.Add(-1 * time.Hour).Format(time.RFC3339)
		endTime := now.Add(1 * time.Hour).Format(time.RFC3339)

		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "test-logs-*",
			Query:         "*",
			StartTime:     startTime,
			EndTime:       endTime,
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result")
		assert.NotEmpty(t, result, "Should return documents within the time range")
	})

	t.Run("query elasticsearch with time range no results", func(t *testing.T) {
		ctx := newTestContext()
		// Use a time range far in the past that won't match any seeded data
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "test-logs-*",
			Query:         "*",
			StartTime:     "2020-01-01T00:00:00Z",
			EndTime:       "2020-01-02T00:00:00Z",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Empty results should be an empty slice, not nil")
		assert.Empty(t, result, "Should return no documents for old time range")
	})

	t.Run("query elasticsearch with lucene query", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "test-logs-*",
			Query:         "level:error",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result")
		assert.NotEmpty(t, result, "Should find error-level log entries")

		for _, doc := range result {
			level, ok := doc.Source["level"].(string)
			assert.True(t, ok, "level field should be a string")
			assert.Equal(t, "error", level, "All results should have level=error")
		}
	})

	t.Run("query elasticsearch with service filter", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "test-logs-*",
			Query:         "service:api-gateway",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result")
		assert.NotEmpty(t, result, "Should find api-gateway log entries")

		for _, doc := range result {
			service, ok := doc.Source["service"].(string)
			assert.True(t, ok, "service field should be a string")
			assert.Equal(t, "api-gateway", service, "All results should have service=api-gateway")
		}
	})

	t.Run("query elasticsearch with no results", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "test-logs-*",
			Query:         "nonexistent_field:nonexistent_value_123456789",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Empty results should be an empty slice, not nil")
		assert.Empty(t, result, "Should return no documents")
	})

	t.Run("query elasticsearch with nonexistent index", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "nonexistent-index-*",
			Query:         "*",
			Limit:         10,
		})
		// Querying a nonexistent index may return an error or empty results
		// depending on ES configuration; either is acceptable
		if err == nil {
			assert.NotNil(t, result)
			assert.Empty(t, result)
		}
	})

	t.Run("query elasticsearch with invalid datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "non-existent-datasource",
			Index:         "test-logs-*",
			Query:         "*",
			Limit:         10,
		})
		require.Error(t, err, "Should return error for invalid datasource")
	})

	t.Run("query elasticsearch respects limit", func(t *testing.T) {
		ctx := newTestContext()
		limit := 3
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "test-logs-*",
			Query:         "*",
			Limit:         limit,
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.LessOrEqual(t, len(result), limit, "Should not exceed requested limit")
	})

	t.Run("query elasticsearch with query dsl json", func(t *testing.T) {
		ctx := newTestContext()
		// Use Query DSL JSON to search for error-level logs
		queryDSL := `{"bool":{"must":[{"term":{"level":"error"}}]}}`
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "test-logs-*",
			Query:         queryDSL,
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result")
		assert.NotEmpty(t, result, "Should find error-level documents via Query DSL")

		for _, doc := range result {
			level, ok := doc.Source["level"].(string)
			assert.True(t, ok, "level field should be a string")
			assert.Equal(t, "error", level, "All results should have level=error")
		}
	})

	t.Run("query elasticsearch documents have timestamps", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "test-logs-*",
			Query:         "*",
			Limit:         5,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result, "Should return documents")

		for _, doc := range result {
			assert.NotEmpty(t, doc.Timestamp, "Documents with @timestamp should have Timestamp set")
		}
	})
}
