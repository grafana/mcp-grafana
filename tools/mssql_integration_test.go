//go:build integration

package tools

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mssqlTestDatasourceUID = "mssql"

func TestMSSQLDatabaseQuery(t *testing.T) {
	ctx := newTestContext()
	t.Run("should list databases from datasource", func(t *testing.T) {

		result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
			DatasourceUID: mssqlTestDatasourceUID,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Databases)

		found := false
		for _, db := range result.Databases {
			if db == "infrastructure_logs" {
				found = true
				break
			}
		}
		//GPT : use assert.subset

		assert.True(t, found, "should contain infrastructure_logs database")
	})

	t.Run("should return error for invalid datasource", func(t *testing.T) {

		result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
			DatasourceUID: "nonexistent-datasource",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.Error(t, err, "should return error for invalid datasource")
	})

	t.Run("should list tables from database", func(t *testing.T) {

		result, err := listSQLTables(ctx, ListSQLTablesArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      "infrastructure_logs",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)

		//GPT: assert.subset

		assert.Subset(t, result.Tables, []string{"dbo.logs", "dbo.host_metrics"}, "should contain expected tables")
	})

}

func TestMSSQLRawQuery(t *testing.T) {
	ctx := newTestContext()
	t.Run("should execute sql queries", func(t *testing.T) {

		queries := []struct {
			name  string
			query string
		}{
			{
				name:  "Simple Select",
				query: "SELECT TOP 5 * FROM infrastructure_logs.dbo.logs",
			},
			{
				name:  "Join",
				query: "SELECT TOP 5 l.id, h.host FROM infrastructure_logs.dbo.logs l JOIN infrastructure_logs.dbo.host_metrics h ON l.id = h.id",
			},
			{
				name:  "Subquery",
				query: "SELECT * FROM (SELECT TOP 5 * FROM infrastructure_logs.dbo.logs) AS service_logs",
			},
		}

		for _, tc := range queries {

			t.Run(tc.name, func(t *testing.T) {

				result, err := sqlQuery(ctx, SQLQueryArgs{
					DatasourceUID: mssqlTestDatasourceUID,
					Query:         tc.query,
				})

				t.Logf("result: %v", result)
				t.Logf("error: %v", err)

				require.NoError(t, err)
				require.NotNil(t, result.Result)
				require.Empty(t, result.Result.Error)
				assert.Greaterf(t, len(result.Result.Frames), 0, "should contain at least one frame")
				assert.Greaterf(t, len(result.Result.Frames[0].Rows), 0, "should contain at least one row for first frame")
			})
		}
	})
	t.Run("should execute time range query", func(t *testing.T) {

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         "SELECT * FROM infrastructure_logs.dbo.logs WHERE $__timeFilter(timestamp)",
			Start:         "now-6h",
			End:           "now",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result.Result)
		require.NotEmpty(t, result.Result.Frames)

		assert.Greaterf(t, len(result.Result.Frames[0].Rows), 0, "should contain at least one row")

		assert.NotEmpty(t, result.ProcessedQuery, "should contain processed query")
	})
}

func TestMSSQLTableSchemaQuery(t *testing.T) {

	ctx := newTestContext()

	t.Run("should return table schema", func(t *testing.T) {

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      "infrastructure_logs",
			Tables:        "dbo.logs",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Errors)

		schema := result.Schemas["dbo.logs"]
		fields := getSQLSchemaFieldKeys(schema.Fields)

		assert.Subset(t, fields, []string{"id", "timestamp", "body"}, "should contain expected columns")
	})
	t.Run("should return schemas for multiple tables in same database", func(t *testing.T) {

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      "infrastructure_logs",
			Tables:        "dbo.logs, dbo.host_metrics",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)

		logSchema := result.Schemas["dbo.logs"]
		logFields := getSQLSchemaFieldKeys(logSchema.Fields)

		assert.Subset(t, logFields, []string{"id", "timestamp"}, "should contain expected log columns")

		metricSchema := result.Schemas["dbo.host_metrics"]
		metricFields := getSQLSchemaFieldKeys(metricSchema.Fields)

		assert.Subset(t, metricFields, []string{"id", "host"}, "should contain expected metric columns")
	})

	t.Run("should return schema for table in different database", func(t *testing.T) {

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      "shop_performance",
			Tables:        "dbo.api_metrics",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)

		apiSchema := result.Schemas["dbo.api_metrics"]
		apiFields := getSQLSchemaFieldKeys(apiSchema.Fields)

		assert.Subset(t, apiFields, []string{
			"endpoint",
			"status_code",
			"latency_ms",
		}, "should contain table columns")
	})

	t.Run("should return valid field types for schema fields", func(t *testing.T) {

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Database:      "infrastructure_logs",
			Tables:        "dbo.logs",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)

		schema := result.Schemas["dbo.logs"]

		for fieldName, field := range schema.Fields {
			assert.NotEmpty(t, field.Type, "field %s should have a valid type", fieldName)
		}

		fields := getSQLSchemaFieldKeys(schema.Fields)
		assert.Subset(t, fields, []string{"id", "body", "severity_text"}, "should contain expected columns")
	})
}

