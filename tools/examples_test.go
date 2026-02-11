//go:build unit

package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetQueryExamples_Prometheus(t *testing.T) {
	result, err := getQueryExamples(context.Background(), GetQueryExamplesParams{
		DatasourceType: "prometheus",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "prometheus", result.DatasourceType)
	assert.NotEmpty(t, result.Examples)

	// Check that we have the expected examples
	var foundRequestRate, found95thPercentile, foundUpTargets bool
	for _, ex := range result.Examples {
		switch ex.Name {
		case "Request rate":
			foundRequestRate = true
			assert.Contains(t, ex.Query, "rate(http_requests_total[5m])")
			assert.NotEmpty(t, ex.Description)
		case "95th percentile latency":
			found95thPercentile = true
			assert.Contains(t, ex.Query, "histogram_quantile(0.95")
			assert.NotEmpty(t, ex.Description)
		case "Up targets by job":
			foundUpTargets = true
			assert.Contains(t, ex.Query, "sum by (job) (up)")
			assert.NotEmpty(t, ex.Description)
		}
	}
	assert.True(t, foundRequestRate, "Expected 'Request rate' example")
	assert.True(t, found95thPercentile, "Expected '95th percentile latency' example")
	assert.True(t, foundUpTargets, "Expected 'Up targets by job' example")
}

func TestGetQueryExamples_Loki(t *testing.T) {
	result, err := getQueryExamples(context.Background(), GetQueryExamplesParams{
		DatasourceType: "loki",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "loki", result.DatasourceType)
	assert.NotEmpty(t, result.Examples)

	// Check that we have the expected examples
	var foundErrorLogs, foundJSONFilter, foundLogVolume bool
	for _, ex := range result.Examples {
		switch ex.Name {
		case "Error logs":
			foundErrorLogs = true
			assert.Contains(t, ex.Query, `{job="nginx"}`)
			assert.Contains(t, ex.Query, `"error"`)
			assert.NotEmpty(t, ex.Description)
		case "JSON logs with status filter":
			foundJSONFilter = true
			assert.Contains(t, ex.Query, "| json |")
			assert.Contains(t, ex.Query, "status >= 500")
			assert.NotEmpty(t, ex.Description)
		case "Log volume by status":
			foundLogVolume = true
			assert.Contains(t, ex.Query, "sum(rate(")
			assert.Contains(t, ex.Query, "by (status)")
			assert.NotEmpty(t, ex.Description)
		}
	}
	assert.True(t, foundErrorLogs, "Expected 'Error logs' example")
	assert.True(t, foundJSONFilter, "Expected 'JSON logs with status filter' example")
	assert.True(t, foundLogVolume, "Expected 'Log volume by status' example")
}

func TestGetQueryExamples_ClickHouse(t *testing.T) {
	result, err := getQueryExamples(context.Background(), GetQueryExamplesParams{
		DatasourceType: "clickhouse",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "clickhouse", result.DatasourceType)
	assert.NotEmpty(t, result.Examples)

	// Check that we have the expected examples
	var foundBasicQuery, foundTimeSeriesCount bool
	for _, ex := range result.Examples {
		switch ex.Name {
		case "Basic time-filtered query":
			foundBasicQuery = true
			assert.Contains(t, ex.Query, "$__timeFilter")
			assert.NotEmpty(t, ex.Description)
		case "Time series count":
			foundTimeSeriesCount = true
			assert.Contains(t, ex.Query, "$__timeInterval")
			assert.Contains(t, ex.Query, "count(*)")
			assert.NotEmpty(t, ex.Description)
		}
	}
	assert.True(t, foundBasicQuery, "Expected 'Basic time-filtered query' example")
	assert.True(t, foundTimeSeriesCount, "Expected 'Time series count' example")
}

func TestGetQueryExamples_CloudWatch(t *testing.T) {
	result, err := getQueryExamples(context.Background(), GetQueryExamplesParams{
		DatasourceType: "cloudwatch",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "cloudwatch", result.DatasourceType)
	assert.NotEmpty(t, result.Examples)

	// Check that we have the expected examples with CloudWatch-specific fields
	var foundECSCPU, foundEC2CPU, foundRDS, foundLambda bool
	for _, ex := range result.Examples {
		switch ex.Name {
		case "ECS CPU Utilization":
			foundECSCPU = true
			assert.Equal(t, "AWS/ECS", ex.Namespace)
			assert.Equal(t, "CPUUtilization", ex.MetricName)
			assert.NotNil(t, ex.Dimensions)
			assert.Contains(t, ex.Dimensions, "ClusterName")
			assert.NotEmpty(t, ex.Description)
		case "EC2 CPU Utilization":
			foundEC2CPU = true
			assert.Equal(t, "AWS/EC2", ex.Namespace)
			assert.Equal(t, "CPUUtilization", ex.MetricName)
			assert.NotNil(t, ex.Dimensions)
			assert.Contains(t, ex.Dimensions, "InstanceId")
			assert.NotEmpty(t, ex.Description)
		case "RDS Database Connections":
			foundRDS = true
			assert.Equal(t, "AWS/RDS", ex.Namespace)
			assert.Equal(t, "DatabaseConnections", ex.MetricName)
			assert.NotNil(t, ex.Dimensions)
			assert.NotEmpty(t, ex.Description)
		case "Lambda Invocations":
			foundLambda = true
			assert.Equal(t, "AWS/Lambda", ex.Namespace)
			assert.Equal(t, "Invocations", ex.MetricName)
			assert.NotNil(t, ex.Dimensions)
			assert.Contains(t, ex.Dimensions, "FunctionName")
			assert.NotEmpty(t, ex.Description)
		}
	}
	assert.True(t, foundECSCPU, "Expected 'ECS CPU Utilization' example")
	assert.True(t, foundEC2CPU, "Expected 'EC2 CPU Utilization' example")
	assert.True(t, foundRDS, "Expected 'RDS Database Connections' example")
	assert.True(t, foundLambda, "Expected 'Lambda Invocations' example")
}

func TestGetQueryExamples_CaseInsensitive(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"PROMETHEUS", "prometheus"},
		{"Prometheus", "prometheus"},
		{"LOKI", "loki"},
		{"Loki", "loki"},
		{"CLICKHOUSE", "clickhouse"},
		{"ClickHouse", "clickhouse"},
		{"CLOUDWATCH", "cloudwatch"},
		{"CloudWatch", "cloudwatch"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result, err := getQueryExamples(context.Background(), GetQueryExamplesParams{
				DatasourceType: tc.input,
			})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tc.expected, result.DatasourceType)
			assert.NotEmpty(t, result.Examples)
		})
	}
}

