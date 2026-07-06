//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const graphiteTestDatasourceUID = "graphite"

func TestGraphiteIntegration_ListMetrics(t *testing.T) {
	t.Run("list top-level metrics", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listGraphiteMetrics(ctx, ListGraphiteMetricsParams{
			DatasourceUID: graphiteTestDatasourceUID,
			Query:         "*",
		})
		require.NoError(t, err)
		require.NotEmpty(t, result, "should return at least one top-level node")

		// Verify node structure.
		for _, node := range result {
			assert.NotEmpty(t, node.ID, "node should have an ID")
			assert.NotEmpty(t, node.Text, "node should have a text")
		}

		// The seeded metrics all live under "test.*".
		ids := make(map[string]bool, len(result))
		for _, n := range result {
			ids[n.ID] = true
		}
		assert.True(t, ids["test"], "top-level 'test' node should be present")
	})

	t.Run("list second-level metrics", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listGraphiteMetrics(ctx, ListGraphiteMetricsParams{
			DatasourceUID: graphiteTestDatasourceUID,
			Query:         "test.*",
		})
		require.NoError(t, err)
		require.NotEmpty(t, result, "should return at least one node under 'test'")

		ids := make(map[string]bool, len(result))
		for _, n := range result {
			ids[n.ID] = true
		}
		assert.True(t, ids["test.servers"], "'test.servers' node should be present")
	})

	t.Run("list leaf metrics", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listGraphiteMetrics(ctx, ListGraphiteMetricsParams{
			DatasourceUID: graphiteTestDatasourceUID,
			Query:         "test.servers.web01.cpu.*",
		})
		require.NoError(t, err)
		require.NotEmpty(t, result, "should return leaf metrics under 'test.servers.web01.cpu'")

		// All returned nodes should be leaves.
		for _, node := range result {
			assert.True(t, node.Leaf, "node %q should be a leaf", node.ID)
		}
	})
}

func TestGraphiteIntegration_QueryGraphite(t *testing.T) {
	t.Run("query returns series with data", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryGraphite(ctx, QueryGraphiteParams{
			DatasourceUID: graphiteTestDatasourceUID,
			Target:        "test.servers.web01.cpu.load5",
			From:          "-1h",
			Until:         "now",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Series, "should return at least one series")
		assert.Nil(t, result.Hints, "hints should be absent when data is returned")

		series := result.Series[0]
		assert.Equal(t, "test.servers.web01.cpu.load5", series.Target)
		assert.NotEmpty(t, series.Datapoints, "series should have datapoints")
	})

	t.Run("query with wildcard returns multiple series", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryGraphite(ctx, QueryGraphiteParams{
			DatasourceUID: graphiteTestDatasourceUID,
			Target:        "test.servers.*.cpu.load5",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.GreaterOrEqual(t, len(result.Series), 2, "wildcard should match multiple servers")
	})

	t.Run("query with no matching target returns hints", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryGraphite(ctx, QueryGraphiteParams{
			DatasourceUID: graphiteTestDatasourceUID,
			Target:        "test.nonexistent.metric.xyz",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Empty(t, result.Series, "non-matching target should return no series")
		assert.NotNil(t, result.Hints, "hints should be present for empty results")
	})
}

func TestGraphiteIntegration_QueryGraphiteDensity(t *testing.T) {
	t.Run("density for specific series", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryGraphiteDensity(ctx, QueryGraphiteDensityParams{
			DatasourceUID: graphiteTestDatasourceUID,
			Target:        "test.servers.web01.cpu.load5",
			From:          "-1h",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Series, "should return density for the seeded series")

		density := result.Series[0]
		assert.Equal(t, "test.servers.web01.cpu.load5", density.Target)
		assert.Greater(t, density.TotalPoints, 0, "should have datapoints in the window")
		assert.GreaterOrEqual(t, density.FillRatio, 0.0, "fill ratio should be non-negative")
		assert.LessOrEqual(t, density.FillRatio, 1.0, "fill ratio should not exceed 1.0")
		assert.NotNil(t, density.LastSeen, "lastSeen should be set since we seeded data")
	})

	t.Run("density for wildcard matches multiple series", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryGraphiteDensity(ctx, QueryGraphiteDensityParams{
			DatasourceUID: graphiteTestDatasourceUID,
			Target:        "test.servers.*.cpu.load5",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.GreaterOrEqual(t, len(result.Series), 2, "wildcard should match multiple series")
	})
}

func TestGraphiteIntegration_ListTags(t *testing.T) {
	t.Run("list tags returns without error", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listGraphiteTags(ctx, ListGraphiteTagsParams{
			DatasourceUID: graphiteTestDatasourceUID,
		})
		require.NoError(t, err)
		// Tags may or may not be present depending on whether Graphite's tag
		// support has indexed the seeded tagged metrics yet. We only assert that
		// the call succeeds and returns a non-nil slice.
		assert.NotNil(t, result)
	})

	t.Run("list tags with prefix filter", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listGraphiteTags(ctx, ListGraphiteTagsParams{
			DatasourceUID: graphiteTestDatasourceUID,
			Prefix:        "server",
		})
		require.NoError(t, err)
		// All returned tags must start with the requested prefix.
		for _, tag := range result {
			assert.Contains(t, tag, "server", "all tags should match the prefix filter")
		}
	})
}
