package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLokiClientFromContext(t *testing.T) {
	ctx := context.Background()
	client, url, err := lokiClientFromContext(ctx, "loki")
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Contains(t, url, "/api/datasources/proxy/uid/loki")
}

func TestQueryLoki(t *testing.T) {
	// This is a mock test since we can't actually query Loki in unit tests
	ctx := context.Background()
	result, err := queryLoki(ctx, QueryLokiParams{
		DatasourceUID: "loki",
		Query:         `{app="test"}`,
		StartRFC3339:  "2023-01-01T00:00:00Z",
	})

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "success", result["status"])
}

func TestListLokiLabelNames(t *testing.T) {
	// This is a mock test since we can't actually query Loki in unit tests
	ctx := context.Background()
	result, err := listLokiLabelNames(ctx, ListLokiLabelNamesParams{
		DatasourceUID: "loki",
	})

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Contains(t, result, "app")
	assert.Contains(t, result, "job")
}

func TestListLokiLabelValues(t *testing.T) {
	// This is a mock test since we can't actually query Loki in unit tests
	ctx := context.Background()
	result, err := listLokiLabelValues(ctx, ListLokiLabelValuesParams{
		DatasourceUID: "loki",
		LabelName:     "app",
	})

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Contains(t, result, "value1")
}

// TestLokiTools tests all Loki tools together in a single test function
// This follows the structure of TestPrometheusTools
func TestLokiTools(t *testing.T) {
	t.Run("query loki", func(t *testing.T) {
		// This is a mock test since we can't actually query Loki in unit tests
		ctx := context.Background()
		result, err := queryLoki(ctx, QueryLokiParams{
			DatasourceUID: "loki",
			Query:         `{app="test"}`,
			StartRFC3339:  "2023-01-01T00:00:00Z",
			EndRFC3339:    "2023-01-02T00:00:00Z",
			Limit:         100,
			Direction:     "backward",
		})

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "success", result["status"])

		data, ok := result["data"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "streams", data["resultType"])
	})

	t.Run("list loki label names", func(t *testing.T) {
		// This is a mock test since we can't actually query Loki in unit tests
		ctx := context.Background()
		result, err := listLokiLabelNames(ctx, ListLokiLabelNamesParams{
			DatasourceUID: "loki",
			StartRFC3339:  "2023-01-01T00:00:00Z",
			EndRFC3339:    "2023-01-02T00:00:00Z",
		})

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Contains(t, result, "app")
		assert.Contains(t, result, "job")
		assert.Contains(t, result, "level")
		assert.Contains(t, result, "container")
	})

	t.Run("list loki label values", func(t *testing.T) {
		// This is a mock test since we can't actually query Loki in unit tests
		ctx := context.Background()
		result, err := listLokiLabelValues(ctx, ListLokiLabelValuesParams{
			DatasourceUID: "loki",
			LabelName:     "app",
			StartRFC3339:  "2023-01-01T00:00:00Z",
			EndRFC3339:    "2023-01-02T00:00:00Z",
		})

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Contains(t, result, "value1")
		assert.Contains(t, result, "value2")
		assert.Contains(t, result, "value3")
	})
}
