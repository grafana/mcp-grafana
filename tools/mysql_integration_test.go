//go:build integration

package tools

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mysqlTestDatasourceUID = "mysql"

func TestMySQLDatabaseQuery(t *testing.T) {
	ctx := newTestContext()

	t.Run("should list databases", func(t *testing.T) {

		result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
			DatasourceUID: mysqlTestDatasourceUID,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err, "should not return error while listing databases")
		require.NotNil(t, result, "should return database result")
		require.NotEmpty(t, result.Databases, "should return at least one database")

		assert.Contains(
			t,
			result.Databases,
			"infrastructure_logs",
			"should contain infrastructure_logs database",
		)
	})

	t.Run("should return error for invalid datasource", func(t *testing.T) {

		result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
			DatasourceUID: "nonexistent-datasource",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.Error(t, err, "should error with invalid datasource")
	})

	t.Run("should return error for wrong datasource type", func(t *testing.T) {

		result, err := listSQLDatabases(ctx, ListSQLDatabaseArgs{
			DatasourceUID: "prometheus",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.Error(t, err, "should error with wrong datasource type")

		assert.Contains(
			t,
			err.Error(),
			"is not an SQL Datasource",
			"should indicate datasource type mismatch",
		)
	})
}

func TestMySQLTableSchemaQuery(t *testing.T) {
	ctx := newTestContext()

	t.Run("should list tables from database", func(t *testing.T) {

		result, err := listSQLTables(ctx, ListSQLTablesArgs{
			DatasourceUID: mysqlTestDatasourceUID,
			Database:      "infrastructure_logs",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)

		assert.Subset(t, result.Tables, []string{"logs", "host_metrics"}, "should contain expected tables")
	})

	t.Run("should return schema for logs table", func(t *testing.T) {

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mysqlTestDatasourceUID,
			Database:      "infrastructure_logs",
			Tables:        "logs",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)

		schema := result.Schemas["logs"]

		assert.Equal(t, "logs", schema.Name, "should return logs schema")

		fields := getSQLSchemaFieldKeys(schema.Fields)

		assert.Subset(
			t,
			fields,
			[]string{"id", "timestamp", "body", "service_name", "severity_text", "trace_id"},
			"should contain expected log columns",
		)
	})

	t.Run("should return schemas for multiple tables", func(t *testing.T) {

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mysqlTestDatasourceUID,
			Database:      "infrastructure_logs",
			Tables:        "logs, host_metrics",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)

		logFields := getSQLSchemaFieldKeys(result.Schemas["logs"].Fields)

		assert.Subset(
			t,
			logFields,
			[]string{"id", "timestamp", "body"},
			"should contain expected logs columns",
		)

		metricFields := getSQLSchemaFieldKeys(result.Schemas["host_metrics"].Fields)

		assert.Subset(
			t,
			metricFields,
			[]string{"id", "host", "cpu_usage", "mem_usage"},
			"should contain expected metrics columns",
		)
	})

	t.Run("should return schema for table in another database", func(t *testing.T) {

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mysqlTestDatasourceUID,
			Database:      "infrastructure_logs",
			Tables:        "host_metrics",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Schemas["host_metrics"])

		fields := getSQLSchemaFieldKeys(result.Schemas["host_metrics"].Fields)

		assert.Subset(
			t,
			fields,
			[]string{"id", "host", "cpu_usage", "mem_usage"},
			"should contain host_metrics fields",
		)
	})

	t.Run("should validate schema field types", func(t *testing.T) {

		result, err := listSQLTableSchema(ctx, GetSQLTableSchemaArgs{
			DatasourceUID: mysqlTestDatasourceUID,
			Database:      "infrastructure_logs",
			Tables:        "logs",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)

		schema := result.Schemas["logs"]

		for fieldName, field := range schema.Fields {
			assert.NotEmpty(
				t,
				field.Type,
				"field %s should have a valid type",
				fieldName,
			)
		}
	})
}

