//go:build unit

package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSubstituteSnowflakeMacros(t *testing.T) {
	from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:  "timeFilter macro with column name",
			query: "SELECT * FROM events WHERE $__timeFilter(TIMESTAMP)",
			expected: "SELECT * FROM events WHERE TIMESTAMP >= TO_TIMESTAMP_NTZ('2024-01-15 10:00:00') " +
				"AND TIMESTAMP <= TO_TIMESTAMP_NTZ('2024-01-15 11:00:00')",
		},
		{
			name:  "timeFilter with three-part identifier",
			query: "SELECT * FROM SNOWFLAKE.TELEMETRY.EVENTS WHERE $__timeFilter(TIMESTAMP)",
			expected: "SELECT * FROM SNOWFLAKE.TELEMETRY.EVENTS WHERE TIMESTAMP >= TO_TIMESTAMP_NTZ('2024-01-15 10:00:00') " +
				"AND TIMESTAMP <= TO_TIMESTAMP_NTZ('2024-01-15 11:00:00')",
		},
		{
			name:  "timeFilter with double-quoted column",
			query: `SELECT * FROM events WHERE $__timeFilter("Timestamp")`,
			expected: `SELECT * FROM events WHERE "Timestamp" >= TO_TIMESTAMP_NTZ('2024-01-15 10:00:00') ` +
				`AND "Timestamp" <= TO_TIMESTAMP_NTZ('2024-01-15 11:00:00')`,
		},
		{
			name:  "timeFilter with spaces around column",
			query: "SELECT * FROM events WHERE $__timeFilter( TIMESTAMP )",
			expected: "SELECT * FROM events WHERE TIMESTAMP >= TO_TIMESTAMP_NTZ('2024-01-15 10:00:00') " +
				"AND TIMESTAMP <= TO_TIMESTAMP_NTZ('2024-01-15 11:00:00')",
		},
		{
			name:     "$__timeFrom and $__timeTo",
			query:    "SELECT * FROM events WHERE TIMESTAMP BETWEEN $__timeFrom AND $__timeTo",
			expected: "SELECT * FROM events WHERE TIMESTAMP BETWEEN TO_TIMESTAMP_NTZ('2024-01-15 10:00:00') AND TO_TIMESTAMP_NTZ('2024-01-15 11:00:00')",
		},
		{
			name:     "$__from and $__to are Unix milliseconds",
			query:    "SELECT $__from, $__to",
			expected: "SELECT 1705312800000, 1705316400000",
		},
		{
			name:     "$__interval emits seconds as integer",
			query:    "SELECT TIME_SLICE(TIMESTAMP, $__interval, 'SECOND') FROM events",
			expected: "SELECT TIME_SLICE(TIMESTAMP, 3, 'SECOND') FROM events",
		},
		{
			name:     "$__interval_ms emits milliseconds",
			query:    "SELECT $__interval_ms",
			expected: "SELECT 3000",
		},
		{
			name:     "interval_ms is replaced before interval (no partial match)",
			query:    "SELECT $__interval_ms, $__interval",
			expected: "SELECT 3000, 3",
		},
		{
			name:     "no macros - query unchanged",
			query:    "SELECT * FROM events WHERE timestamp > '2024-01-01'",
			expected: "SELECT * FROM events WHERE timestamp > '2024-01-01'",
		},
		{
			name:  "multiple macros mixed",
			query: "SELECT * FROM events WHERE $__timeFilter(TIMESTAMP) AND BUCKET = $__interval_ms",
			expected: "SELECT * FROM events WHERE TIMESTAMP >= TO_TIMESTAMP_NTZ('2024-01-15 10:00:00') " +
				"AND TIMESTAMP <= TO_TIMESTAMP_NTZ('2024-01-15 11:00:00') AND BUCKET = 3000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := substituteSnowflakeMacros(tt.query, from, to)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSubstituteSnowflakeMacros_IntervalCalculation(t *testing.T) {
	tests := []struct {
		name             string
		rangeHours       int
		expectedInterval string
	}{
		{name: "1 hour range", rangeHours: 1, expectedInterval: "3"},
		{name: "6 hour range", rangeHours: 6, expectedInterval: "21"},
		{name: "24 hour range", rangeHours: 24, expectedInterval: "86"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
			to := from.Add(time.Duration(tt.rangeHours) * time.Hour)

			result := substituteSnowflakeMacros("$__interval", from, to)
			assert.Equal(t, tt.expectedInterval, result)
		})
	}
}

func TestSubstituteSnowflakeMacros_NonUTCInputNormalized(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("timezone data not available")
	}
	from := time.Date(2024, 1, 15, 5, 0, 0, 0, loc) // 10:00 UTC
	to := time.Date(2024, 1, 15, 6, 0, 0, 0, loc)   // 11:00 UTC

	result := substituteSnowflakeMacros("$__timeFrom", from, to)
	assert.Equal(t, "TO_TIMESTAMP_NTZ('2024-01-15 10:00:00')", result,
		"timestamps must be emitted as UTC regardless of input timezone")
}

