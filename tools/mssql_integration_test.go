//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mssqlTestDatasourceUID = "mssql"

func TestMSSQLIntegration_ListDatabases(t *testing.T) {
	ctx := newTestContext()
	t.Logf("Testing ListDatabases on datasource: %s", mssqlTestDatasourceUID)

	result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
		DatasourceUID: mssqlTestDatasourceUID,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	t.Logf("Found %d databases: %v", len(result.Databases), result.Databases)
	require.NotEmpty(t, result.Databases)

	found := false
	for _, dbName := range result.Databases {
		if dbName == "shop_performance" || dbName == "infrastructure_logs" {
			found = true
			break
		}
	}
	assert.True(t, found, "Should find at least one seeded database name in %v", result.Databases)
}

func TestMSSQLIntegration_ListTables(t *testing.T) {
	ctx := newTestContext()

	t.Run("Default Schema", func(t *testing.T) {
		db := "infrastructure_logs"
		t.Logf("Testing ListTables on database: %s", db)
		result, err := listSQLTables(ctx, ListSQLTablesArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      db,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		t.Logf("Found %d tables in %s: %v", len(result.Tables), db, result.Tables)

		foundLogs := false
		for _, tableName := range result.Tables {
			if tableName == "dbo.logs" {
				foundLogs = true
				break
			}
		}
		assert.True(t, foundLogs, "Should find 'dbo.logs' table in %v", result.Tables)
	})

	t.Run("Custom Schema", func(t *testing.T) {
		db := "shop_performance"
		t.Logf("Testing ListTables on database: %s", db)
		result, err := listSQLTables(ctx, ListSQLTablesArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      db,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		t.Logf("Found %d tables in %s: %v", len(result.Tables), db, result.Tables)

		foundSales := false
		for _, tableName := range result.Tables {
			if tableName == "reporting.sales" {
				foundSales = true
				break
			}
		}
		assert.True(t, foundSales, "Should find 'reporting.sales' table in %v", result.Tables)
	})
}

func TestMSSQLIntegration_ListTableSchemas(t *testing.T) {
	ctx := newTestContext()
	db := "shop_performance"
	table := "reporting.sales"
	t.Logf("Testing ListTableSchema for table: %s.%s", db, table)

	result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
		DatasourceUID: mssqlTestDatasourceUID,
		Database:      db,
		Tables:        table,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Empty(t, result.Errors)
	require.NotEmpty(t, result.Schemas)

	schema, ok := result.Schemas[table]
	require.True(t, ok, "Schema for '%s' should be present", table)
	t.Logf("Fields for %s: %v", table, schema.Fields)
	assert.Equal(t, table, schema.Name)

	assert.Subset(t, schema.Fields, []string{"id", "timestamp", "product_name", "amount"})
}

func TestMSSQLIntegration_ExecuteQuery(t *testing.T) {
	ctx := newTestContext()

	queries := []struct {
		name  string
		query string
	}{
		{
			name:  "Simple Select",
			query: "SELECT * FROM infrastructure_logs.dbo.logs",
		},
		{
			name:  "Cross Schema Query",
			query: "SELECT s.product_name, s.amount FROM shop_performance.reporting.sales s",
		},
		{
			name:  "Join Across Databases",
			query: "SELECT l.id, h.host FROM infrastructure_logs.dbo.logs l JOIN infrastructure_logs.dbo.host_metrics h ON l.id = h.id",
		},
		{
			name:  "Union Across Schemas",
			query: "SELECT product_name FROM shop_performance.reporting.sales UNION SELECT service_name FROM shop_performance.dbo.api_metrics",
		},
	}

	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Executing Query: %s", tc.query)
			result, err := sqlQuery(ctx, SQLQueryArgs{
				DatasourceUID: mssqlTestDatasourceUID,
				Query:         tc.query,
				Limit:         5,
			})

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, result.SQLQueryResult)
			require.Empty(t, result.Error, "Should not have query error: %s", result.Error)
			t.Logf("Received %d frames, first frame has %d rows", len(result.Frames), len(result.Frames[0].Rows))
			require.NotEmpty(t, result.Frames)
		})
	}
}

