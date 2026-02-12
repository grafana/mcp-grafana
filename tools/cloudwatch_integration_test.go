//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cloudwatchTestDatasourceUID = "cloudwatch"

func TestCloudWatchIntegration_ListNamespaces(t *testing.T) {
	ctx := newTestContext()

	result, err := listCloudWatchNamespaces(ctx, ListCloudWatchNamespacesParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Region:        "us-east-1",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	// LocalStack should have our test namespaces
	assert.GreaterOrEqual(t, len(result), 1, "Should find at least one namespace")
}

func TestCloudWatchIntegration_ListMetrics(t *testing.T) {
	ctx := newTestContext()

	result, err := listCloudWatchMetrics(ctx, ListCloudWatchMetricsParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Namespace:     "Test/Application",
		Region:        "us-east-1",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	// Should find our seeded metrics
	assert.GreaterOrEqual(t, len(result), 1, "Should find at least one metric")
}

func TestCloudWatchIntegration_ListDimensions(t *testing.T) {
	ctx := newTestContext()

	result, err := listCloudWatchDimensions(ctx, ListCloudWatchDimensionsParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Namespace:     "Test/Application",
		MetricName:    "CPUUtilization",
		Region:        "us-east-1",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	// Should find ServiceName dimension
	assert.GreaterOrEqual(t, len(result), 1, "Should find at least one dimension")
}

func TestCloudWatchIntegration_QueryMetrics(t *testing.T) {
	ctx := newTestContext()

	result, err := queryCloudWatch(ctx, CloudWatchQueryParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Namespace:     "Test/Application",
		MetricName:    "CPUUtilization",
		Dimensions:    map[string]string{"ServiceName": "test-service"},
		Statistic:     "Average",
		Start:         "now-1h",
		End:           "now",
		Region:        "us-east-1",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	// May or may not have data depending on LocalStack timing
	assert.NotNil(t, result.Timestamps)
}

func TestCloudWatchIntegration_QueryEmptyResult(t *testing.T) {
	ctx := newTestContext()

	result, err := queryCloudWatch(ctx, CloudWatchQueryParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Namespace:     "NonExistent/Namespace",
		MetricName:    "FakeMetric",
		Statistic:     "Average",
		Start:         "now-1h",
		End:           "now",
		Region:        "us-east-1",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	// Empty result should have hints
	if len(result.Values) == 0 {
		assert.NotEmpty(t, result.Hints, "Empty result should have hints")
	}
}

func TestCloudWatchIntegration_InvalidDatasource(t *testing.T) {
	ctx := newTestContext()

	_, err := queryCloudWatch(ctx, CloudWatchQueryParams{
		DatasourceUID: "nonexistent-datasource",
		Namespace:     "AWS/EC2",
		MetricName:    "CPUUtilization",
		Region:        "us-east-1",
	})

	require.Error(t, err, "Should error with invalid datasource")
}

func TestCloudWatchIntegration_WrongDatasourceType(t *testing.T) {
	ctx := newTestContext()

	// Try to use Prometheus datasource as CloudWatch
	_, err := queryCloudWatch(ctx, CloudWatchQueryParams{
		DatasourceUID: "prometheus",
		Namespace:     "AWS/EC2",
		MetricName:    "CPUUtilization",
		Region:        "us-east-1",
	})

	require.Error(t, err, "Should error with wrong datasource type")
	assert.Contains(t, err.Error(), "not cloudwatch", "Error should mention wrong type")
}
