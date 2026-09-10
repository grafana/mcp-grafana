//go:build integration

package tools

import (
	"testing"

	"github.com/grafana/mcp-grafana/tools/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ClickHouse integration tests

const clickhouseTestDatasourceUID = "clickhouse"

func TestSQLIntegration_ClickHouse_ListDatabases(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{
		DatasourceUID: clickhouseTestDatasourceUID,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Databases), 1)

	names := make(map[string]bool)
	for _, db := range result.Databases {
		assert.NotEmpty(t, db.Database)
		names[db.Database] = true
	}
	assert.True(t, names["test"], "should include 'test' database")
	assert.False(t, names["system"], "should exclude system databases")
}

func TestSQLIntegration_ClickHouse_ListTables(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: clickhouseTestDatasourceUID,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Tables), 1)

	for _, table := range result.Tables {
		assert.NotEmpty(t, table.Database)
		assert.NotEmpty(t, table.Name)
	}
}

func TestSQLIntegration_ClickHouse_ListTablesFiltered(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Database:      "test",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	for _, table := range result.Tables {
		assert.Equal(t, "test", table.Database)
	}
}

func TestSQLIntegration_ClickHouse_DescribeTable(t *testing.T) {
	ctx := newTestContext()

	result, err := describeSQLTableHandler(ctx, sql.DescribeSQLTableParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Database:      "test",
		Table:         "logs",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Columns), 1)

	columnNames := make(map[string]bool)
	for _, col := range result.Columns {
		assert.NotEmpty(t, col.Name)
		assert.NotEmpty(t, col.Type)
		columnNames[col.Name] = true
	}

	for _, expected := range []string{"Timestamp", "Body", "ServiceName", "SeverityText"} {
		assert.True(t, columnNames[expected], "expected column %s", expected)
	}
}

func TestSQLIntegration_ClickHouse_Query(t *testing.T) {
	ctx := newTestContext()

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Query:         "SELECT * FROM test.logs",
		Limit:         10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, result.RowCount, 1)
	assert.NotEmpty(t, result.Columns)
	assert.Contains(t, result.ProcessedQuery, "LIMIT")
}

func TestSQLIntegration_ClickHouse_QueryWithTimeFilter(t *testing.T) {
	ctx := newTestContext()

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Query:         "SELECT * FROM test.logs WHERE $__timeFilter(Timestamp)",
		Start:         "now-24h",
		End:           "now",
		Limit:         10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotContains(t, result.ProcessedQuery, "$__timeFilter")
	assert.Contains(t, result.ProcessedQuery, "toDateTime")
}

func TestSQLIntegration_ClickHouse_QueryWithVariables(t *testing.T) {
	ctx := newTestContext()

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Query:         "SELECT * FROM test.logs WHERE ServiceName = '${service}'",
		Variables:     map[string]string{"service": "test-service"},
		Limit:         10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotContains(t, result.ProcessedQuery, "${service}")
	assert.Contains(t, result.ProcessedQuery, "test-service")
}

func TestSQLIntegration_ClickHouse_QueryEmptyResult(t *testing.T) {
	ctx := newTestContext()

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: clickhouseTestDatasourceUID,
		Query:         "SELECT * FROM test.logs WHERE ServiceName = 'nonexistent-service-xyz'",
		Limit:         10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.RowCount)
	assert.NotNil(t, result.Hints)
}

// MySQL integration tests

const mysqlTestDatasourceUID = "mysql"

func TestSQLIntegration_MySQL_ListDatabases(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{
		DatasourceUID: mysqlTestDatasourceUID,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Databases), 1)

	names := make(map[string]bool)
	for _, db := range result.Databases {
		assert.NotEmpty(t, db.Database)
		names[db.Database] = true
	}
	assert.True(t, names["test"], "should include 'test' database")
	assert.False(t, names["mysql"], "should exclude system databases")
}

func TestSQLIntegration_MySQL_ListTables(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: mysqlTestDatasourceUID,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Tables), 1)

	for _, table := range result.Tables {
		assert.NotEmpty(t, table.Name)
	}
}

func TestSQLIntegration_MySQL_ListTablesFilteredByDatabase(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: mysqlTestDatasourceUID,
		Database:      "test",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	for _, table := range result.Tables {
		assert.Equal(t, "test", table.Database)
	}
}

func TestSQLIntegration_MySQL_DescribeTable(t *testing.T) {
	ctx := newTestContext()

	result, err := describeSQLTableHandler(ctx, sql.DescribeSQLTableParams{
		DatasourceUID: mysqlTestDatasourceUID,
		Database:      "test",
		Table:         "logs",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Columns), 1)

	columnNames := make(map[string]bool)
	for _, col := range result.Columns {
		assert.NotEmpty(t, col.Name)
		assert.NotEmpty(t, col.Type)
		columnNames[col.Name] = true
	}

	for _, expected := range []string{"id", "timestamp", "body", "service_name", "severity_text"} {
		assert.True(t, columnNames[expected], "expected column %s", expected)
	}
}

