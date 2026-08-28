//go:build cloud

package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/grafana/mcp-grafana/tools/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	athenaCloudDatasourceUID    = "athena-ds-m"
	mssqlCloudDatasourceUID     = "mssql-ds-m"
	snowflakeCloudDatasourceUID = "snowflake-ds-m"
)

func createSQLCloudTestContext(t *testing.T) context.Context {
	t.Helper()
	return createCloudTestContext(t, "SQL", "DATASOURCEDEV_GRAFANA_URL", "DATASOURCEDEV_GRAFANA_API_KEY")
}

func skipIfNoSQLCloudDatasource(t *testing.T) {
	t.Helper()
	if os.Getenv("DATASOURCEDEV_GRAFANA_URL") == "" {
		t.Skip("DATASOURCEDEV_GRAFANA_URL not set, skipping SQL cloud tests")
	}
}

func skipOnDatasourceAuthError(t *testing.T, err error) {
	t.Helper()
	if err != nil && (strings.Contains(err.Error(), "failed to auth") ||
		strings.Contains(err.Error(), "invalid password") ||
		strings.Contains(err.Error(), "PERMISSION_DENIED") ||
		strings.Contains(err.Error(), "status 401")) {
		t.Skipf("datasource auth/permission error: %v", err)
	}
}

// Athena cloud tests
//
// The datasource has defaults configured in jsonData (catalog, database, region).
// fillDatasourceDefaults fills these for ListTables/DescribeTable/Query handlers,
// so most tests pass empty params. ListDatabases is the discovery tool — it does
// NOT fill defaults, so an empty catalog lists catalogs.

func TestSQLCloud_Athena_ListCatalogs(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	result, err := listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{
		DatasourceUID: athenaCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Databases), 1)

	for _, db := range result.Databases {
		assert.NotEmpty(t, db.Catalog)
	}
}

func TestSQLCloud_Athena_ListDatabases(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	catalogs, err := listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{
		DatasourceUID: athenaCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotEmpty(t, catalogs.Databases)

	result, err := listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{
		DatasourceUID: athenaCloudDatasourceUID,
		Catalog:       catalogs.Databases[0].Catalog,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Databases), 1)

	for _, db := range result.Databases {
		assert.NotEmpty(t, db.Database)
	}
}

func TestSQLCloud_Athena_ListTables(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	result, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: athenaCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Tables), 1)
}

func TestSQLCloud_Athena_DescribeTable(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	tables, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: athenaCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotEmpty(t, tables.Tables, "need at least one table to describe")

	result, err := describeSQLTableHandler(ctx, sql.DescribeSQLTableParams{
		DatasourceUID: athenaCloudDatasourceUID,
		Table:         tables.Tables[0].Name,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Columns), 1)

	for _, col := range result.Columns {
		assert.NotEmpty(t, col.Name)
	}
}

func TestSQLCloud_Athena_Query(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	tables, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: athenaCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotEmpty(t, tables.Tables)

	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: athenaCloudDatasourceUID,
		Query:         "SELECT * FROM " + tables.Tables[0].Name,
		Limit:         5,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.Columns)
	assert.Contains(t, result.ProcessedQuery, "LIMIT")
}

// MSSQL cloud tests

func TestSQLCloud_MSSQL_ListDatabases(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	result, err := listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{
		DatasourceUID: mssqlCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Databases), 1)

	for _, db := range result.Databases {
		assert.NotEmpty(t, db.Schema)
	}
}

func TestSQLCloud_MSSQL_ListTables(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	result, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: mssqlCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Tables), 1)

	for _, table := range result.Tables {
		assert.NotEmpty(t, table.Name)
		assert.NotEmpty(t, table.Schema)
	}
}

func TestSQLCloud_MSSQL_DescribeTable(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	tables, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: mssqlCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotEmpty(t, tables.Tables)

	result, err := describeSQLTableHandler(ctx, sql.DescribeSQLTableParams{
		DatasourceUID: mssqlCloudDatasourceUID,
		Table:         tables.Tables[0].Name,
		Schema:        tables.Tables[0].Schema,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Columns), 1)

	for _, col := range result.Columns {
		assert.NotEmpty(t, col.Name)
		assert.NotEmpty(t, col.Type)
	}
}

func TestSQLCloud_MSSQL_Query(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	tables, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: mssqlCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotEmpty(t, tables.Tables)

	table := tables.Tables[0]
	query := "SELECT * FROM " + table.Schema + "." + table.Name
	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: mssqlCloudDatasourceUID,
		Query:         query,
		Limit:         5,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.Columns)
	assert.Contains(t, result.ProcessedQuery, "TOP")
}

// Snowflake cloud tests

func TestSQLCloud_Snowflake_ListDatabases(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	result, err := listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{
		DatasourceUID: snowflakeCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Databases), 1)

	for _, db := range result.Databases {
		assert.NotEmpty(t, db.Schema)
	}
}

func TestSQLCloud_Snowflake_ListTables(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	result, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: snowflakeCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Tables), 1)

	for _, table := range result.Tables {
		assert.NotEmpty(t, table.Name)
		assert.NotEmpty(t, table.Schema)
	}
}

func TestSQLCloud_Snowflake_DescribeTable(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	tables, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: snowflakeCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotEmpty(t, tables.Tables)

	result, err := describeSQLTableHandler(ctx, sql.DescribeSQLTableParams{
		DatasourceUID: snowflakeCloudDatasourceUID,
		Table:         tables.Tables[0].Name,
		Schema:        tables.Tables[0].Schema,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Columns), 1)

	for _, col := range result.Columns {
		assert.NotEmpty(t, col.Name)
		assert.NotEmpty(t, col.Type)
	}
}

func TestSQLCloud_Snowflake_Query(t *testing.T) {
	skipIfNoSQLCloudDatasource(t)
	ctx := createSQLCloudTestContext(t)

	tables, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{
		DatasourceUID: snowflakeCloudDatasourceUID,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	require.NotEmpty(t, tables.Tables)

	table := tables.Tables[0]
	query := "SELECT * FROM " + table.Schema + "." + table.Name
	result, err := querySQLHandler(ctx, sql.QuerySQLParams{
		DatasourceUID: snowflakeCloudDatasourceUID,
		Query:         query,
		Limit:         5,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.Columns)
	assert.Contains(t, result.ProcessedQuery, "LIMIT")
}