func TestMSSQLIntegration_TimeRangeQuery(t *testing.T) {
	ctx := newTestContext()
	query := "SELECT host, [timestamp] as time FROM infrastructure_logs.dbo.host_metrics WHERE $__timeFilter([timestamp])"
	t.Logf("Executing TimeRangeQuery: %s", query)

	result, err := sqlQuery(ctx, SQLQueryArgs{
		DatasourceUID: mssqlTestDatasourceUID,
		Query:         query,
		Start:         "now-1d",
		End:           "now",
		Limit:         5,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.SQLQueryResult)
	require.Empty(t, result.Error)
	require.NotEmpty(t, result.Frames)
	t.Logf("Processed query: %s", result.ProcessedQuery)
}

func TestMSSQLIntegration_LimitTests(t *testing.T) {
	ctx := newTestContext()

	t.Run("Default Limit", func(t *testing.T) {
		query := "SELECT * FROM infrastructure_logs.dbo.logs"
		t.Logf("Testing Default Limit for: %s", query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         query,
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		t.Logf("Processed Query: %s", result.ProcessedQuery)
		assert.Contains(t, result.ProcessedQuery, "TOP 100", "Default limit 100 should be applied")
	})

	t.Run("Manual Limit", func(t *testing.T) {
		limit := uint(2)
		query := "SELECT * FROM infrastructure_logs.dbo.logs"
		t.Logf("Testing Manual Limit (%d) for: %s", limit, query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         query,
			Limit:         limit,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		t.Logf("Processed Query: %s", result.ProcessedQuery)
		assert.Contains(t, result.ProcessedQuery, "TOP 2", "Manual limit 2 should be applied")
		if len(result.Frames) > 0 {
			assert.LessOrEqual(t, len(result.Frames[0].Rows), int(limit))
		}
	})
}

func TestMSSQLIntegration_ResponseFormat(t *testing.T) {
	ctx := newTestContext()

	t.Run("Frame Structure", func(t *testing.T) {
		query := "SELECT TOP 3 id, severity_text, service_name FROM infrastructure_logs.dbo.logs"
		t.Logf("Executing query for frame structure validation: %s", query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         query,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.SQLQueryResult, "SQLQueryResult should not be nil")
		require.Empty(t, result.Error, "Error field should be empty for valid query")
		require.NotEmpty(t, result.Frames, "Frames should not be empty")

		frame := result.Frames[0]
		t.Logf("Frame Name: %q", frame.Name)
		t.Logf("Frame Columns: %v", frame.Columns)
		t.Logf("Frame RowCount: %d", frame.RowCount)
		t.Logf("Frame Rows count: %d", len(frame.Rows))

		// Validate Columns
		require.NotEmpty(t, frame.Columns, "Columns should not be empty")
		assert.Subset(t, frame.Columns, []string{"id", "severity_text", "service_name"})
		assert.Equal(t, 3, len(frame.Columns), "Should have exactly 3 columns")

		// Validate RowCount matches actual rows
		assert.Equal(t, frame.RowCount, uint(len(frame.Rows)), "RowCount should match actual rows length")

		// Validate each row has keys matching columns
		for i, row := range frame.Rows {
			t.Logf("Row %d: %v", i, row)
			for _, col := range frame.Columns {
				_, exists := row[col]
				assert.True(t, exists, "Row %d should have key %q matching column name", i, col)
			}
		}
	})

	t.Run("Numeric And String Types", func(t *testing.T) {
		query := "SELECT TOP 2 id, cpu_usage, host FROM infrastructure_logs.dbo.host_metrics"
		t.Logf("Executing query for type validation: %s", query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         query,
		})

		require.NoError(t, err)
		require.NotEmpty(t, result.Frames)

		frame := result.Frames[0]
		require.NotEmpty(t, frame.Rows)

		row := frame.Rows[0]
		t.Logf("First row data: %v", row)

		// id should be a numeric value
		idVal, ok := row["id"]
		require.True(t, ok, "Row should have 'id' key")
		t.Logf("id value: %v (type: %T)", idVal, idVal)
		assert.NotNil(t, idVal, "id should not be nil")

		// host should be a string value
		hostVal, ok := row["host"]
		require.True(t, ok, "Row should have 'host' key")
		t.Logf("host value: %v (type: %T)", hostVal, hostVal)
		_, isString := hostVal.(string)
		assert.True(t, isString, "host should be a string, got %T", hostVal)
	})

	t.Run("Cross Database Join Response", func(t *testing.T) {
		query := "SELECT l.id, h.host, h.cpu_usage FROM infrastructure_logs.dbo.logs l JOIN infrastructure_logs.dbo.host_metrics h ON l.id = h.id"
		t.Logf("Executing cross-database join query: %s", query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         query,
			Limit:         5,
		})

		require.NoError(t, err)
		require.NotNil(t, result.SQLQueryResult)
		require.Empty(t, result.Error)
		require.NotEmpty(t, result.Frames)

		frame := result.Frames[0]
		t.Logf("Join columns: %v", frame.Columns)
		t.Logf("Join row count: %d", frame.RowCount)

		assert.Subset(t, frame.Columns, []string{"id", "host", "cpu_usage"})

		for i, row := range frame.Rows {
			t.Logf("Join row %d: %v", i, row)
		}
	})

	t.Run("Error Response Format", func(t *testing.T) {
		query := "SELECT * FROM nonexistent_db.dbo.nonexistent_table"
		t.Logf("Executing invalid query to test error format: %s", query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         query,
			Limit:         1,
		})

		if err != nil {
			t.Logf("Received Go error: %v", err)
		} else {
			require.NotNil(t, result)
			t.Logf("Result error field: %q", result.Error)
			t.Logf("Result frames count: %d", len(result.Frames))
			assert.NotEmpty(t, result.Error, "Should have error for invalid table")
		}
	})
}

