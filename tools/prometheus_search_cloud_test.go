//go:build cloud
// +build cloud

// Cloud integration tests for the Prometheus search API against the Grafana
// Cloud Metrics (Mimir) datasource of the stack at GRAFANA_URL. The tests skip
// if GRAFANA_URL is not set.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mcptests stack has few metrics. On 2026-10-01 the only metric name in
// the last 7 days was GRAFANA_ALERTS, which Grafana alerting writes, so the
// metric names test searches for "alerts".
const prometheusSearchCloudDatasourceUID = "grafanacloud-prom"

func TestSearchPrometheusCloud(t *testing.T) {
	uid := prometheusSearchCloudDatasourceUID
	ctx := createCloudTestContext(t, "PrometheusSearch", "GRAFANA_URL", "GRAFANA_API_KEY")

	t.Run("metric names", func(t *testing.T) {
		result, err := searchPrometheusMetricNames(ctx, SearchPrometheusMetricNamesParams{
			PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: uid, StartRFC3339: "now-1d", Limit: 5},
			Search:                 []string{"alerts"},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result.Results)
		assert.LessOrEqual(t, len(result.Results), 5)
	})

	t.Run("label names", func(t *testing.T) {
		result, err := searchPrometheusLabelNames(ctx, SearchPrometheusLabelNamesParams{
			PrometheusSearchParams: PrometheusSearchParams{
				DatasourceUID: uid,
				Matches:       []string{`{__name__=~".+"}`},
				StartRFC3339:  "now-1d",
				Limit:         5,
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result.Results)
		assert.LessOrEqual(t, len(result.Results), 5)
	})

	t.Run("label values", func(t *testing.T) {
		result, err := searchPrometheusLabelValues(ctx, SearchPrometheusLabelValuesParams{
			PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: uid, StartRFC3339: "now-1d", Limit: 5},
			Label:                  "__name__",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result.Results)
	})
}
