//go:build integration

package tools

import (
	"context"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const clickhouseTestDatasourceUID = "clickhouse"

func TestClickHouseIntegration_ListTables(t *testing.T) {
	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaConfig(ctx, testGrafanaConfig())
	ctx = mcpgrafana.WithGrafanaClient(ctx, testGrafanaClient(t, ctx))

	result, err := listClickHouseTables(ctx, ListClickHouseTablesParams{
		DatasourceUID: clickhouseTestDatasourceUID,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Should find at least the test database tables
	assert.GreaterOrEqual(t, len(result), 1, "Should find at least one table")

	// Verify table structure
	for _, table := range result {
		assert.NotEmpty(t, table.Database, "Table should have a database")
		assert.NotEmpty(t, table.Name, "Table should have a name")
		assert.NotEmpty(t, table.Engine, "Table should have an engine type")
	}
}

func TestClickHouseIntegration_ListTablesFilteredByDatabase(t *testing.T) {
	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaConfig(ctx, testGrafanaConfig())
	ctx = mcpgrafana.WithGrafanaClient(ctx, testGrafanaClient(t, ctx))

	result, err := listClickHouseTables(ctx, ListClickHouseTablesParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Database:      "test",
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// All results should be from the 'test' database
	for _, table := range result {
		assert.Equal(t, "test", table.Database, "All tables should be from the 'test' database")
	}
}

func TestClickHouseIntegration_DescribeTable(t *testing.T) {
	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaConfig(ctx, testGrafanaConfig())
	ctx = mcpgrafana.WithGrafanaClient(ctx, testGrafanaClient(t, ctx))

	result, err := describeClickHouseTable(ctx, DescribeClickHouseTableParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Database:      "test",
		Table:         "logs",
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have columns defined in the init script
	assert.GreaterOrEqual(t, len(result), 1, "Should find at least one column")

	// Verify column structure
	for _, col := range result {
		assert.NotEmpty(t, col.Name, "Column should have a name")
		assert.NotEmpty(t, col.Type, "Column should have a type")
	}

	// Look for expected columns from our test data
	columnNames := make(map[string]bool)
	for _, col := range result {
		columnNames[col.Name] = true
	}

	// These columns should exist based on our init script
	expectedColumns := []string{"Timestamp", "Body", "ServiceName", "SeverityText"}
	for _, expected := range expectedColumns {
		assert.True(t, columnNames[expected], "Should have column: %s", expected)
	}
}

func TestClickHouseIntegration_Query(t *testing.T) {
	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaConfig(ctx, testGrafanaConfig())
	ctx = mcpgrafana.WithGrafanaClient(ctx, testGrafanaClient(t, ctx))

	result, err := queryClickHouse(ctx, ClickHouseQueryParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Query:         "SELECT * FROM test.logs",
		Limit:         10,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have data from our test seed
	assert.GreaterOrEqual(t, result.RowCount, 1, "Should find at least one row")
	assert.NotEmpty(t, result.Columns, "Should have columns")
	assert.NotEmpty(t, result.ProcessedQuery, "Should include processed query")
	assert.Contains(t, result.ProcessedQuery, "LIMIT", "Processed query should have LIMIT")
}

func TestClickHouseIntegration_QueryWithTimeFilter(t *testing.T) {
	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaConfig(ctx, testGrafanaConfig())
	ctx = mcpgrafana.WithGrafanaClient(ctx, testGrafanaClient(t, ctx))

	result, err := queryClickHouse(ctx, ClickHouseQueryParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Query:         "SELECT * FROM test.logs WHERE $__timeFilter(Timestamp)",
		Start:         "now-24h",
		End:           "now",
		Limit:         10,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// The processed query should have the time filter macro replaced
	assert.NotContains(t, result.ProcessedQuery, "$__timeFilter", "Macro should be substituted")
	assert.Contains(t, result.ProcessedQuery, "toDateTime", "Should use toDateTime function")
}

func TestClickHouseIntegration_QueryWithVariables(t *testing.T) {
	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaConfig(ctx, testGrafanaConfig())
	ctx = mcpgrafana.WithGrafanaClient(ctx, testGrafanaClient(t, ctx))

	result, err := queryClickHouse(ctx, ClickHouseQueryParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Query:         "SELECT * FROM test.logs WHERE ServiceName = '${service}'",
		Variables: map[string]string{
			"service": "test-service",
		},
		Limit: 10,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// The processed query should have variables substituted
	assert.NotContains(t, result.ProcessedQuery, "${service}", "Variable should be substituted")
	assert.Contains(t, result.ProcessedQuery, "test-service", "Variable value should appear in query")
}

func TestClickHouseIntegration_QueryEmptyResult(t *testing.T) {
	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaConfig(ctx, testGrafanaConfig())
	ctx = mcpgrafana.WithGrafanaClient(ctx, testGrafanaClient(t, ctx))

	result, err := queryClickHouse(ctx, ClickHouseQueryParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Query:         "SELECT * FROM test.logs WHERE ServiceName = 'nonexistent-service-xyz'",
		Limit:         10,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Should return empty result with hints
	assert.Equal(t, 0, result.RowCount, "Should have no rows")
	assert.NotEmpty(t, result.Hints, "Should include hints for empty results")
}

func TestClickHouseIntegration_InvalidDatasource(t *testing.T) {
	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaConfig(ctx, testGrafanaConfig())
	ctx = mcpgrafana.WithGrafanaClient(ctx, testGrafanaClient(t, ctx))

	_, err := queryClickHouse(ctx, ClickHouseQueryParams{
		DatasourceUID: "nonexistent-datasource",
		Query:         "SELECT 1",
	})

	require.Error(t, err, "Should error with invalid datasource")
}

func TestClickHouseIntegration_WrongDatasourceType(t *testing.T) {
	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaConfig(ctx, testGrafanaConfig())
	ctx = mcpgrafana.WithGrafanaClient(ctx, testGrafanaClient(t, ctx))

	// Try to use a Prometheus datasource as ClickHouse
	_, err := queryClickHouse(ctx, ClickHouseQueryParams{
		DatasourceUID: "prometheus",
		Query:         "SELECT 1",
	})

	require.Error(t, err, "Should error with wrong datasource type")
	assert.Contains(t, err.Error(), "not grafana-clickhouse-datasource", "Error should mention wrong type")
}