func TestMSSQLConcurrency(t *testing.T) {
	ctx := newTestContext()

	t.Run("should handle concurrent queries", func(t *testing.T) {

		const workers = 5
		const queriesPerWorker = 3

		var wg sync.WaitGroup
		wg.Add(workers)

		for i := 0; i < workers; i++ {

			go func(workerID int) {

				defer wg.Done()

				for j := 0; j < queriesPerWorker; j++ {

					result, err := sqlQuery(ctx, SQLQueryArgs{
						DatasourceUID: mssqlTestDatasourceUID,
						Query:         "SELECT 1 as val",
					})

					t.Logf("worker %d query %d result: %v", workerID, j, result)
					t.Logf("worker %d query %d error: %v", workerID, j, err)

					require.NoError(t, err, "should not return error for concurrent query")

					require.NotNil(t, result, "should return query result")
					require.NotNil(t, result.Result, "should return query execution result")

					assert.Empty(t, result.Result.Error, "should not contain query execution error")

					assert.NotEmpty(
						t,
						result.Result.Frames,
						"should return at least one frame",
					)

					frame := result.Result.Frames[0]

					assert.NotNil(
						t,
						frame.Columns,
						"frame should contain columns",
					)

					assert.NotNil(
						t,
						frame.Rows,
						"frame should contain rows",
					)

				}

			}(i)
		}

		wg.Wait()
	})
}

func TestMSSQLLimits(t *testing.T) {

	ctx := newTestContext()

	t.Run("should apply default query limit", func(t *testing.T) {

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         "SELECT * FROM infrastructure_logs.dbo.logs",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)

		assert.Greaterf(t, len(result.Result.Frames[0].Rows), 0, "should contain at least one row")

		assert.Contains(t, result.ProcessedQuery, "TOP 100", "should enforce default limit")
	})

	t.Run("should apply explicit limit", func(t *testing.T) {

		limit := uint(5)

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         "SELECT * FROM infrastructure_logs.dbo.logs",
			Limit:         limit,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)

		assert.Greaterf(t, len(result.Result.Frames[0].Rows), 0, "should contain at least one row")
	})
}

func TestMSSQLSchemaAndResponse(t *testing.T) {

	ctx := newTestContext()

	t.Run("should return valid frame structure", func(t *testing.T) {

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         "SELECT TOP 3 id, severity_text, service_name FROM infrastructure_logs.dbo.logs",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)

		frame := result.Result.Frames[0]

		assert.Subset(t, frame.Columns, []string{"id", "severity_text", "service_name"}, "should contain expected columns")

		assert.Equal(t, frame.RowCount, uint(len(frame.Rows)), "row count should match rows")
	})

	t.Run("should preserve numeric and string types", func(t *testing.T) {

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mssqlTestDatasourceUID,
			Query:         "SELECT TOP 1 id, host FROM infrastructure_logs.dbo.host_metrics",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)

		assert.Greaterf(t, len(result.Result.Frames[0].Rows), 0, "should contain at least one row")

		row := result.Result.Frames[0].Rows[0]

		_, isString := row["host"].(string)

		assert.True(t, isString, "host field should be string")
	})
}
