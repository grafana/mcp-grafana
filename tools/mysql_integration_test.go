//go:build integration

package tools

import (
	"testing"

	"github.com/grafana/mcp-grafana/tools/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mysqlTestDatasourceUID = "mysql"

func TestMySQLIntegration_ListDatabases(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
		DatasourceUID: mysqlTestDatasourceUID,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// result should be *sql.DSQueryResponse
	resp, ok := result.(*sql.DSQueryResponse)
	require.True(t, ok, "result should be *sql.DSQueryResponse")

	// Verify we got results for refId "databases"
	res, ok := resp.Results["databases"]
	require.True(t, ok, "should have results for 'databases' refId")
	require.Empty(t, res.Error, "should not have an error: %s", res.Error)
	require.NotEmpty(t, res.Frames, "should have at least one frame")

	// MySQL list databases query: "SELECT DISTINCT TABLE_SCHEMA from information_schema.TABLES where TABLE_TYPE != 'SYSTEM VIEW' ORDER BY TABLE_SCHEMA"
	// It should return at least "information_schema" or similar databases.
	found := false
	for _, frame := range res.Frames {
		if len(frame.Data.Values) > 0 {
			// Values is [][]interface{}: each inner slice is a column
			for _, col := range frame.Data.Values {
				for _, val := range col {
					if dbName, ok := val.(string); ok {
						if dbName != "" {
							found = true
							break
						}
					}
				}
				if found {
					break
				}
			}
		}
		if found {
			break
		}
	}
	assert.True(t, found, "Should find at least one database name")
}

func TestMySQLIntegration_InvalidDatasource(t *testing.T) {
	ctx := newTestContext()

	_, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
		DatasourceUID: "nonexistent-datasource",
	})

	require.Error(t, err, "Should error with invalid datasource")
}

func TestMySQLIntegration_WrongDatasourceType(t *testing.T) {
	ctx := newTestContext()

	// Use Prometheus which is not an SQL datasource
	_, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
		DatasourceUID: "prometheus",
	})

	require.Error(t, err, "Should error with wrong datasource type")
	assert.Contains(t, err.Error(), "is not an SQL Datasource")
}

func TestMySQLIntegration_ListTables(t *testing.T) {
	ctx := newTestContext()

	result, err := listSQLTables(ctx, ListSQLTablesArgs{
		DatasourceUID: mysqlTestDatasourceUID,
		Database:      "infrastructure_logs",
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	resp, ok := result.(*sql.DSQueryResponse)
	require.True(t, ok, "result should be *sql.DSQueryResponse")

	res, ok := resp.Results["tables"]
	require.True(t, ok, "should have results for 'tables' refId")
	require.Empty(t, res.Error, "should not have an error: %s", res.Error)

	// Should find 'logs' and 'host_metrics' tables
	foundLogs := false
	foundHostMetrics := false
	for _, frame := range res.Frames {
		for _, col := range frame.Data.Values {
			for _, val := range col {
				if tableName, ok := val.(string); ok {
					if tableName == "logs" {
						foundLogs = true
					}
					if tableName == "host_metrics" {
						foundHostMetrics = true
					}
				}
			}
		}
	}
	assert.True(t, foundLogs, "Should find 'logs' table")
	assert.True(t, foundHostMetrics, "Should find 'host_metrics' table")
}

func TestMySQLIntegration_GetTableSchema(t *testing.T) {
	ctx := newTestContext()

	result, err := getSQLTableSchema(ctx, GetSQLTableSchemaArgs{
		DatasourceUID: mysqlTestDatasourceUID,
		Database:      "infrastructure_logs",
		Table:         "logs",
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	resp, ok := result.(*sql.DSQueryResponse)
	require.True(t, ok, "result should be *sql.DSQueryResponse")

	res, ok := resp.Results["schema"]
	require.True(t, ok, "should have results for 'schema' refId")
	require.Empty(t, res.Error, "should not have an error: %s", res.Error)

	// Should find columns: id, timestamp, body, service_name, severity_text, trace_id
	expectedColumns := map[string]bool{
		"id":            false,
		"timestamp":     false,
		"body":          false,
		"service_name":  false,
		"severity_text": false,
		"trace_id":      false,
	}

	for _, frame := range res.Frames {
		// In schema query, first column is usually column_name
		if len(frame.Data.Values) > 0 {
			for _, val := range frame.Data.Values[0] {
				if colName, ok := val.(string); ok {
					if _, exists := expectedColumns[colName]; exists {
						expectedColumns[colName] = true
					}
				}
			}
		}
	}

	for col, found := range expectedColumns {
		assert.True(t, found, "Column %s should be present in schema", col)
	}
}
