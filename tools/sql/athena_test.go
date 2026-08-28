//go:build unit

package sql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAthenaDialect_SubstituteMacros(t *testing.T) {
	d := &athenaDialect{}
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
			"SELECT * FROM logs WHERE ts BETWEEN TIMESTAMP '2024-01-15 10:00:00' AND TIMESTAMP '2024-01-15 11:00:00'",
		},
		{
			"dateFilter",
			"SELECT * FROM logs WHERE $__dateFilter(dt)",
			"SELECT * FROM logs WHERE dt BETWEEN date '2024-01-15' AND date '2024-01-15'",
		},
		{
			"unixEpochFilter",
			"SELECT * FROM logs WHERE $__unixEpochFilter(epoch)",
			"SELECT * FROM logs WHERE epoch BETWEEN 1705312800 AND 1705316400",
		},
		{
			"timeFrom and timeTo",
			"SELECT * WHERE ts > $__timeFrom() AND ts < $__timeTo()",
			"SELECT * WHERE ts > TIMESTAMP '2024-01-15 10:00:00' AND ts < TIMESTAMP '2024-01-15 11:00:00'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.SubstituteMacros(tt.query, from, to)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAthenaDialect_EnforceLimit(t *testing.T) {
	d := &athenaDialect{}

	assert.Equal(t, "SELECT 1 LIMIT 100", d.EnforceLimit("SELECT 1", 0))
	assert.Equal(t, "SHOW TABLES", d.EnforceLimit("SHOW TABLES", 0))
	assert.Equal(t, "DESCRIBE my_table", d.EnforceLimit("DESCRIBE my_table", 0))
}

func TestAthenaDialect_ExtraQueryPayloadFields(t *testing.T) {
	d := &athenaDialect{}

	// No extras when no context params set
	assert.Nil(t, d.ExtraQueryPayloadFields(QuerySQLParams{}))

	// Athena-specific fields
	extra := d.ExtraQueryPayloadFields(QuerySQLParams{
		Region:   "us-east-1",
		Catalog:  "AwsDataCatalog",
		Database: "mydb",
	})
	assert.NotNil(t, extra)
	connArgs := extra["connectionArgs"].(map[string]interface{})
	assert.Equal(t, "us-east-1", connArgs["region"])
	assert.Equal(t, "AwsDataCatalog", connArgs["catalog"])
	assert.Equal(t, "mydb", connArgs["database"])
}
