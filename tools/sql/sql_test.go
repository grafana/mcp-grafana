//go:build unit

package sql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSQLIdentifier(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		wantErr bool
	}{
		{name: "", field: "database", wantErr: false},
		{name: "default", field: "database", wantErr: false},
		{name: "table_1", field: "table", wantErr: false},
		{name: "MyDB123", field: "database", wantErr: false},
		{name: "default' OR 1=1 --", field: "database", wantErr: true},
		{name: "table-name", field: "table", wantErr: true},
		{name: "table name", field: "table", wantErr: true},
		{name: "system.tables", field: "table", wantErr: true},
	}

	for _, tt := range tests {
		err := ValidateSQLIdentifier(tt.name, tt.field)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateSQLIdentifier(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestEnforceSQLLimit(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		limit    int
		expected string
	}{
		{"no limit - append default", "SELECT * FROM t", 0, "SELECT * FROM t LIMIT 100"},
		{"custom limit", "SELECT * FROM t", 50, "SELECT * FROM t LIMIT 50"},
		{"exceeds max - capped", "SELECT * FROM t", 5000, "SELECT * FROM t LIMIT 1000"},
		{"existing limit below max", "SELECT * FROM t LIMIT 50", 100, "SELECT * FROM t LIMIT 50"},
		{"existing limit exceeds max", "SELECT * FROM t LIMIT 5000", 100, "SELECT * FROM t LIMIT 1000"},
		{"trailing semicolon", "SELECT * FROM t;", 100, "SELECT * FROM t LIMIT 100"},
		{"case insensitive", "SELECT * FROM t limit 50", 100, "SELECT * FROM t limit 50"},
		{"negative uses default", "SELECT * FROM t", -1, "SELECT * FROM t LIMIT 100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EnforceSQLLimit(tt.query, tt.limit, DefaultSQLLimit, MaxSQLLimit)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMSSQLEnforceLimit(t *testing.T) {
	d := &mssqlDialect{}
	tests := []struct {
		name     string
		query    string
		limit    int
		expected string
	}{
		{"injects TOP with default", "SELECT * FROM t", 0, "SELECT TOP 100 * FROM t"},
		{"injects TOP with custom limit", "SELECT * FROM t", 50, "SELECT TOP 50 * FROM t"},
		{"caps at max", "SELECT * FROM t", 5000, "SELECT TOP 1000 * FROM t"},
		{"preserves DISTINCT", "SELECT DISTINCT col FROM t", 100, "SELECT DISTINCT TOP 100 col FROM t"},
		{"existing TOP below max", "SELECT TOP 50 * FROM t", 100, "SELECT TOP 50 * FROM t"},
		{"existing TOP exceeds max", "SELECT TOP 5000 * FROM t", 100, "SELECT TOP 1000 * FROM t"},
		{"existing DISTINCT TOP exceeds max", "SELECT DISTINCT TOP 5000 col FROM t", 100, "SELECT DISTINCT TOP 1000 col FROM t"},
		{"trailing semicolon", "SELECT * FROM t;", 100, "SELECT TOP 100 * FROM t"},
		{"negative uses default", "SELECT * FROM t", -1, "SELECT TOP 100 * FROM t"},
		{"CTE passes through without limit", "WITH cte AS (SELECT * FROM t) SELECT * FROM cte", 100, "WITH cte AS (SELECT * FROM t) SELECT * FROM cte"},
		{"OFFSET FETCH passes through", "SELECT * FROM t ORDER BY id OFFSET 10 ROWS FETCH NEXT 20 ROWS ONLY", 100, "SELECT * FROM t ORDER BY id OFFSET 10 ROWS FETCH NEXT 20 ROWS ONLY"},
		{"parenthesized TOP preserved", "SELECT TOP (50) * FROM t", 100, "SELECT TOP (50) * FROM t"},
		{"parenthesized TOP exceeds max", "SELECT TOP (5000) * FROM t", 100, "SELECT TOP 1000 * FROM t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.EnforceLimit(tt.query, tt.limit)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMySQLEnforceLimit(t *testing.T) {
	d := &mysqlDialect{}
	tests := []struct {
		name     string
		query    string
		limit    int
		expected string
	}{
		{"normal SELECT gets limit", "SELECT * FROM t", 0, "SELECT * FROM t LIMIT 100"},
		{"SHOW TABLES skipped", "SHOW TABLES", 100, "SHOW TABLES"},
		{"DESCRIBE skipped", "DESCRIBE my_table", 100, "DESCRIBE my_table"},
		{"SET skipped", "SET @var = 1", 100, "SET @var = 1"},
		{"show lowercase skipped", "show tables", 100, "show tables"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.EnforceLimit(tt.query, tt.limit)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDialectRegistry(t *testing.T) {
	// ClickHouse
	d, err := DialectFor(ClickHouseDatasourceType)
	require.NoError(t, err)
	assert.Equal(t, 1, d.DefaultFormat())

	// Snowflake
	d, err = DialectFor(SnowflakeDatasourceType)
	require.NoError(t, err)
	assert.Equal(t, 1, d.DefaultFormat())

	// Athena
	d, err = DialectFor(AthenaDatasourceType)
	require.NoError(t, err)
	assert.Equal(t, 1, d.DefaultFormat())

	// MySQL
	d, err = DialectFor(MySQLDatasourceType)
	require.NoError(t, err)
	assert.Equal(t, "table", d.DefaultFormat())

	// PostgreSQL (both types)
	d, err = DialectFor(PostgresDatasourceType)
	require.NoError(t, err)
	assert.Equal(t, "table", d.DefaultFormat())

	d, err = DialectFor(PostgresLegacyDatasourceType)
	require.NoError(t, err)
	assert.Equal(t, "table", d.DefaultFormat())

	// MSSQL
	d, err = DialectFor(MSSQLDatasourceType)
	require.NoError(t, err)
	assert.Equal(t, "table", d.DefaultFormat())

	// Unknown
	_, err = DialectFor("unknown-datasource")
	assert.Error(t, err)
}

func TestMySQLSubstituteMacros(t *testing.T) {
	d := &mysqlDialect{}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)

	t.Run("timeFilter", func(t *testing.T) {
		result := d.SubstituteMacros("SELECT * FROM t WHERE $__timeFilter(ts)", from, to)
		assert.Contains(t, result, "ts BETWEEN '2026-01-01 00:00:00' AND '2026-01-01 01:00:00'")
		assert.NotContains(t, result, "$__timeFilter")
	})

	t.Run("timeFrom and timeTo", func(t *testing.T) {
		result := d.SubstituteMacros("SELECT * FROM t WHERE ts > $__timeFrom() AND ts < $__timeTo()", from, to)
		assert.Contains(t, result, "'2026-01-01 00:00:00'")
		assert.Contains(t, result, "'2026-01-01 01:00:00'")
	})

	t.Run("interval", func(t *testing.T) {
		result := d.SubstituteMacros("SELECT $__interval AS i", from, to)
		assert.NotContains(t, result, "$__interval")
		assert.NotContains(t, result, "s")
	})
}

func TestPostgresSubstituteMacros(t *testing.T) {
	d := &postgresDialect{}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)

	t.Run("timeFilter", func(t *testing.T) {
		result := d.SubstituteMacros("SELECT * FROM t WHERE $__timeFilter(ts)", from, to)
		assert.Contains(t, result, "ts BETWEEN '2026-01-01 00:00:00' AND '2026-01-01 01:00:00'")
		assert.NotContains(t, result, "$__timeFilter")
	})

	t.Run("timeFrom uses timestamp cast", func(t *testing.T) {
		result := d.SubstituteMacros("SELECT * FROM t WHERE ts > $__timeFrom()", from, to)
		assert.Contains(t, result, "'2026-01-01 00:00:00'::timestamp")
	})

	t.Run("interval is bare integer", func(t *testing.T) {
		result := d.SubstituteMacros("SELECT $__interval AS i", from, to)
		assert.NotContains(t, result, "$__interval")
	})
}

func TestMSSQLSubstituteMacros(t *testing.T) {
	d := &mssqlDialect{}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)

	t.Run("timeFilter", func(t *testing.T) {
		result := d.SubstituteMacros("SELECT * FROM t WHERE $__timeFilter(ts)", from, to)
		assert.Contains(t, result, "ts BETWEEN '2026-01-01 00:00:00' AND '2026-01-01 01:00:00'")
		assert.NotContains(t, result, "$__timeFilter")
	})

	t.Run("timeFrom and timeTo", func(t *testing.T) {
		result := d.SubstituteMacros("SELECT * FROM t WHERE ts > $__timeFrom() AND ts < $__timeTo()", from, to)
		assert.Contains(t, result, "'2026-01-01 00:00:00'")
		assert.Contains(t, result, "'2026-01-01 01:00:00'")
	})

	t.Run("from and to as epoch ms", func(t *testing.T) {
		result := d.SubstituteMacros("SELECT $__from, $__to", from, to)
		assert.NotContains(t, result, "$__from")
		assert.NotContains(t, result, "$__to")
	})
}

func TestSQLQueryResultStructure(t *testing.T) {
	result := SQLQueryResult{
		Columns: []string{"id", "name"},
		Rows: []map[string]interface{}{
			{"id": 1, "name": "test"},
		},
		RowCount:       1,
		ProcessedQuery: "SELECT id, name FROM t LIMIT 100",
	}
	assert.Len(t, result.Columns, 2)
	assert.Equal(t, 1, result.RowCount)
}
