//go:build integration

package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPyroscopeTools(t *testing.T) {
	t.Run("list Pyroscope label names", func(t *testing.T) {
		ctx := newTestContext()
		names, err := listPyroscopeLabelNames(ctx, ListPyroscopeLabelNamesParams{
			DataSourceUID: "pyroscope",
			Matchers:      `{service_name="pyroscope"}`,
		})
		require.NoError(t, err)
		require.ElementsMatch(t, names, []string{
			"__name__",
			"__period_type__",
			"__period_unit__",
			"__profile_type__",
			"__service_name__",
			"__type__",
			"__unit__",
			"hostname",
			"pyroscope_spy",
			"service_git_ref",
			"service_name",
			"service_repository",
			"target",
		})
	})

	t.Run("get Pyroscope label values", func(t *testing.T) {
		ctx := newTestContext()
		values, err := listPyroscopeLabelValues(ctx, ListPyroscopeLabelValuesParams{
			DataSourceUID: "pyroscope",
			Name:          "target",
			Matchers:      `{service_name="pyroscope"}`,
		})
		require.NoError(t, err)
		require.ElementsMatch(t, values, []string{"all"})
	})

	t.Run("get Pyroscope profile types", func(t *testing.T) {
		ctx := newTestContext()
		types, err := listPyroscopeProfileTypes(ctx, ListPyroscopeProfileTypesParams{
			DataSourceUID: "pyroscope",
		})
		require.NoError(t, err)
		require.ElementsMatch(t, types, []string{
			"block:contentions:count:contentions:count",
			"block:delay:nanoseconds:contentions:count",
			"goroutines:goroutine:count:goroutine:count",
			"memory:alloc_objects:count:space:bytes",
			"memory:alloc_space:bytes:space:bytes",
			"memory:inuse_objects:count:space:bytes",
			"memory:inuse_space:bytes:space:bytes",
			"mutex:contentions:count:contentions:count",
			"mutex:delay:nanoseconds:contentions:count",
			"process_cpu:cpu:nanoseconds:cpu:nanoseconds",
			"process_cpu:samples:count:cpu:nanoseconds",
		})
	})

	t.Run("query Pyroscope both", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryPyroscope(ctx, QueryPyroscopeParams{
			DataSourceUID: "pyroscope",
			ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
			Matchers:      `{service_name="pyroscope"}`,
			QueryType:     "both",
		})
		require.NoError(t, err)
		require.NotEmpty(t, result)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))
		assert.Equal(t, "both", parsed["query_type"])
		assert.NotNil(t, parsed["profile"], "profile should be present")
		assert.NotNil(t, parsed["metrics"], "metrics should be present")
	})

	t.Run("query Pyroscope profile only", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryPyroscope(ctx, QueryPyroscopeParams{
			DataSourceUID: "pyroscope",
			ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
			Matchers:      `{service_name="pyroscope"}`,
			QueryType:     "profile",
		})
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))
		assert.Equal(t, "profile", parsed["query_type"])
		assert.NotNil(t, parsed["profile"])
		assert.Nil(t, parsed["metrics"], "metrics should not be present for profile-only")
	})

	t.Run("query Pyroscope metrics only", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryPyroscope(ctx, QueryPyroscopeParams{
			DataSourceUID: "pyroscope",
			ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
			Matchers:      `{service_name="pyroscope"}`,
			QueryType:     "metrics",
		})
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))
		assert.Equal(t, "metrics", parsed["query_type"])
		assert.Nil(t, parsed["profile"], "profile should not be present for metrics-only")
		assert.NotNil(t, parsed["metrics"])
	})
}