func TestEnforceSnowflakeLimit(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		limit    int
		expected string
	}{
		{name: "no limit clause - append default", query: "SELECT * FROM events", limit: 0,
			expected: "SELECT * FROM events LIMIT 100"},
		{name: "no limit clause - append custom", query: "SELECT * FROM events", limit: 50,
			expected: "SELECT * FROM events LIMIT 50"},
		{name: "limit exceeds max - cap at max", query: "SELECT * FROM events", limit: 5000,
			expected: "SELECT * FROM events LIMIT 1000"},
		{name: "existing limit below max - unchanged", query: "SELECT * FROM events LIMIT 50", limit: 100,
			expected: "SELECT * FROM events LIMIT 50"},
		{name: "existing limit exceeds max - capped", query: "SELECT * FROM events LIMIT 5000", limit: 100,
			expected: "SELECT * FROM events LIMIT 1000"},
		{name: "trailing semicolon stripped", query: "SELECT * FROM events;", limit: 100,
			expected: "SELECT * FROM events LIMIT 100"},
		{name: "surrounding whitespace trimmed", query: "  SELECT * FROM events  ", limit: 100,
			expected: "SELECT * FROM events LIMIT 100"},
		{name: "case insensitive LIMIT detection", query: "SELECT * FROM events limit 50", limit: 100,
			expected: "SELECT * FROM events limit 50"},
		{name: "negative limit uses default", query: "SELECT * FROM events", limit: -1,
			expected: "SELECT * FROM events LIMIT 100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enforceSnowflakeLimit(tt.query, tt.limit)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateSnowflakeIdentifier(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		wantErr bool
	}{
		{name: "", field: "database", wantErr: false}, // empty is allowed (means "no filter")
		{name: "PUBLIC", field: "schema", wantErr: false},
		{name: "MY_TABLE", field: "table", wantErr: false},
		{name: "events_2024", field: "table", wantErr: false},

		// injection attempts must fail
		{name: "events; DROP TABLE bar", field: "table", wantErr: true},
		{name: "events' OR 1=1 --", field: "table", wantErr: true},
		{name: "events' UNION SELECT name FROM INFORMATION_SCHEMA.TABLES --", field: "table", wantErr: true},
		{name: "events name", field: "table", wantErr: true},
		{name: "events-name", field: "table", wantErr: true},
		{name: "schema.table", field: "table", wantErr: true},
	}

	for _, tt := range tests {
		err := validateSnowflakeIdentifier(tt.name, tt.field)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateSnowflakeIdentifier(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestSnowflakeQueryParams_VariableSubstitution(t *testing.T) {
	// Sanity check that we're using the shared substituteVariables helper correctly.
	query := "SELECT * FROM ${database}.PUBLIC.events WHERE service = '${service}'"
	vars := map[string]string{
		"database": "ANALYTICS",
		"service":  "checkout",
	}
	got := substituteVariables(query, vars)
	assert.Equal(t, "SELECT * FROM ANALYTICS.PUBLIC.events WHERE service = 'checkout'", got)
}

func TestSnowflakeQueryParams_Structure(t *testing.T) {
	params := SnowflakeQueryParams{
		DatasourceUID: "test-uid",
		Query:         "SELECT * FROM events",
		Start:         "now-1h",
		End:           "now",
		Variables:     map[string]string{"service": "my-app"},
		Limit:         100,
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "SELECT * FROM events", params.Query)
	assert.Equal(t, "now-1h", params.Start)
	assert.Equal(t, "now", params.End)
	assert.Equal(t, "my-app", params.Variables["service"])
	assert.Equal(t, 100, params.Limit)
}

func TestSnowflakeTableInfo_Structure(t *testing.T) {
	info := SnowflakeTableInfo{
		Database: "ANALYTICS",
		Schema:   "PUBLIC",
		Name:     "EVENTS",
		Kind:     "BASE TABLE",
		RowCount: 1000000,
		Bytes:    52428800,
	}
	assert.Equal(t, "ANALYTICS", info.Database)
	assert.Equal(t, "PUBLIC", info.Schema)
	assert.Equal(t, "EVENTS", info.Name)
	assert.Equal(t, "BASE TABLE", info.Kind)
	assert.Equal(t, int64(1000000), info.RowCount)
	assert.Equal(t, int64(52428800), info.Bytes)
}

func TestSnowflakeColumnInfo_Structure(t *testing.T) {
	col := SnowflakeColumnInfo{
		Name:     "TIMESTAMP",
		Type:     "TIMESTAMP_NTZ",
		Nullable: "YES",
		Default:  "",
		Comment:  "Event timestamp",
	}
	assert.Equal(t, "TIMESTAMP", col.Name)
	assert.Equal(t, "TIMESTAMP_NTZ", col.Type)
	assert.Equal(t, "YES", col.Nullable)
	assert.Equal(t, "Event timestamp", col.Comment)
}

func TestGenerateSnowflakeEmptyResultHints(t *testing.T) {
	hints := GenerateEmptyResultHints(HintContext{
		DatasourceType: "snowflake",
		Query:          "SELECT * FROM events",
	})

	assert.NotNil(t, hints)
	assert.Contains(t, hints.Summary, "Snowflake")
	found := false
	for _, action := range hints.SuggestedActions {
		if strings.Contains(action, "list_snowflake_tables") {
			found = true
			break
		}
	}
	assert.True(t, found, "Hints should suggest using list_snowflake_tables")
}
