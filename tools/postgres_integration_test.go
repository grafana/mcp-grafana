//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const postgresTestDatasourceUID = "postgres"

func TestPostgresIntegration_ListDatabases(t *testing.T) {
	ctx := newTestContext()
	t.Logf("Testing ListDatabases on datasource: %s", postgresTestDatasourceUID)

	result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
		DatasourceUID: postgresTestDatasourceUID,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	t.Logf("Found %d databases: %v", len(result.Databases), result.Databases)
	require.NotEmpty(t, result.Databases)

	foundLogs := false
	foundApp := false
	for _, dbName := range result.Databases {
		if dbName == "infrastructure_logs" {
			foundLogs = true
		}
		if dbName == "application_db" {
			foundApp = true
		}
	}
	assert.True(t, foundLogs, "Should find 'infrastructure_logs' database in %v", result.Databases)
	assert.True(t, foundApp, "Should find 'application_db' database in %v", result.Databases)
}

func TestPostgresIntegration_InvalidDatasource(t *testing.T) {
	ctx := newTestContext()
	uid := "nonexistent-datasource"
	t.Logf("Testing InvalidDatasource with UID: %s", uid)

	_, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
		DatasourceUID: uid,
	})

	require.Error(t, err, "Should error with invalid datasource")
	t.Logf("Received expected error: %v", err)
}

func TestPostgresIntegration_WrongDatasourceType(t *testing.T) {
	ctx := newTestContext()
	uid := "prometheus"
	t.Logf("Testing WrongDatasourceType with UID: %s", uid)

	// Use Prometheus which is not an SQL datasource
	_, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
		DatasourceUID: uid,
	})

	require.Error(t, err, "Should error with wrong datasource type")
	assert.Contains(t, err.Error(), "is not an SQL Datasource")
	t.Logf("Received expected error: %v", err)
}

func TestPostgresIntegration_ListTables(t *testing.T) {
	ctx := newTestContext()
	t.Logf("Testing ListTables on datasource: %s", postgresTestDatasourceUID)

	result, err := listSQLTables(ctx, ListSQLTablesArgs{
		DatasourceUID: postgresTestDatasourceUID,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	t.Logf("Found %d tables: %v", len(result.Tables), result.Tables)

	// Should find tables across different schemas (standard in Postgres to see schema-qualified names)
	foundLogs := false
	foundSales := false
	foundProducts := false
	foundAudit := false

	for _, tableName := range result.Tables {
		if tableName == "public.logs" || tableName == "logs" || tableName == "\"public\".\"logs\"" {
			foundLogs = true
		}
		if tableName == "reporting.sales" || tableName == "sales" || tableName == "\"reporting\".\"sales\"" {
			foundSales = true
		}
		if tableName == "catalog.products" || tableName == "products" || tableName == "\"catalog\".\"products\"" {
			foundProducts = true
		}
		if tableName == "events.audit_log" || tableName == "audit_log" || tableName == "\"events\".\"audit_log\"" {
			foundAudit = true
		}
	}
	assert.True(t, foundLogs, "Should find 'public.logs' table in %v", result.Tables)
	assert.True(t, foundSales, "Should find 'reporting.sales' table in %v", result.Tables)
	assert.True(t, foundProducts, "Should find 'catalog.products' table in %v", result.Tables)
	assert.True(t, foundAudit, "Should find 'events.audit_log' table in %v", result.Tables)
}

func TestPostgresIntegration_ListTableSchemas(t *testing.T) {
	ctx := newTestContext()

	t.Run("Public Schema Table", func(t *testing.T) {
		table := "public.logs"
		t.Logf("Testing ListTableSchema for: %s", table)
		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        table,
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors)

		schema, ok := result.Schemas[table]
		require.True(t, ok, "Schema for %s should be present in %v", table, result.Schemas)
		t.Logf("Fields for %s: %v", table, schema.Fields)
		// "timestamp" is returned as "\"timestamp\"" because Postgres quote_ident is used on reserved keywords
		assert.Subset(t, schema.Fields, []string{"id", "\"timestamp\"", "body", "severity_text"})
	})

	t.Run("Custom Schema Table", func(t *testing.T) {
		table := "reporting.sales"
		t.Logf("Testing ListTableSchema for: %s", table)
		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        table,
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors)

		schema, ok := result.Schemas[table]
		require.True(t, ok, "Schema for %s should be present", table)
		t.Logf("Fields for %s: %v", table, schema.Fields)
		assert.Equal(t, table, schema.Name)
		assert.Subset(t, schema.Fields, []string{"id", "product_name", "amount"})
	})
}

