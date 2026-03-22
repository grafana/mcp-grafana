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

	t.Run("fetch Pyroscope profile", func(t *testing.T) {
		ctx := newTestContext()
		profile, err := fetchPyroscopeProfile(ctx, FetchPyroscopeProfileParams{
			DataSourceUID: "pyroscope",
			ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
			Matchers:      `{service_name="pyroscope"}`,
		})
		require.NoError(t, err)
		require.NotEmpty(t, profile)
	})

	t.Run("fetch empty Pyroscope profile", func(t *testing.T) {
		ctx := newTestContext()
		_, err := fetchPyroscopeProfile(ctx, FetchPyroscopeProfileParams{
			DataSourceUID: "pyroscope",
			ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
			Matchers:      `{service_name="pyroscope", label_does_not_exit="missing"}`,
		})
		require.EqualError(t, err, "failed to call Pyroscope API: pyroscope API returned an empty response")
	})

	t.Run("fetch Pyroscope series", func(t *testing.T) {
		ctx := newTestContext()
		result, err := fetchPyroscopeSeries(ctx, FetchPyroscopeSeriesParams{
			DataSourceUID: "pyroscope",
			ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
			Matchers:      `{service_name="pyroscope"}`,
		})
		require.NoError(t, err)
		require.NotEmpty(t, result)

		var parsed struct {
			Series     []seriesSummary   `json:"series"`
			TimeRange  map[string]string `json:"time_range"`
			StepSecs   float64           `json:"step_seconds"`
		}
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))
		assert.NotEmpty(t, parsed.Series)
		assert.NotEmpty(t, parsed.TimeRange["from"])
		assert.NotEmpty(t, parsed.TimeRange["to"])
		assert.Greater(t, parsed.StepSecs, 0.0)
	})

	t.Run("fetch Pyroscope series empty", func(t *testing.T) {
		ctx := newTestContext()
		result, err := fetchPyroscopeSeries(ctx, FetchPyroscopeSeriesParams{
			DataSourceUID: "pyroscope",
			ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
			Matchers:      `{service_name="pyroscope", label_does_not_exit="missing"}`,
		})
		require.NoError(t, err)
		assert.Equal(t, "No series data returned for the given query and time range.", result)
	})

	t.Run("fetch Pyroscope unified both", func(t *testing.T) {
		ctx := newTestContext()
		result, err := fetchPyroscopeUnified(ctx, FetchPyroscopeUnifiedParams{
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

	t.Run("fetch Pyroscope unified profile only", func(t *testing.T) {
		ctx := newTestContext()
		result, err := fetchPyroscopeUnified(ctx, FetchPyroscopeUnifiedParams{
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

	t.Run("fetch Pyroscope unified metrics only", func(t *testing.T) {
		ctx := newTestContext()
		result, err := fetchPyroscopeUnified(ctx, FetchPyroscopeUnifiedParams{
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
