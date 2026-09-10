//go:build unit

package sql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClickHouseDialect_SubstituteMacros(t *testing.T) {
	d := &clickHouseDialect{}
	from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			"timeFilter",
			"SELECT * FROM logs WHERE $__timeFilter(ts)",
			"SELECT * FROM logs WHERE ts >= toDateTime(1705312800) AND ts <= toDateTime(1705316400)",
		},
		{
			"from and to",
			"SELECT * FROM logs WHERE t BETWEEN $__from AND $__to",
			"SELECT * FROM logs WHERE t BETWEEN 1705312800000 AND 1705316400000",
		},
		{
			"interval",
			"SELECT toStartOfInterval(ts, INTERVAL $__interval) AS time",
			"SELECT toStartOfInterval(ts, INTERVAL 3s) AS time",
		},
		{
			"interval_ms",
			"SELECT * FROM logs WHERE interval_ms = $__interval_ms",
			"SELECT * FROM logs WHERE interval_ms = 3000",
		},
		{
			"dotted column",
			"SELECT * FROM logs WHERE $__timeFilter(table.ts)",
			"SELECT * FROM logs WHERE table.ts >= toDateTime(1705312800) AND table.ts <= toDateTime(1705316400)",
		},
		{
			"no macros",
			"SELECT * FROM logs WHERE ts > '2024-01-01'",
			"SELECT * FROM logs WHERE ts > '2024-01-01'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.SubstituteMacros(tt.query, from, to)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClickHouseDialect_EnforceLimit(t *testing.T) {
	d := &clickHouseDialect{}

	assert.Equal(t, "SELECT 1 LIMIT 100", d.EnforceLimit("SELECT 1", 0))
	assert.Equal(t, "SELECT 1 LIMIT 50", d.EnforceLimit("SELECT 1", 50))
	assert.Equal(t, "SELECT 1 LIMIT 1000", d.EnforceLimit("SELECT 1", 5000))
	assert.Equal(t, "SELECT 1 LIMIT 50", d.EnforceLimit("SELECT 1 LIMIT 50", 100))
}