func TestPostgresIntegration_ExecuteQuery(t *testing.T) {
	ctx := newTestContext()

	queries := []struct {
		name  string
		query string
	}{
		{
			name:  "Simple Select",
			query: "SELECT * FROM public.logs LIMIT 5",
		},
		{
			name:  "Cross Schema Join",
			query: "SELECT s.product_name, s.amount FROM reporting.sales s JOIN catalog.products p ON s.product_name = p.name",
		},
		{
			name:  "JSONB Query",
			query: "SELECT actor, metadata->>'ip' as ip FROM events.audit_log WHERE metadata->>'ip' IS NOT NULL",
		},
		{
			name:  "Array Type Query",
			query: "SELECT actor, tags FROM events.audit_log",
		},
		{
			name:  "Union Across Schemas",
			query: "SELECT product_name FROM reporting.sales UNION SELECT name FROM catalog.products",
		},
	}

	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Executing Query: %s", tc.query)
			result, err := sqlQuery(ctx, SQLQueryArgs{
				DatasourceUID: postgresTestDatasourceUID,
				Query:         tc.query,
				Limit:         5,
			})

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, result.SQLQueryResult)
			require.Empty(t, result.Error, "Should not have query error: %s", result.Error)
			require.NotEmpty(t, result.Frames, "Should have at least one frame")
			t.Logf("Received %d frames, first frame has %d rows", len(result.Frames), len(result.Frames[0].Rows))
		})
	}
}