func TestMySQLRawQuery(t *testing.T) {
	ctx := newTestContext()

	t.Run("should execute sql queries", func(t *testing.T) {

		queries := []struct {
			name  string
			query string
		}{
			{
				name:  "Simple Select",
				query: "SELECT * FROM infrastructure_logs.logs LIMIT 5",
			},
			{
				name:  "Join",
				query: "SELECT l.id, h.host FROM infrastructure_logs.logs l JOIN infrastructure_logs.host_metrics h ON l.id = h.id LIMIT 5",
			},
			{
				name:  "CTE",
				query: "WITH service_logs AS (SELECT * FROM infrastructure_logs.logs) SELECT * FROM service_logs LIMIT 5",
			},
		}

		for _, tc := range queries {

			t.Run(tc.name, func(t *testing.T) {

				result, err := sqlQuery(ctx, SQLQueryArgs{
					DatasourceUID: mysqlTestDatasourceUID,
					Query:         tc.query,
				})

				t.Logf("result: %v", result)
				t.Logf("error: %v", err)

				require.NoError(t, err)
				require.NotNil(t, result.Result)
				require.Empty(t, result.Result.Error)
				assert.NotEmpty(t, result.Result.Frames, "should return at least one frame")
			})
		}
	})

	t.Run("should execute time range query", func(t *testing.T) {

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mysqlTestDatasourceUID,
			Query:         "SELECT * FROM infrastructure_logs.logs WHERE $__timeFilter(timestamp) LIMIT 10",
			Start:         "now-6h",
			End:           "now",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result.Result)
		assert.NotEmpty(t, result.Result.Frames, "should return at least one frame")
		assert.NotEmpty(
			t,
			result.ProcessedQuery,
			"should return processed query",
		)
	})
}

func TestMySQLConcurrency(t *testing.T) {

	ctx := newTestContext()

	t.Run("should handle concurrent sql queries", func(t *testing.T) {

		const workers = 5
		const queriesPerWorker = 3

		var wg sync.WaitGroup
		wg.Add(workers)

		for i := 0; i < workers; i++ {

			go func(workerID int) {

				defer wg.Done()

				for j := 0; j < queriesPerWorker; j++ {

					result, err := sqlQuery(ctx, SQLQueryArgs{
						DatasourceUID: mysqlTestDatasourceUID,
						Query:         "SELECT 1",
					})

					t.Logf("worker %d query %d result: %v", workerID, j, result)
					t.Logf("worker %d query %d error: %v", workerID, j, err)

					require.NoError(t, err)
					require.NotNil(t, result)
					require.NotNil(t, result.Result)

					assert.Empty(t, result.Result.Error, "should not contain query execution error")
					assert.NotEmpty(t, result.Result.Frames, "should return frames")
					assert.NotEmpty(t, result.Result.Frames[0].Rows, "should return at least one row")
					assert.Equal(t, len(result.Result.Frames[0].Rows), int(result.Result.Frames[0].RowCount), "row count should match rows length")
				}

			}(i)
		}

		wg.Wait()
	})
}

func TestMySQLLimits(t *testing.T) {
	ctx := newTestContext()

	t.Run("should apply default query limit", func(t *testing.T) {

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mysqlTestDatasourceUID,
			Query:         "SELECT * FROM infrastructure_logs.logs",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotEmpty(t, result.Result.Frames, "should return at least one frame")
		require.NotEmpty(t, result.Result.Frames[0].Rows, "should return at least one row")
		assert.Contains(
			t,
			result.ProcessedQuery,
			"LIMIT 100",
			"should enforce default limit",
		)
	})

	t.Run("should apply explicit query limit", func(t *testing.T) {

		limit := uint(5)

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mysqlTestDatasourceUID,
			Query:         "SELECT * FROM infrastructure_logs.logs",
			Limit:         limit,
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotNil(t, result.Result)

		require.NotEmpty(t, result.Result.Frames, "should return at least one frame")
		frame := result.Result.Frames[0]
		require.NotEmpty(t, frame.Rows, "should return at least one row")

		assert.LessOrEqual(
			t,
			len(frame.Rows),
			int(limit),
			"should respect explicit query limit",
		)
	})
}

func TestMySQLSchemaAndResponse(t *testing.T) {

	ctx := newTestContext()

	t.Run("should validate frame structure", func(t *testing.T) {

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mysqlTestDatasourceUID,
			Query:         "SELECT id, severity_text, service_name FROM infrastructure_logs.logs LIMIT 3",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotEmpty(t, result.Result.Frames, "should return at least one frame")
		require.NotEmpty(t, result.Result.Frames[0].Rows, "should return at least one row")

		frame := result.Result.Frames[0]

		assert.Subset(
			t,
			frame.Columns,
			[]string{"id", "severity_text", "service_name"},
			"should contain expected columns",
		)

		assert.Equal(
			t,
			frame.RowCount,
			uint(len(frame.Rows)),
			"row count should match rows",
		)
	})

	t.Run("should preserve numeric and string types", func(t *testing.T) {

		result, err := sqlQuery(ctx, SQLQueryArgs{
			DatasourceUID: mysqlTestDatasourceUID,
			Query:         "SELECT id, host FROM infrastructure_logs.host_metrics LIMIT 1",
		})

		t.Logf("result: %v", result)
		t.Logf("error: %v", err)

		require.NoError(t, err)
		require.NotEmpty(t, result.Result.Frames[0].Rows, "should return at least one row")
		row := result.Result.Frames[0].Rows[0]

		_, isString := row["host"].(string)

		assert.True(
			t,
			isString,
			"host field should be string",
		)
	})
}
