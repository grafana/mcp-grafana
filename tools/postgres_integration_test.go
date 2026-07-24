//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const postgresTestDatasourceUID = "postgres"

func TestPostgresDatabaseQuery(t *testing.T) {
	ctx := newTestContext()

	t.Run("should list databases", func(t *testing.T) {
		result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
			DatasourceUID: postgresTestDatasourceUID,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Databases)

		assert.Subset(t, result.Databases, []string{"infrastructure_logs", "application_db"}, "should find logs and app databases")
	})

	t.Run("should fail with invalid datasource", func(t *testing.T) {
		result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
			DatasourceUID: "nonexistent-datasource",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.Error(t, err)
	})

	t.Run("should fail with wrong datasource type", func(t *testing.T) {
		result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
			DatasourceUID: "prometheus",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not an SQL Datasource")
	})

	t.Run("should list tables across schemas", func(t *testing.T) {
		result, err := listSQLTables(ctx, ListSQLTablesArgs{
			DatasourceUID: postgresTestDatasourceUID,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)

		require.NotEmpty(t, result.Tables, "should return at least one table")
		assert.Subset(t, result.Tables, []string{"logs", "reporting.sales", "catalog.products", "events.audit_log"}, "should contain expected tables")
	})
}

func TestPostgresTableSchemaQuery(t *testing.T) {
	ctx := newTestContext()
	t.Run("should fetch default schema table", func(t *testing.T) {
		table := "logs"

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        table,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors)

		schema, ok := result.Schemas[table]
		require.True(t, ok)

		fieldNames := getSQLSchemaFieldKeys(schema.Fields)

		assert.Subset(t, fieldNames, []string{
			"id",
			"\"timestamp\"",
			"body",
			"severity_text",
		})
	})

	t.Run("should fetch custom schema table", func(t *testing.T) {
		table := "reporting.sales"

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        table,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors)

		schema := result.Schemas[table]

		fieldNames := getSQLSchemaFieldKeys(schema.Fields)

		assert.Subset(t, fieldNames, []string{
			"id",
			"product_name",
			"amount",
		})
	})

	t.Run("should fetch schemas across multiple schemas", func(t *testing.T) {
		tables := "public.logs, reporting.sales, catalog.products, events.audit_log"

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        tables,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors)

		assert.Equal(t, 4, len(result.Schemas))
	})

	t.Run("should validate field types", func(t *testing.T) {
		table := "public.api_metrics"

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        table,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)

		schema := result.Schemas[table]

		for fieldName, field := range schema.Fields {
			assert.NotEmpty(t, field.Type, "field %s should have type", fieldName)
		}
	})

	t.Run("should handle mixed valid and invalid tables", func(t *testing.T) {
		tables := "public.logs, nonexistent_schema.fake_table"

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Tables:        tables,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)

		schema := result.Schemas["public.logs"]
		assert.Empty(t, result.Schemas["nonexistent_schema.fake_table"], "should not return schema for non-existent table")
		assert.NotEmpty(t, schema.Fields, "should return logs fields")
	})
}

func TestPostgresRawQuery(t *testing.T) {
	ctx := newTestContext()

	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "simple select",
			query: "SELECT * FROM public.logs LIMIT 5",
		},
		{
			name:  "cross schema join",
			query: "SELECT s.product_name, s.amount FROM reporting.sales s JOIN catalog.products p ON s.product_name = p.name",
		},
		{
			name:  "jsonb query",
			query: "SELECT actor, metadata->>'ip' as ip FROM events.audit_log WHERE metadata->>'ip' IS NOT NULL",
		},
		{
			name:  "array type query",
			query: "SELECT actor, tags FROM events.audit_log",
		},
		{
			name:  "union across schemas",
			query: "SELECT product_name FROM reporting.sales UNION SELECT name FROM catalog.products",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {

			result, err := sqlQuery(ctx, SQLQueryArgs{
				DatasourceUID: postgresTestDatasourceUID,
				Query:         tc.query,
				Limit:         5,
			})

			t.Logf("result: %v", result)
			t.Logf("error: %v", err)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, result.Result)
			require.Empty(t, result.Result.Error)
			assert.NotEmpty(t, result.Result.Frames, "should return at least one frame")
			require.NotEmpty(t, result.Result.Frames[0].Rows, "should return at least one row")

		})
	}

	t.Run("should execute time range query", func(t *testing.T) {

		query := "SELECT host, timestamp as time FROM public.host_metrics WHERE $__timeFilter(timestamp)"

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Query:         query,
			Start:         "now-1d",
			End:           "now",
			Limit:         5,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result.Result)
		require.Empty(t, result.Result.Error)
		assert.NotEmpty(t, result.Result.Frames, "should return at least one frame")
		require.NotEmpty(t, result.Result.Frames[0].Rows, "should return at least one row")

	})
}

func TestPostgresLimits(t *testing.T) {
	ctx := newTestContext()

	t.Run("should apply default limit", func(t *testing.T) {

		query := "SELECT * FROM public.logs"

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Query:         query,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotEmpty(t, result.Result.Frames, "should return at least one frame")
		require.NotEmpty(t, result.Result.Frames[0].Rows, "should return at least one row")
		assert.LessOrEqual(t, len(result.Result.Frames[0].Rows), 100, "should respect default limit")
		assert.Contains(t, result.ProcessedQuery, "LIMIT 100")
	})

	t.Run("should apply manual limit", func(t *testing.T) {

		limit := uint(3)

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Query:         "SELECT * FROM public.logs",
			Limit:         limit,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result.Result)
		require.NotEmpty(t, result.Result.Frames)

		rows := result.Result.Frames[0].Rows

		assert.LessOrEqual(t, len(rows), int(limit))
	})
}

func TestPostgresSchemaAndResponse(t *testing.T) {
	ctx := newTestContext()

	t.Run("should validate frame structure", func(t *testing.T) {

		query := "SELECT id, severity_text, service_name FROM public.logs LIMIT 3"

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Query:         query,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result.Result)
		require.NotEmpty(t, result.Result.Frames, "should return at least one frame")
		frame := result.Result.Frames[0]
		require.NotEmpty(t, frame.Rows, "should return at least one row")
		assert.Subset(t, frame.Columns, []string{
			"id",
			"severity_text",
			"service_name",
		}, "frame should contain expected columns")
		assert.Equal(t, frame.RowCount, uint(len(frame.Rows)), "row count should match rows length")
	})

	t.Run("should validate numeric and string types", func(t *testing.T) {

		query := "SELECT id, host FROM public.host_metrics LIMIT 1"

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: postgresTestDatasourceUID,
			Query:         query,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result.Result)
		require.NotEmpty(t, result.Result.Frames)

		require.NotEmpty(t, result.Result.Frames[0].Rows, "should return at least one row")
		row := result.Result.Frames[0].Rows[0]

		_, isString := row["host"].(string)

		assert.True(t, isString, "host field should be a string")
	})
}