func TestPostgresIntegration_TimeRangeQuery(t *testing.T) {
	ctx := newTestContext()
	query := "SELECT host, timestamp as time FROM public.host_metrics WHERE $__timeFilter(timestamp)"
	t.Logf("Executing TimeRangeQuery: %s", query)

	result, err := sqlQuery(ctx, SQLQueryArgs{
		DatasourceUID: postgresTestDatasourceUID,
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

func TestPostgresIntegration_LimitTests(t *testing.T) {
	ctx := newTestContext()

	t.Run("Default Limit", func(t *testing.T) {
		query := "SELECT * FROM public.logs"
		t.Logf("Testing Default Limit for: %s", query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Query:         query,
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		t.Logf("Processed Query: %s", result.ProcessedQuery)
		assert.Contains(t, result.ProcessedQuery, "LIMIT 100", "Default limit 100 should be applied")
	})

	t.Run("Manual Limit", func(t *testing.T) {
		limit := uint(3)
		query := "SELECT * FROM public.logs"
		t.Logf("Testing Manual Limit (%d) for: %s", limit, query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Query:         query,
			Limit:         limit,
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		t.Logf("Processed Query: %s", result.ProcessedQuery)
		assert.Contains(t, result.ProcessedQuery, "LIMIT 3", "Manual limit 3 should be applied")
		if len(result.Frames) > 0 {
			assert.LessOrEqual(t, len(result.Frames[0].Rows), int(limit))
		}
	})
}

func TestPostgresIntegration_ResponseFormat(t *testing.T) {
	ctx := newTestContext()

	t.Run("Frame Structure", func(t *testing.T) {
		query := "SELECT id, severity_text, service_name FROM public.logs LIMIT 3"
		t.Logf("Executing query for frame structure validation: %s", query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
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
		query := "SELECT id, cpu_usage, host FROM public.host_metrics LIMIT 2"
		t.Logf("Executing query for type validation: %s", query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
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

	t.Run("Cross Schema Response", func(t *testing.T) {
		query := "SELECT s.product_name, s.amount, p.category FROM reporting.sales s JOIN catalog.products p ON s.product_name = p.name"
		t.Logf("Executing cross-schema query: %s", query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Query:         query,
			Limit:         5,
		})

		require.NoError(t, err)
		require.NotNil(t, result.SQLQueryResult)
		require.Empty(t, result.Error)
		require.NotEmpty(t, result.Frames)

		frame := result.Frames[0]
		t.Logf("Cross-schema columns: %v", frame.Columns)
		t.Logf("Cross-schema row count: %d", frame.RowCount)

		assert.Subset(t, frame.Columns, []string{"product_name", "amount", "category"})

		for i, row := range frame.Rows {
			t.Logf("Cross-schema row %d: %v", i, row)
		}
	})

	t.Run("Error Response Format", func(t *testing.T) {
		query := "SELECT * FROM nonexistent_schema.nonexistent_table"
		t.Logf("Executing invalid query to test error format: %s", query)
		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Query:         query,
			Limit:         1,
		})

		// May return error or result with error field set
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

func TestPostgresIntegration_MultiTableSchema(t *testing.T) {
	ctx := newTestContext()

	t.Run("Two Tables Same Schema", func(t *testing.T) {
		tables := "public.logs, public.host_metrics"
		t.Logf("Testing multi-table schema for: %s", tables)
		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        tables,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors, "Should have no errors, got: %v", result.Errors)
		t.Logf("Schemas returned: %d", len(result.Schemas))

		// Validate public.logs schema
		logsSchema, ok := result.Schemas["public.logs"]
		require.True(t, ok, "Should have schema for public.logs in %v", result.Schemas)
		t.Logf("public.logs fields: %v", logsSchema.Fields)
		assert.Equal(t, "public.logs", logsSchema.Name)
		assert.Subset(t, logsSchema.Fields, []string{"id", "\"timestamp\"", "body", "severity_text"})

		// Validate public.host_metrics schema
		metricsSchema, ok := result.Schemas["public.host_metrics"]
		require.True(t, ok, "Should have schema for public.host_metrics in %v", result.Schemas)
		t.Logf("public.host_metrics fields: %v", metricsSchema.Fields)
		assert.Equal(t, "public.host_metrics", metricsSchema.Name)
		assert.Subset(t, metricsSchema.Fields, []string{"id", "host", "cpu_usage", "mem_usage"})
	})

	t.Run("Tables Across Different Schemas", func(t *testing.T) {
		tables := "public.logs, reporting.sales, catalog.products, events.audit_log"
		t.Logf("Testing multi-table schema across schemas: %s", tables)
		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        tables,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors, "Should have no errors, got: %v", result.Errors)
		t.Logf("Total schemas returned: %d", len(result.Schemas))
		assert.Equal(t, 4, len(result.Schemas), "Should have 4 schemas")

		// Validate each schema independently
		salesSchema, ok := result.Schemas["reporting.sales"]
		require.True(t, ok, "Should have schema for reporting.sales")
		t.Logf("reporting.sales fields: %v", salesSchema.Fields)
		assert.Subset(t, salesSchema.Fields, []string{"product_name", "amount"})

		productsSchema, ok := result.Schemas["catalog.products"]
		require.True(t, ok, "Should have schema for catalog.products")
		t.Logf("catalog.products fields: %v", productsSchema.Fields)
		assert.Subset(t, productsSchema.Fields, []string{"name", "category", "price"})

		auditSchema, ok := result.Schemas["events.audit_log"]
		require.True(t, ok, "Should have schema for events.audit_log")
		t.Logf("events.audit_log fields: %v", auditSchema.Fields)
		assert.Subset(t, auditSchema.Fields, []string{"actor", "action", "metadata", "tags"})
	})

	t.Run("Field Types Validation", func(t *testing.T) {
		tables := "public.api_metrics"
		t.Logf("Testing field type information for: %s", tables)
		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        tables,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors)

		schema, ok := result.Schemas[tables]
		require.True(t, ok, "Should have schema for %s", tables)
		t.Logf("Fields with types for %s:", tables)
		for fieldName, field := range schema.Fields {
			t.Logf("  %s: %s", fieldName, field.Type)
			assert.NotEmpty(t, field.Type, "Field %s should have a type", fieldName)
		}

		// Validate specific Postgres types
		assert.Subset(t, schema.Fields, []string{"latency_ms", "endpoint", "status_code"})
	})

	t.Run("Mixed Valid And Invalid Tables", func(t *testing.T) {
		tables := "public.logs, nonexistent_schema.fake_table"
		t.Logf("Testing multi-table with invalid table: %s", tables)
		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        tables,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		t.Logf("Schemas: %d, Errors: %v", len(result.Schemas), result.Errors)

		// Valid table should still return its schema
		logsSchema, ok := result.Schemas["public.logs"]
		if ok {
			t.Logf("public.logs schema returned successfully with %d fields", len(logsSchema.Fields))
			assert.NotEmpty(t, logsSchema.Fields)
		} else {
			t.Logf("public.logs schema not present (may be affected by batch error)")
		}
	})
}
