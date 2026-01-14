//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestElasticsearchTools(t *testing.T) {
	t.Run("query elasticsearch with simple query", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "logs-*",
			Query:         "*",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result")

		// Check structure of returned documents
		for _, doc := range result {
			assert.NotEmpty(t, doc.Index, "Document should have an index")
			assert.NotEmpty(t, doc.ID, "Document should have an ID")
			assert.NotNil(t, doc.Source, "Document should have a source")
		}
	})

	t.Run("query elasticsearch with time range", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "logs-*",
			Query:         "*",
			StartTime:     "2024-01-01T00:00:00Z",
			EndTime:       "2024-12-31T23:59:59Z",
			Limit:         5,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result")
	})

	t.Run("query elasticsearch with lucene query", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "logs-*",
			Query:         "status:200",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Should return a result")
	})

	t.Run("query elasticsearch with no results", func(t *testing.T) {
		ctx := newTestContext()
		// Use a query that's unlikely to match any documents
		result, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "elasticsearch",
			Index:         "logs-*",
			Query:         "nonexistent_field:nonexistent_value_123456789",
			Limit:         10,
		})
		require.NoError(t, err)

		// Should return an empty slice, not nil
		assert.NotNil(t, result, "Empty results should be an empty slice, not nil")
		assert.Equal(t, 0, len(result), "Empty results should have length 0")
	})

	t.Run("query elasticsearch with invalid datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := queryElasticsearch(ctx, QueryElasticsearchParams{
			DatasourceUID: "non-existent-datasource",
			Index:         "logs-*",
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
			Index:         "logs-*",
			Query:         "*",
			Limit:         limit,
		})
		require.NoError(t, err)
		assert.LessOrEqual(t, len(result), limit, "Should not exceed requested limit")
	})
}
