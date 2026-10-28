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
		{"nested settings", "SELECT * FROM (SELECT 1 SETTINGS max_threads=2) SETTINGS max_threads=1", 1, "SELECT * FROM (SELECT 1 SETTINGS max_threads=2) LIMIT 1 SETTINGS max_threads=1"},
		{"nested only", "SELECT * FROM (SELECT 1 SETTINGS max_threads=2)", 1, "SELECT * FROM (SELECT 1 SETTINGS max_threads=2) LIMIT 1"},
		{"heredoc", "SELECT $body$ SETTINGS max_threads=2 $body$ SETTINGS max_threads=1", 1, "SELECT $body$ SETTINGS max_threads=2 $body$ LIMIT 1 SETTINGS max_threads=1"},
		{"heredoc only", "SELECT $$ SETTINGS max_threads=2 $$", 1, "SELECT $$ SETTINGS max_threads=2 $$ LIMIT 1"},
		{"clause comments", "SELECT 1 SETTINGS /* spacing */ max_threads /* name */ = 1", 1, "SELECT 1 LIMIT 1 SETTINGS /* spacing */ max_threads /* name */ = 1"},
		{"quoted setting", "SELECT 1 SETTINGS `max_threads` = 1", 1, "SELECT 1 LIMIT 1 SETTINGS `max_threads` = 1"},
		{"plain", "SELECT 1", 1, "SELECT 1 LIMIT 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.EnforceLimit(tt.query, tt.limit)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, got, d.EnforceLimit(got, tt.limit), "projection must be idempotent")
		})
	}
}
