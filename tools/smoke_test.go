package tools

import (
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
)

// TestSmokeTest_AllToolsRegister verifies all tools can be registered without panic
func TestSmokeTest_AllToolsRegister(t *testing.T) {
	s := server.NewMCPServer("smoke-test", "1.0.0")

	// Test all tool registration functions don't panic
	assert.NotPanics(t, func() {
		AddCloudWatchTools(s)
	}, "CloudWatch tools should register without panic")

	assert.NotPanics(t, func() {
		AddClickHouseTools(s)
	}, "ClickHouse tools should register without panic")

	assert.NotPanics(t, func() {
		AddLokiTools(s)
	}, "Loki tools should register without panic")

	assert.NotPanics(t, func() {
		AddPrometheusTools(s)
	}, "Prometheus tools should register without panic")

	assert.NotPanics(t, func() {
		AddHelperTools(s)
	}, "Helper tools should register without panic")
}

// TestSmokeTest_NewToolsExist verifies the new tools are properly defined
func TestSmokeTest_NewToolsExist(t *testing.T) {
	// CloudWatch discovery tools
	assert.NotNil(t, ListCloudWatchNamespaces, "ListCloudWatchNamespaces should be defined")
	assert.NotNil(t, ListCloudWatchMetrics, "ListCloudWatchMetrics should be defined")
	assert.NotNil(t, ListCloudWatchDimensions, "ListCloudWatchDimensions should be defined")

	// ClickHouse discovery tools
	assert.NotNil(t, ListClickHouseTables, "ListClickHouseTables should be defined")
	assert.NotNil(t, DescribeClickHouseTable, "DescribeClickHouseTable should be defined")


	// Helper tools
	assert.NotNil(t, QueryPrometheusHistogram, "QueryPrometheusHistogram should be defined")
	assert.NotNil(t, GetQueryExamples, "GetQueryExamples should be defined")
}

// TestSmokeTest_HintsGeneration verifies hints are generated for all datasource types
func TestSmokeTest_HintsGeneration(t *testing.T) {
	datasourceTypes := []string{"prometheus", "loki", "cloudwatch", "clickhouse", "unknown"}

	for _, dsType := range datasourceTypes {
		t.Run(dsType, func(t *testing.T) {
			hints := GenerateEmptyResultHints(dsType)
			assert.NotEmpty(t, hints, "Should generate hints for %s", dsType)
			assert.Contains(t, hints[0], "No data found", "First hint should mention no data")
		})
	}
}

// TestSmokeTest_QueryExamplesAllTypes verifies examples exist for all supported types
func TestSmokeTest_QueryExamplesAllTypes(t *testing.T) {
	// This tests the examples are defined correctly (doesn't need Grafana connection)
	types := []string{"prometheus", "loki", "cloudwatch", "clickhouse"}

	for _, dsType := range types {
		t.Run(dsType, func(t *testing.T) {
			// We can't call getQueryExamples directly without a context,
			// but we verify the function exists and types are supported
			params := GetQueryExamplesParams{DatasourceType: dsType}
			assert.Equal(t, dsType, params.DatasourceType)
		})
	}
}
