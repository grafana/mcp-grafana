//go:build unit

package sql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSnowflakeDialect_SubstituteMacros(t *testing.T) {
	d := &snowflakeDialect{}
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
			"SELECT * FROM logs WHERE ts >= TO_TIMESTAMP_NTZ('2024-01-15 10:00:00') AND ts <= TO_TIMESTAMP_NTZ('2024-01-15 11:00:00')",
		},
		{
			"timeFrom and timeTo",
			"SELECT * FROM logs WHERE ts BETWEEN $__timeFrom AND $__timeTo",
			"SELECT * FROM logs WHERE ts BETWEEN TO_TIMESTAMP_NTZ('2024-01-15 10:00:00') AND TO_TIMESTAMP_NTZ('2024-01-15 11:00:00')",
		},
		{
			"from and to as millis",
			"SELECT * FROM logs WHERE t BETWEEN $__from AND $__to",
			"SELECT * FROM logs WHERE t BETWEEN 1705312800000 AND 1705316400000",
		},
		{
			"interval",
			"SELECT TIME_SLICE(ts, $__interval, 'SECOND')",
			"SELECT TIME_SLICE(ts, 3, 'SECOND')",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.SubstituteMacros(tt.query, from, to)
			assert.Equal(t, tt.expected, result)
		})
	}
}