func TestSQLIntegration_MySQL_Query(t *testing.T) {
	ctx := newTestContext()

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: mysqlTestDatasourceUID,
		Query:         "SELECT * FROM test.logs",
		Limit:         10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, result.RowCount, 1)
	assert.NotEmpty(t, result.Columns)
	assert.Contains(t, result.ProcessedQuery, "LIMIT")
}

func TestSQLIntegration_MySQL_QueryWithTimeFilter(t *testing.T) {
	ctx := newTestContext()

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: mysqlTestDatasourceUID,
		Query:         "SELECT * FROM test.logs WHERE $__timeFilter(timestamp)",
		Start:         "now-24h",
		End:           "now",
		Limit:         10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotContains(t, result.ProcessedQuery, "$__timeFilter")
	assert.Contains(t, result.ProcessedQuery, "BETWEEN")
}

func TestSQLIntegration_MySQL_QueryEmptyResult(t *testing.T) {
	ctx := newTestContext()

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: mysqlTestDatasourceUID,
		Query:         "SELECT * FROM test.logs WHERE service_name = 'nonexistent-service-xyz'",
		Limit:         10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.RowCount)
}

// PostgreSQL integration tests

const postgresTestDatasourceUID = "postgres"

func TestSQLIntegration_Postgres_ListDatabases(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{
		DatasourceUID: postgresTestDatasourceUID,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Databases), 1)

	names := make(map[string]bool)
	for _, db := range result.Databases {
		assert.NotEmpty(t, db.Schema)
		names[db.Schema] = true
	}
	assert.True(t, names["public"], "should include 'public' schema")
	assert.False(t, names["pg_catalog"], "should exclude system schemas")
}

func TestSQLIntegration_Postgres_ListTables(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: postgresTestDatasourceUID,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Tables), 1)

	for _, table := range result.Tables {
		assert.NotEmpty(t, table.Name)
	}
}

func TestSQLIntegration_Postgres_ListTablesFilteredBySchema(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: postgresTestDatasourceUID,
		Schema:        "public",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	for _, table := range result.Tables {
		assert.Equal(t, "public", table.Schema)
	}
}

func TestSQLIntegration_Postgres_DescribeTable(t *testing.T) {
	ctx := newTestContext()

	result, err := describeSQLTableHandler(ctx, sql.DescribeSQLTableParams{
		DatasourceUID: postgresTestDatasourceUID,
		Table:         "logs",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Columns), 1)

	columnNames := make(map[string]bool)
	for _, col := range result.Columns {
		assert.NotEmpty(t, col.Name)
		assert.NotEmpty(t, col.Type)
		columnNames[col.Name] = true
	}

	for _, expected := range []string{"id", "timestamp", "body", "service_name", "severity_text"} {
		assert.True(t, columnNames[expected], "expected column %s", expected)
	}
}

func TestSQLIntegration_Postgres_Query(t *testing.T) {
	ctx := newTestContext()

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: postgresTestDatasourceUID,
		Query:         "SELECT * FROM logs",
		Limit:         10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, result.RowCount, 1)
	assert.NotEmpty(t, result.Columns)
	assert.Contains(t, result.ProcessedQuery, "LIMIT")
}

func TestSQLIntegration_Postgres_QueryWithTimeFilter(t *testing.T) {
	ctx := newTestContext()

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: postgresTestDatasourceUID,
		Query:         "SELECT * FROM logs WHERE $__timeFilter(timestamp)",
		Start:         "now-24h",
		End:           "now",
		Limit:         10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotContains(t, result.ProcessedQuery, "$__timeFilter")
	assert.Contains(t, result.ProcessedQuery, "BETWEEN")
}

func TestSQLIntegration_Postgres_QueryEmptyResult(t *testing.T) {
	ctx := newTestContext()

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: postgresTestDatasourceUID,
		Query:         "SELECT * FROM logs WHERE service_name = 'nonexistent-service-xyz'",
		Limit:         10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.RowCount)
}

// Cross-dialect error tests

func TestSQLIntegration_InvalidDatasource(t *testing.T) {
	ctx := newTestContext()

	_, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: "nonexistent-datasource",
		Query:         "SELECT 1",
	})
	require.Error(t, err)
}

func TestSQLIntegration_WrongDatasourceType(t *testing.T) {
	ctx := newTestContext()

	_, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: "prometheus",
		Query:         "SELECT 1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported SQL datasource type")
}
