//go:build cloud
// +build cloud

// Cloud integration tests for Cloud Monitoring (Stackdriver) datasources.
// These tests run against a Grafana instance with a Cloud Monitoring datasource
// configured at (DATASOURCEDEV_GRAFANA_URL, DATASOURCEDEV_GRAFANA_SERVICE_ACCOUNT_TOKEN).
// Tests will skip if the required environment variables are not set.

package tools

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cloudMonitoringTestDatasourceUID = "dehw4r7w7o0lcd"

func createCloudMonitoringTestContext(t *testing.T) context.Context {
	t.Helper()
	return createCloudTestContext(t, "CloudMonitoring", "DATASOURCEDEV_GRAFANA_URL", "DATASOURCEDEV_GRAFANA_API_KEY")
}

func skipIfNoCloudMonitoringDatasource(t *testing.T) {
	t.Helper()
	if os.Getenv("DATASOURCEDEV_GRAFANA_URL") == "" {
		t.Skip("DATASOURCEDEV_GRAFANA_URL not set, skipping Cloud Monitoring tests")
	}
}

func TestCloudMonitoringQuery(t *testing.T) {
	skipIfNoCloudMonitoringDatasource(t)
	ctx := createCloudMonitoringTestContext(t)

	t.Run("range query", func(t *testing.T) {
		result, err := queryPrometheus(ctx, QueryPrometheusParams{
			DatasourceUID: cloudMonitoringTestDatasourceUID,
			Expr:          `compute_googleapis_com:instance:cpu:utilization{monitored_resource="gce_instance"}`,
			StartTime:     "now-1h",
			EndTime:       "now",
			StepSeconds:   60,
			QueryType:     "range",
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("instant query", func(t *testing.T) {
		result, err := queryPrometheus(ctx, QueryPrometheusParams{
			DatasourceUID: cloudMonitoringTestDatasourceUID,
			Expr:          `compute_googleapis_com:instance:cpu:utilization`,
			EndTime:     "now",
			QueryType:     "instant",
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}

func TestCloudMonitoringListMetricNames(t *testing.T) {
	skipIfNoCloudMonitoringDatasource(t)
	ctx := createCloudMonitoringTestContext(t)

	result, err := listPrometheusMetricNames(ctx, ListPrometheusMetricNamesParams{
		DatasourceUID: cloudMonitoringTestDatasourceUID,
		Limit:         10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.LessOrEqual(t, len(result), 10)

	// Cloud Monitoring metric names should contain the GCP domain prefix
	for _, name := range result {
		assert.Contains(t, name, "googleapis.com", "metric name should be a GCP metric type")
	}
}

func TestCloudMonitoringListMetricMetadata(t *testing.T) {
	skipIfNoCloudMonitoringDatasource(t)
	ctx := createCloudMonitoringTestContext(t)

	result, err := listPrometheusMetricMetadata(ctx, ListPrometheusMetricMetadataParams{
		DatasourceUID: cloudMonitoringTestDatasourceUID,
		Limit:         5,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.LessOrEqual(t, len(result), 5)

	for name, metadata := range result {
		assert.Contains(t, name, "googleapis.com")
		require.NotEmpty(t, metadata)
		// Each metric should have a description
		assert.NotEmpty(t, metadata[0].Help, "metric %s should have a description", name)
	}
}

func TestCloudMonitoringListLabelNames(t *testing.T) {
	skipIfNoCloudMonitoringDatasource(t)
	ctx := createCloudMonitoringTestContext(t)

	// Cloud Monitoring requires a metric filter — you can't query all metrics at once.
	// This matches the real workflow: discover metrics first, then ask for their labels.
	result, err := listPrometheusLabelNames(ctx, ListPrometheusLabelNamesParams{
		DatasourceUID: cloudMonitoringTestDatasourceUID,
		Matches: []Selector{{
			Filters: []LabelMatcher{
				{Name: "__name__", Value: "compute.googleapis.com/instance/cpu/utilization", Type: "="},
			},
		}},
		Limit: 50,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
}

func TestCloudMonitoringListLabelValues(t *testing.T) {
	skipIfNoCloudMonitoringDatasource(t)
	ctx := createCloudMonitoringTestContext(t)

	t.Run("__name__ returns metric names", func(t *testing.T) {
		result, err := listPrometheusLabelValues(ctx, ListPrometheusLabelValuesParams{
			DatasourceUID: cloudMonitoringTestDatasourceUID,
			LabelName:     "__name__",
			Limit:         5,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)
		for _, name := range result {
			assert.Contains(t, name, "googleapis.com")
		}
	})
}