func TestGetQueryExamples_UnsupportedDatasource(t *testing.T) {
	testCases := []string{
		"mysql",
		"postgres",
		"elasticsearch",
		"unknown",
		"",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			result, err := getQueryExamples(context.Background(), GetQueryExamplesParams{
				DatasourceType: tc,
			})
			require.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), "unsupported datasource type")
			// Check that error message includes supported types
			assert.Contains(t, err.Error(), "prometheus")
			assert.Contains(t, err.Error(), "loki")
			assert.Contains(t, err.Error(), "clickhouse")
			assert.Contains(t, err.Error(), "cloudwatch")
		})
	}
}

func TestGetQueryExamples_ExamplesHaveRequiredFields(t *testing.T) {
	datasourceTypes := []string{"prometheus", "loki", "clickhouse", "cloudwatch"}

	for _, dsType := range datasourceTypes {
		t.Run(dsType, func(t *testing.T) {
			result, err := getQueryExamples(context.Background(), GetQueryExamplesParams{
				DatasourceType: dsType,
			})
			require.NoError(t, err)
			require.NotNil(t, result)

			for _, ex := range result.Examples {
				assert.NotEmpty(t, ex.Name, "Example should have a name")
				assert.NotEmpty(t, ex.Description, "Example should have a description")
				// CloudWatch examples use Namespace/MetricName instead of Query
				if dsType != "cloudwatch" {
					assert.NotEmpty(t, ex.Query, "Non-CloudWatch example should have a query")
				} else {
					assert.NotEmpty(t, ex.Namespace, "CloudWatch example should have a namespace")
					assert.NotEmpty(t, ex.MetricName, "CloudWatch example should have a metric name")
				}
			}
		})
	}
}

func TestGetQueryExamples_ToolDefinition(t *testing.T) {
	// Verify the tool is properly defined
	assert.Equal(t, "get_query_examples", GetQueryExamples.Tool.Name)
	assert.NotEmpty(t, GetQueryExamples.Tool.Description)
	assert.Contains(t, GetQueryExamples.Tool.Description, "Prometheus")
	assert.Contains(t, GetQueryExamples.Tool.Description, "Loki")
	assert.Contains(t, GetQueryExamples.Tool.Description, "ClickHouse")
	assert.Contains(t, GetQueryExamples.Tool.Description, "CloudWatch")
}