func TestMSSQLIntegration_MultiTableSchema(t *testing.T) {
	ctx := newTestContext()

	t.Run("Two Tables Same Database", func(t *testing.T) {
		tables := "dbo.logs, dbo.host_metrics"
		db := "infrastructure_logs"
		t.Logf("Testing multi-table schema for: %s in database: %s", tables, db)
		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      db,
			Tables:        tables,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors, "Should have no errors, got: %v", result.Errors)
		t.Logf("Schemas returned: %d", len(result.Schemas))

		// Validate dbo.logs schema
		logsSchema, ok := result.Schemas["dbo.logs"]
		require.True(t, ok, "Should have schema for dbo.logs in %v", result.Schemas)
		t.Logf("dbo.logs fields: %v", logsSchema.Fields)
		assert.Equal(t, "dbo.logs", logsSchema.Name)
		assert.Subset(t, logsSchema.Fields, []string{"id", "timestamp", "body", "severity_text"})

		// Validate dbo.host_metrics schema
		metricsSchema, ok := result.Schemas["dbo.host_metrics"]
		require.True(t, ok, "Should have schema for dbo.host_metrics in %v", result.Schemas)
		t.Logf("dbo.host_metrics fields: %v", metricsSchema.Fields)
		assert.Equal(t, "dbo.host_metrics", metricsSchema.Name)
		assert.Subset(t, metricsSchema.Fields, []string{"id", "host", "cpu_usage", "mem_usage"})
	})

	t.Run("Cross Schema Tables", func(t *testing.T) {
		tables := "dbo.api_metrics, reporting.sales"
		db := "shop_performance"
		t.Logf("Testing multi-table schema across schemas: %s in database: %s", tables, db)
		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      db,
			Tables:        tables,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors, "Should have no errors, got: %v", result.Errors)
		t.Logf("Total schemas returned: %d", len(result.Schemas))
		assert.Equal(t, 2, len(result.Schemas), "Should have 2 schemas")

		// Validate dbo.api_metrics schema
		apiSchema, ok := result.Schemas["dbo.api_metrics"]
		require.True(t, ok, "Should have schema for dbo.api_metrics")
		t.Logf("dbo.api_metrics fields: %v", apiSchema.Fields)
		assert.Subset(t, apiSchema.Fields, []string{"endpoint", "status_code", "latency_ms"})

		// Validate reporting.sales schema
		salesSchema, ok := result.Schemas["reporting.sales"]
		require.True(t, ok, "Should have schema for reporting.sales")
		t.Logf("reporting.sales fields: %v", salesSchema.Fields)
		assert.Subset(t, salesSchema.Fields, []string{"product_name", "amount"})
	})

	t.Run("Field Types Validation", func(t *testing.T) {
		table := "dbo.api_metrics"
		db := "shop_performance"
		t.Logf("Testing field type information for: %s in %s", table, db)
		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      db,
			Tables:        table,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors)

		schema, ok := result.Schemas[table]
		require.True(t, ok, "Should have schema for %s", table)
		t.Logf("Fields with types for %s:", table)
		for fieldName, field := range schema.Fields {
			t.Logf("  %s: %s", fieldName, field.Type)
			assert.NotEmpty(t, field.Type, "Field %s should have a type", fieldName)
		}

		// Validate specific MSSQL types
		assert.Subset(t, schema.Fields, []string{"latency_ms", "endpoint", "status_code"})
	})

	t.Run("Mixed Valid And Invalid Tables", func(t *testing.T) {
		tables := "dbo.logs, dbo.nonexistent_table"
		db := "infrastructure_logs"
		t.Logf("Testing multi-table with invalid table: %s in %s", tables, db)
		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      db,
			Tables:        tables,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		t.Logf("Schemas: %d, Errors: %v", len(result.Schemas), result.Errors)

		// Valid table should still return its schema
		logsSchema, ok := result.Schemas["dbo.logs"]
		if ok {
			t.Logf("dbo.logs schema returned successfully with %d fields", len(logsSchema.Fields))
			assert.NotEmpty(t, logsSchema.Fields)
		} else {
			t.Logf("dbo.logs schema not present (may be affected by batch error)")
		}
	})
}
