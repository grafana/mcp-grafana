//go:build integration

package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuickwitTools(t *testing.T) {
	t.Run("query quickwit with match all", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "quickwit",
			Query:         "",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotEmpty(t, result, "Should return at least one document from seeded data")

		for _, doc := range result {
			assert.NotEmpty(t, doc.Index, "Document should have an index")
			assert.NotNil(t, doc.Source, "Document should have source fields")
			assert.Contains(t, doc.Source, "body", "Document should have a body field")
			assert.Contains(t, doc.Source, "severity_text", "Document should have a severity_text field")
			assert.Contains(t, doc.Source, "service_name", "Document should have a service_name field")
		}
	})

	t.Run("query quickwit defaults to configured index", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "quickwit",
			Query:         "",
			Limit:         5,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)
	})

	t.Run("query quickwit with search filter", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "quickwit",
			Index:         "test-logs",
			Query:         `{"term":{"service_name":"api-gateway"}}`,
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result, "Should find api-gateway log entries")

		for _, doc := range result {
			service, ok := doc.Source["service_name"].(string)
			assert.True(t, ok, "service_name field should be a string")
			assert.Equal(t, "api-gateway", service)
		}
	})

	t.Run("query quickwit with lucene filter", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "quickwit",
			Index:         "test-logs",
			Query:         "severity_text:ERROR",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result, "Should find ERROR-level log entries")

		for _, doc := range result {
			severity, ok := doc.Source["severity_text"].(string)
			assert.True(t, ok, "severity_text field should be a string")
			assert.Equal(t, "ERROR", severity)
		}
	})

	t.Run("query quickwit with time range", func(t *testing.T) {
		ctx := newTestContext()
		now := time.Now().UTC()
		startTime := now.Add(-24 * time.Hour).Format(time.RFC3339)
		endTime := now.Add(24 * time.Hour).Format(time.RFC3339)

		result, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "quickwit",
			Index:         "test-logs",
			Query:         "",
			StartTime:     startTime,
			EndTime:       endTime,
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result, "Should return documents within the time range")
	})

	t.Run("query quickwit with time range no results", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "quickwit",
			Index:         "test-logs",
			Query:         "",
			StartTime:     "2020-01-01T00:00:00Z",
			EndTime:       "2020-01-02T00:00:00Z",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Empty(t, result, "Should return no documents for old time range")
	})

	t.Run("query quickwit respects limit", func(t *testing.T) {
		ctx := newTestContext()
		limit := 3
		result, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "quickwit",
			Index:         "test-logs",
			Query:         "",
			Limit:         limit,
		})
		require.NoError(t, err)
		assert.LessOrEqual(t, len(result), limit)
	})

	t.Run("query quickwit documents have timestamps", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "quickwit",
			Index:         "test-logs",
			Query:         "",
			Limit:         5,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		for _, doc := range result {
			assert.NotEmpty(t, doc.Timestamp, "Documents should have Timestamp set")
		}
	})

	t.Run("query quickwit with invalid datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "non-existent-datasource",
			Index:         "test-logs",
			Query:         "",
			Limit:         10,
		})
		require.Error(t, err)
	})

	t.Run("query quickwit with wrong datasource type", func(t *testing.T) {
		ctx := newTestContext()
		_, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "prometheus",
			Index:         "test-logs",
			Query:         "",
			Limit:         10,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not quickwit")
	})

	t.Run("query quickwit with incompatible index", func(t *testing.T) {
		ctx := newTestContext()
		_, err := queryQuickwit(ctx, QueryQuickwitParams{
			DatasourceUID: "quickwit",
			Index:         "other-index",
			Query:         "",
			Limit:         10,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not compatible")
	})
}
