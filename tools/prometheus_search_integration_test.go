// Requires a Grafana instance running on localhost:3000, with a Prometheus
// datasource provisioned. The Prometheus server must be 3.13.0 or later and
// run with --enable-feature=search-api.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchPrometheusIntegration(t *testing.T) {
	t.Run("metric names", func(t *testing.T) {
		result, err := searchPrometheusMetricNames(newTestContext(), SearchPrometheusMetricNamesParams{
			PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: "prometheus", IncludeScore: true, Limit: 5},
			Search:                 []string{"prometheus_http_requests"},
			IncludeMetadata:        true,
		})
		require.NoError(t, err)
		require.NotEmpty(t, result.Results)
		assert.LessOrEqual(t, len(result.Results), 5)
		assert.False(t, result.Incomplete)
		names := make([]string, 0, len(result.Results))
		for _, r := range result.Results {
			require.NotNil(t, r.Score)
			names = append(names, r.Name)
		}
		assert.Contains(t, names, "prometheus_http_requests_total")
	})

	t.Run("label names on a metric", func(t *testing.T) {
		result, err := searchPrometheusLabelNames(newTestContext(), SearchPrometheusLabelNamesParams{
			PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: "prometheus", Matches: []string{"up"}},
		})
		require.NoError(t, err)
		names := make([]string, 0, len(result.Results))
		for _, r := range result.Results {
			names = append(names, r.Name)
		}
		assert.Contains(t, names, "job")
	})

	t.Run("label values", func(t *testing.T) {
		result, err := searchPrometheusLabelValues(newTestContext(), SearchPrometheusLabelValuesParams{
			PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: "prometheus"},
			Label:                  "job",
			Search:                 []string{"prom"},
		})
		require.NoError(t, err)
		values := make([]string, 0, len(result.Results))
		for _, r := range result.Results {
			values = append(values, r.Value)
		}
		assert.Contains(t, values, "prometheus")
	})
}
