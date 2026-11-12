//go:build unit

package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClickHouseLimitBeforeSettings(t *testing.T) {
	d := &clickHouseDialect{}
	tests := []struct {
		name  string
		query string
		limit int
		want  string
	}{
		{"default", "SELECT 1 SETTINGS max_execution_time=1", 0, "SELECT 1 LIMIT 100 SETTINGS max_execution_time=1"},
		{"requested", "SELECT 1 SETTINGS max_block_size=100", 50, "SELECT 1 LIMIT 50 SETTINGS max_block_size=100"},
		{"maximum", "SELECT 1 SETTINGS max_block_size=100", 5000, "SELECT 1 LIMIT 1000 SETTINGS max_block_size=100"},
		{"multiple", "SELECT 1 SETTINGS max_block_size=100, max_threads=1", 1, "SELECT 1 LIMIT 1 SETTINGS max_block_size=100, max_threads=1"},
		{"semicolon", " SELECT 1 SETTINGS max_threads=1; \n", 1, "SELECT 1 LIMIT 1 SETTINGS max_threads=1"},
		{"existing", "SELECT 1 LIMIT 5 SETTINGS max_threads=1", 1, "SELECT 1 LIMIT 5 SETTINGS max_threads=1"},
		{"existing maximum", "SELECT 1 LIMIT 5000 SETTINGS max_threads=1", 1, "SELECT 1 LIMIT 1000 SETTINGS max_threads=1"},
		{"case and newline", "SELECT 1\nsettings max_threads = 1", 1, "SELECT 1 LIMIT 1\nsettings max_threads = 1"},
		{"alias", "SELECT 1 AS settings SETTINGS max_threads=1", 1, "SELECT 1 AS settings LIMIT 1 SETTINGS max_threads=1"},
		{"alias only", "SELECT 1 AS settings", 1, "SELECT 1 AS settings LIMIT 1"},
		{"string", "SELECT 'SETTINGS max_threads=2' SETTINGS max_threads=1", 1, "SELECT 'SETTINGS max_threads=2' LIMIT 1 SETTINGS max_threads=1"},
		{"string only", "SELECT 'SETTINGS max_threads=2'", 1, "SELECT 'SETTINGS max_threads=2' LIMIT 1"},
		{"doubled quote", "SELECT 'it''s SETTINGS max_threads=2' SETTINGS max_threads=1", 1, "SELECT 'it''s SETTINGS max_threads=2' LIMIT 1 SETTINGS max_threads=1"},
		{"escaped quote", `SELECT 'it\'s SETTINGS max_threads=2' SETTINGS max_threads=1`, 1, `SELECT 'it\'s SETTINGS max_threads=2' LIMIT 1 SETTINGS max_threads=1`},
		{"quoted identifier", "SELECT 1 AS `SETTINGS max_threads=2` SETTINGS max_threads=1", 1, "SELECT 1 AS `SETTINGS max_threads=2` LIMIT 1 SETTINGS max_threads=1"},
		{"double quoted identifier", `SELECT 1 AS "SETTINGS max_threads=2" SETTINGS max_threads=1`, 1, `SELECT 1 AS "SETTINGS max_threads=2" LIMIT 1 SETTINGS max_threads=1`},
		{"block comment", "SELECT 1 /* SETTINGS max_threads=2 */ SETTINGS max_threads=1", 1, "SELECT 1 LIMIT 1 /* SETTINGS max_threads=2 */ SETTINGS max_threads=1"},
		{"line comment", "SELECT 1 -- SETTINGS max_threads=2\nSETTINGS max_threads=1", 1, "SELECT 1 LIMIT 1 -- SETTINGS max_threads=2\nSETTINGS max_threads=1"},
		{"slash comment", "SELECT 1 // comment\nSETTINGS max_threads=1", 1, "SELECT 1 LIMIT 1 // comment\nSETTINGS max_threads=1"},
		{"slash comment with settings", "SELECT 1 // SETTINGS max_threads=2\nSETTINGS max_threads=1", 1, "SELECT 1 LIMIT 1 // SETTINGS max_threads=2\nSETTINGS max_threads=1"},
		{"slash comment before from", "SELECT * // SETTINGS max_threads=2\nFROM numbers(10)", 1, "SELECT * // SETTINGS max_threads=2\nFROM numbers(10) LIMIT 1"},
		{"slash clause comment", "SELECT 1 SETTINGS // spacing\nmax_threads=1", 1, "SELECT 1 LIMIT 1 SETTINGS // spacing\nmax_threads=1"},
		{"insert settings", "INSERT INTO t SETTINGS max_threads=1 SELECT 1", 1, "INSERT INTO t SETTINGS max_threads=1 SELECT 1 LIMIT 1"},
		{"create settings", "CREATE TABLE t ENGINE=MergeTree ORDER BY tuple() SETTINGS index_granularity=8192 AS SELECT 1 AS x", 1, "CREATE TABLE t ENGINE=MergeTree ORDER BY tuple() SETTINGS index_granularity=8192 AS SELECT 1 AS x LIMIT 1"},
		{"insert and select settings", "INSERT INTO t SETTINGS max_threads=2 SELECT 1 SETTINGS max_threads=1", 1, "INSERT INTO t SETTINGS max_threads=2 SELECT 1 LIMIT 1 SETTINGS max_threads=1"},
		{"create and select settings", "CREATE TABLE t ENGINE=MergeTree ORDER BY tuple() SETTINGS index_granularity=8192 AS SELECT 1 AS x SETTINGS max_threads=1", 1, "CREATE TABLE t ENGINE=MergeTree ORDER BY tuple() SETTINGS index_granularity=8192 AS SELECT 1 AS x LIMIT 1 SETTINGS max_threads=1"},
		{"cte settings", "WITH t AS (SELECT 1 SETTINGS max_threads=2) SELECT * FROM t SETTINGS max_threads=1", 1, "WITH t AS (SELECT 1 SETTINGS max_threads=2) SELECT * FROM t LIMIT 1 SETTINGS max_threads=1"},
		{"nested settings", "SELECT * FROM (SELECT 1 SETTINGS max_threads=2) SETTINGS max_threads=1", 1, "SELECT * FROM (SELECT 1 SETTINGS max_threads=2) LIMIT 1 SETTINGS max_threads=1"},
		{"nested only", "SELECT * FROM (SELECT 1 SETTINGS max_threads=2)", 1, "SELECT * FROM (SELECT 1 SETTINGS max_threads=2) LIMIT 1"},
		{"heredoc", "SELECT $body$ SETTINGS max_threads=2 $body$ SETTINGS max_threads=1", 1, "SELECT $body$ SETTINGS max_threads=2 $body$ LIMIT 1 SETTINGS max_threads=1"},
		{"heredoc only", "SELECT $$ SETTINGS max_threads=2 $$", 1, "SELECT $$ SETTINGS max_threads=2 $$ LIMIT 1"},
		{"clause comments", "SELECT 1 SETTINGS /* spacing */ max_threads /* name */ = 1", 1, "SELECT 1 LIMIT 1 SETTINGS /* spacing */ max_threads /* name */ = 1"},
		{"quoted setting", "SELECT 1 SETTINGS `max_threads` = 1", 1, "SELECT 1 LIMIT 1 SETTINGS `max_threads` = 1"},
		{"plain", "SELECT 1", 1, "SELECT 1 LIMIT 1"},
		{"from first", "FROM numbers(10) SETTINGS max_threads=1", 1, "FROM numbers(10) LIMIT 1 SETTINGS max_threads=1"},
		{"from first with comment", "/* SELECT */ from numbers(10) // comment\nsettings max_threads=1", 1, "/* SELECT */ from numbers(10) LIMIT 1 // comment\nsettings max_threads=1"},
		{"from first existing maximum", "FROM numbers(10) LIMIT 5000 SETTINGS max_threads=1", 1, "FROM numbers(10) LIMIT 1000 SETTINGS max_threads=1"},
		{"from first explicit select", "FROM numbers(10) SELECT number SETTINGS max_threads=1", 1, "FROM numbers(10) SELECT number LIMIT 1 SETTINGS max_threads=1"},
		{"from first cte", "WITH t AS (SELECT 1) FROM t SETTINGS max_threads=1", 1, "WITH t AS (SELECT 1) FROM t LIMIT 1 SETTINGS max_threads=1"},
		{"from table named infile", "FROM infile SETTINGS max_threads=1", 1, "FROM infile LIMIT 1 SETTINGS max_threads=1"},
		{"explain from first", "EXPLAIN SYNTAX FROM numbers(10) SETTINGS max_threads=1", 1, "EXPLAIN SYNTAX FROM numbers(10) LIMIT 1 SETTINGS max_threads=1"},
		{"insert from first", "INSERT INTO t SETTINGS max_threads=2 FROM numbers(10) SETTINGS max_threads=1", 1, "INSERT INTO t SETTINGS max_threads=2 FROM numbers(10) LIMIT 1 SETTINGS max_threads=1"},
		{"create from first", "CREATE TABLE t ENGINE=MergeTree ORDER BY tuple() SETTINGS index_granularity=8192 AS FROM numbers(10) SETTINGS max_threads=1", 1, "CREATE TABLE t ENGINE=MergeTree ORDER BY tuple() SETTINGS index_granularity=8192 AS FROM numbers(10) LIMIT 1 SETTINGS max_threads=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.EnforceLimit(tt.query, tt.limit)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, got, d.EnforceLimit(got, tt.limit), "projection must be idempotent")
		})
	}
}

func TestClickHouseSettingsStartNonQueryFrom(t *testing.T) {
	for _, query := range []string{
		"INSERT INTO t FROM INFILE 'data.csv' SETTINGS max_threads=1 FORMAT CSV",
		"INSERT INTO t FROM /* comment */ INFILE 'data.csv' SETTINGS max_threads=1 FORMAT CSV",
		"DELETE FROM t WHERE x=1 SETTINGS max_threads=1",
	} {
		t.Run(query, func(t *testing.T) {
			_, found := clickHouseSettingsStart(query)
			assert.False(t, found, "non-query SETTINGS must not be relocated")
		})
	}
}
