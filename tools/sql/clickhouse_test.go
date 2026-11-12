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
			"plugin macros $__fromTime and $__toTime are left for the ClickHouse plugin",
			"SELECT * FROM logs WHERE ts >= $__fromTime - INTERVAL 1 DAY AND ts <= $__toTime",
			"SELECT * FROM logs WHERE ts >= $__fromTime - INTERVAL 1 DAY AND ts <= $__toTime",
		},
		{
			"plugin macros $__fromTime_ms and $__toTime_ms are left for the ClickHouse plugin",
			"SELECT * FROM logs WHERE ts BETWEEN $__fromTime_ms AND $__toTime_ms",
			"SELECT * FROM logs WHERE ts BETWEEN $__fromTime_ms AND $__toTime_ms",
		},
		{
			"plugin macro $__interval_s is left for the ClickHouse plugin",
			"SELECT toStartOfInterval(ts, INTERVAL greatest($__interval_s, 60) SECOND) FROM logs",
			"SELECT toStartOfInterval(ts, INTERVAL greatest($__interval_s, 60) SECOND) FROM logs",
		},
		{
			"plugin macros $__timeInterval(ts) and $__timeInterval_ms(ts) are left for the ClickHouse plugin",
			"SELECT $__timeInterval(ts) AS t, $__timeInterval_ms(ts) AS t_ms, count() FROM logs GROUP BY t, t_ms",
			"SELECT $__timeInterval(ts) AS t, $__timeInterval_ms(ts) AS t_ms, count() FROM logs GROUP BY t, t_ms",
		},
		{
			"braced ${__from} and ${__to} are template syntax, not substituted here",
			"SELECT * FROM logs WHERE t BETWEEN ${__from} AND ${__to}",
			"SELECT * FROM logs WHERE t BETWEEN ${__from} AND ${__to}",
		},
		{
			"$__from and $__to next to punctuation",
			"SELECT * FROM logs WHERE t > $__from/1000 AND t < ($__to)",
			"SELECT * FROM logs WHERE t > 1705312800000/1000 AND t < (1705316400000)",
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
