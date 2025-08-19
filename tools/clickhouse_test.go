// Requires a Grafana instance running on localhost:3000,
// with a ClickHouse datasource provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClickHouseTools(t *testing.T) {
	t.Run("list clickhouse databases", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listClickHouseDatabases(ctx, ListClickHouseDatabasesParams{
			DatasourceUID: "clickhouse",
		})
		require.NoError(t, err)
		assert.Greater(t, len(result), 0, "Expected at least one database")

		// ClickHouse should at least have the 'system' database
		assert.Contains(t, result, "system", "Expected 'system' database to be present")
	})

	t.Run("list clickhouse tables", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listClickHouseTables(ctx, ListClickHouseTablesParams{
			DatasourceUID: "clickhouse",
			Database:      "system",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.Greater(t, len(result), 0, "Expected at least one table in system database")

		// Check that we got proper table metadata
		for _, table := range result {
			assert.Equal(t, "system", table.Database)
			assert.NotEmpty(t, table.Name)
			assert.NotEmpty(t, table.Engine)
		}
	})

	t.Run("list clickhouse tables with like filter", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listClickHouseTables(ctx, ListClickHouseTablesParams{
			DatasourceUID: "clickhouse",
			Database:      "system",
			Like:          "columns",
			Limit:         5,
		})
		require.NoError(t, err)

		// Should find the 'columns' table
		found := false
		for _, table := range result {
			if table.Name == "columns" {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected to find 'columns' table with LIKE filter")
	})

	t.Run("describe clickhouse table", func(t *testing.T) {
		ctx := newTestContext()
		result, err := describeClickHouseTable(ctx, DescribeClickHouseTableParams{
			DatasourceUID: "clickhouse",
			Database:      "system",
			Table:         "databases",
		})
		require.NoError(t, err)
		assert.Greater(t, len(result), 0, "Expected at least one column in system.databases table")

		// Check that we got proper column metadata
		nameFound := false
		for _, col := range result {
			assert.Equal(t, "system", col.Database)
			assert.Equal(t, "databases", col.Table)
			assert.NotEmpty(t, col.Name)
			assert.NotEmpty(t, col.Type)
			if col.Name == "name" {
				nameFound = true
			}
		}
		assert.True(t, nameFound, "Expected 'name' column in system.databases table")
	})

	t.Run("query clickhouse basic select", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryClickHouse(ctx, QueryClickHouseParams{
			DatasourceUID: "clickhouse",
			Query:         "SELECT name FROM system.databases LIMIT 5",
			Limit:         5,
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Greater(t, len(result.Columns), 0, "Expected at least one column")
		assert.Equal(t, "name", result.Columns[0])
		assert.GreaterOrEqual(t, len(result.Rows), 1, "Expected at least one row")
	})

	t.Run("query clickhouse show databases", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryClickHouse(ctx, QueryClickHouseParams{
			DatasourceUID: "clickhouse",
			Query:         "SHOW DATABASES",
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Greater(t, len(result.Columns), 0, "Expected at least one column")
		assert.GreaterOrEqual(t, len(result.Rows), 1, "Expected at least one database")
	})

	t.Run("query clickhouse describe table", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryClickHouse(ctx, QueryClickHouseParams{
			DatasourceUID: "clickhouse",
			Query:         "DESCRIBE system.tables",
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Greater(t, len(result.Columns), 0, "Expected at least one column from DESCRIBE")
		assert.GreaterOrEqual(t, len(result.Rows), 1, "Expected at least one row from DESCRIBE")
	})

	t.Run("query clickhouse with limit", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryClickHouse(ctx, QueryClickHouseParams{
			DatasourceUID: "clickhouse",
			Query:         "SELECT name FROM system.tables",
			Limit:         3,
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.LessOrEqual(t, len(result.Rows), 3, "Expected no more than 3 rows due to limit")
	})

	t.Run("query clickhouse reject unsafe queries", func(t *testing.T) {
		ctx := newTestContext()

		unsafeQueries := []string{
			"INSERT INTO test VALUES (1)",
			"UPDATE test SET x = 1",
			"DELETE FROM test",
			"CREATE TABLE test (x Int32)",
			"DROP TABLE test",
			"TRUNCATE TABLE test",
		}

		for _, query := range unsafeQueries {
			t.Run("reject "+query, func(t *testing.T) {
				_, err := queryClickHouse(ctx, QueryClickHouseParams{
					DatasourceUID: "clickhouse",
					Query:         query,
				})
				assert.Error(t, err, "Expected error for unsafe query: %s", query)
				assert.Contains(t, err.Error(), "only SELECT, SHOW, and DESCRIBE queries are allowed")
			})
		}
	})

	t.Run("query clickhouse with invalid datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := queryClickHouse(ctx, QueryClickHouseParams{
			DatasourceUID: "nonexistent",
			Query:         "SELECT 1",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "datasource")
	})

	t.Run("query clickhouse enforce limits", func(t *testing.T) {
		ctx := newTestContext()

		// Test default limit
		result, err := queryClickHouse(ctx, QueryClickHouseParams{
			DatasourceUID: "clickhouse",
			Query:         "SELECT number FROM system.numbers",
			Limit:         0, // Should use default
		})
		require.NoError(t, err)
		// Default limit is enforced by max_result_rows parameter
		assert.NotNil(t, result)

		// Test max limit enforcement
		result, err = queryClickHouse(ctx, QueryClickHouseParams{
			DatasourceUID: "clickhouse",
			Query:         "SELECT number FROM system.numbers",
			Limit:         2000, // Should be capped to MaxClickHouseLimit (1000)
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}

// Test helper functions

func TestClickHouseHelperFunctions(t *testing.T) {
	t.Run("toString helper", func(t *testing.T) {
		assert.Equal(t, "hello", toString("hello"))
		assert.Equal(t, "123", toString(123))
		assert.Equal(t, "123.45", toString(123.45))
		assert.Equal(t, "", toString(nil))
	})

	t.Run("toUint64 helper", func(t *testing.T) {
		assert.Equal(t, uint64(123), toUint64(123))
		assert.Equal(t, uint64(123), toUint64(int64(123)))
		assert.Equal(t, uint64(123), toUint64(float64(123.0)))
		assert.Equal(t, uint64(123), toUint64(uint64(123)))
		assert.Equal(t, uint64(123), toUint64("123"))
		assert.Equal(t, uint64(0), toUint64("invalid"))
		assert.Equal(t, uint64(0), toUint64(nil))
	})

	t.Run("enforceClickHouseLimit", func(t *testing.T) {
		assert.Equal(t, DefaultClickHouseLimit, enforceClickHouseLimit(0))
		assert.Equal(t, DefaultClickHouseLimit, enforceClickHouseLimit(-1))
		assert.Equal(t, 50, enforceClickHouseLimit(50))
		assert.Equal(t, MaxClickHouseLimit, enforceClickHouseLimit(2000))
	})
}
