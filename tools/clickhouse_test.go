//go:build unit

package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSubstituteClickHouseMacros(t *testing.T) {
	// Fixed times for deterministic testing
	from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	fromSeconds := from.Unix()
	toSeconds := to.Unix()
	fromMillis := from.UnixMilli()
	toMillis := to.UnixMilli()

	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "timeFilter macro with column name",
			query:    "SELECT * FROM logs WHERE $__timeFilter(TimestampTime)",
			expected: "SELECT * FROM logs WHERE TimestampTime >= toDateTime(1705312800) AND TimestampTime <= toDateTime(1705316400)",
		},
		{
			name:     "timeFilter macro with different column",
			query:    "SELECT * FROM events WHERE $__timeFilter(created_at) ORDER BY created_at",
			expected: "SELECT * FROM events WHERE created_at >= toDateTime(1705312800) AND created_at <= toDateTime(1705316400) ORDER BY created_at",
		},
		{
			name:  "$__from and $__to macros",
			query: "SELECT * FROM logs WHERE timestamp BETWEEN $__from AND $__to",
			expected: "SELECT * FROM logs WHERE timestamp BETWEEN " +
				"1705312800000 AND 1705316400000",
		},
		{
			name:     "$__interval macro",
			query:    "SELECT toStartOfInterval(timestamp, INTERVAL $__interval) AS time",
			expected: "SELECT toStartOfInterval(timestamp, INTERVAL 3s) AS time",
		},
		{
			name:     "$__interval_ms macro",
			query:    "SELECT * FROM logs WHERE interval_ms = $__interval_ms",
			expected: "SELECT * FROM logs WHERE interval_ms = 3000",
		},
		{
			name:     "multiple macros in same query",
			query:    "SELECT * FROM logs WHERE $__timeFilter(ts) AND bucket = $__interval_ms",
			expected: "SELECT * FROM logs WHERE ts >= toDateTime(1705312800) AND ts <= toDateTime(1705316400) AND bucket = 3000",
		},
		{
			name:     "no macros - query unchanged",
			query:    "SELECT * FROM logs WHERE timestamp > '2024-01-01'",
			expected: "SELECT * FROM logs WHERE timestamp > '2024-01-01'",
		},
		// Tests for improved regex matching dotted and quoted column names
		{
			name:     "timeFilter macro with dotted column name",
			query:    "SELECT * FROM logs WHERE $__timeFilter(table.ts)",
			expected: "SELECT * FROM logs WHERE table.ts >= toDateTime(1705312800) AND table.ts <= toDateTime(1705316400)",
		},
		{
			name:     "timeFilter macro with double-quoted column",
			query:    `SELECT * FROM logs WHERE $__timeFilter("timestamp")`,
			expected: `SELECT * FROM logs WHERE "timestamp" >= toDateTime(1705312800) AND "timestamp" <= toDateTime(1705316400)`,
		},
		{
			name:     "timeFilter macro with backtick-quoted column",
			query:    "SELECT * FROM logs WHERE $__timeFilter(`Timestamp`)",
			expected: "SELECT * FROM logs WHERE `Timestamp` >= toDateTime(1705312800) AND `Timestamp` <= toDateTime(1705316400)",
		},
		{
			name:     "timeFilter macro with spaces around column",
			query:    "SELECT * FROM logs WHERE $__timeFilter( ts )",
			expected: "SELECT * FROM logs WHERE ts >= toDateTime(1705312800) AND ts <= toDateTime(1705316400)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := substituteClickHouseMacros(tt.query, from, to)
			assert.Equal(t, tt.expected, result)
		})
	}

	// Verify the actual Unix timestamps used
	t.Run("verify timestamps", func(t *testing.T) {
		assert.Equal(t, int64(1705312800), fromSeconds)
		assert.Equal(t, int64(1705316400), toSeconds)
		assert.Equal(t, int64(1705312800000), fromMillis)
		assert.Equal(t, int64(1705316400000), toMillis)
	})
}

func TestSubstituteVariables(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		variables map[string]string
		expected  string
	}{
		{
			name:  "single ${var} substitution",
			query: "SELECT * FROM logs WHERE service = '${service}'",
			variables: map[string]string{
				"service": "my-app",
			},
			expected: "SELECT * FROM logs WHERE service = 'my-app'",
		},
		{
			name:  "single $var substitution",
			query: "SELECT * FROM logs WHERE service = '$service'",
			variables: map[string]string{
				"service": "my-app",
			},
			expected: "SELECT * FROM logs WHERE service = 'my-app'",
		},
		{
			name:  "multiple variables",
			query: "SELECT * FROM logs WHERE service = '${service}' AND env = '${env}'",
			variables: map[string]string{
				"service": "my-app",
				"env":     "prod",
			},
			expected: "SELECT * FROM logs WHERE service = 'my-app' AND env = 'prod'",
		},
		{
			name:  "mixed formats",
			query: "SELECT * FROM logs WHERE service = '${service}' AND account = '$account'",
			variables: map[string]string{
				"service": "my-app",
				"account": "AC123",
			},
			expected: "SELECT * FROM logs WHERE service = 'my-app' AND account = 'AC123'",
		},
		{
			name:  "variable used multiple times",
			query: "SELECT * FROM logs WHERE service = '${service}' OR source = '${service}'",
			variables: map[string]string{
				"service": "my-app",
			},
			expected: "SELECT * FROM logs WHERE service = 'my-app' OR source = 'my-app'",
		},
		{
			name:      "no variables - query unchanged",
			query:     "SELECT * FROM logs WHERE 1=1",
			variables: nil,
			expected:  "SELECT * FROM logs WHERE 1=1",
		},
		{
			name:      "empty variables map",
			query:     "SELECT * FROM logs WHERE service = '${service}'",
			variables: map[string]string{},
			expected:  "SELECT * FROM logs WHERE service = '${service}'",
		},
		{
			name:  "variable not in query - no change",
			query: "SELECT * FROM logs WHERE service = 'fixed'",
			variables: map[string]string{
				"service": "my-app",
			},
			expected: "SELECT * FROM logs WHERE service = 'fixed'",
		},
		{
			name:  "empty value substitution",
			query: "SELECT * FROM logs WHERE request_id = '${request_id}'",
			variables: map[string]string{
				"request_id": "",
			},
			expected: "SELECT * FROM logs WHERE request_id = ''",
		},
		{
			name:  "$var should not match partial names",
			query: "SELECT * FROM logs WHERE servicename = 'test'",
			variables: map[string]string{
				"service": "my-app",
			},
			expected: "SELECT * FROM logs WHERE servicename = 'test'",
		},
		{
			name:  "value containing dollar sign treated literally",
			query: "SELECT * FROM logs WHERE tag = '$tag'",
			variables: map[string]string{
				"tag": "price$1",
			},
			expected: "SELECT * FROM logs WHERE tag = 'price$1'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := substituteVariables(tt.query, tt.variables)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEnforceClickHouseLimit(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		limit    int
		expected string
	}{
		{
			name:     "no limit clause - append default",
			query:    "SELECT * FROM logs",
			limit:    0,
			expected: "SELECT * FROM logs LIMIT 100",
		},
		{
			name:     "no limit clause - append custom",
			query:    "SELECT * FROM logs",
			limit:    50,
			expected: "SELECT * FROM logs LIMIT 50",
		},
		{
			name:     "limit exceeds max - cap at max",
			query:    "SELECT * FROM logs",
			limit:    5000,
			expected: "SELECT * FROM logs LIMIT 1000",
		},
		{
			name:     "existing limit below max - unchanged",
			query:    "SELECT * FROM logs LIMIT 50",
			limit:    100,
			expected: "SELECT * FROM logs LIMIT 50",
		},
		{
			name:     "existing limit exceeds max - capped",
			query:    "SELECT * FROM logs LIMIT 5000",
			limit:    100,
			expected: "SELECT * FROM logs LIMIT 1000",
		},
		{
			name:     "query with trailing semicolon",
			query:    "SELECT * FROM logs;",
			limit:    100,
			expected: "SELECT * FROM logs LIMIT 100",
		},
		{
			name:     "query with whitespace",
			query:    "  SELECT * FROM logs  ",
			limit:    100,
			expected: "SELECT * FROM logs LIMIT 100",
		},
		{
			name:     "case insensitive LIMIT detection",
			query:    "SELECT * FROM logs limit 50",
			limit:    100,
			expected: "SELECT * FROM logs limit 50",
		},
		{
			name:     "negative limit uses default",
			query:    "SELECT * FROM logs",
			limit:    -1,
			expected: "SELECT * FROM logs LIMIT 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enforceClickHouseLimit(tt.query, tt.limit)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClickHouseQueryParams_Validation(t *testing.T) {
	// Test that the struct has the expected fields
	params := ClickHouseQueryParams{
		DatasourceUID: "test-uid",
		Query:         "SELECT * FROM logs",
		Start:         "now-1h",
		End:           "now",
		Variables: map[string]string{
			"service": "my-app",
		},
		Limit: 100,
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "SELECT * FROM logs", params.Query)
	assert.Equal(t, "now-1h", params.Start)
	assert.Equal(t, "now", params.End)
	assert.Equal(t, "my-app", params.Variables["service"])
	assert.Equal(t, 100, params.Limit)
}

func TestClickHouseQueryResult_Structure(t *testing.T) {
	result := ClickHouseQueryResult{
		Columns: []string{"timestamp", "message", "level"},
		Rows: []map[string]interface{}{
			{"timestamp": "2024-01-15T10:00:00Z", "message": "Test log", "level": "info"},
			{"timestamp": "2024-01-15T10:00:01Z", "message": "Another log", "level": "error"},
		},
		RowCount:       2,
		ProcessedQuery: "SELECT timestamp, message, level FROM logs LIMIT 100",
	}

	assert.Len(t, result.Columns, 3)
	assert.Len(t, result.Rows, 2)
	assert.Equal(t, 2, result.RowCount)
	assert.Equal(t, "Test log", result.Rows[0]["message"])
	assert.Contains(t, result.ProcessedQuery, "LIMIT 100")
}

func TestSubstituteClickHouseMacros_IntervalCalculation(t *testing.T) {
	// Test with different time ranges to verify interval calculation
	tests := []struct {
		name             string
		rangeHours       int
		expectedInterval string
	}{
		{
			name:             "1 hour range",
			rangeHours:       1,
			expectedInterval: "3s", // 3600/1000 = 3.6 -> 3
		},
		{
			name:             "6 hour range",
			rangeHours:       6,
			expectedInterval: "21s", // 21600/1000 = 21.6 -> 21
		},
		{
			name:             "24 hour range",
			rangeHours:       24,
			expectedInterval: "86s", // 86400/1000 = 86.4 -> 86
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
			to := from.Add(time.Duration(tt.rangeHours) * time.Hour)

			result := substituteClickHouseMacros("$__interval", from, to)
			assert.Equal(t, tt.expectedInterval, result)
		})
	}
}

func TestListClickHouseTablesParams_Structure(t *testing.T) {
	params := ListClickHouseTablesParams{
		DatasourceUID: "test-uid",
		Database:      "my_database",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "my_database", params.Database)
}

func TestDescribeClickHouseTableParams_Structure(t *testing.T) {
	params := DescribeClickHouseTableParams{
		DatasourceUID: "test-uid",
		Table:         "my_table",
		Database:      "my_database",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "my_table", params.Table)
	assert.Equal(t, "my_database", params.Database)
}

func TestClickHouseTableInfo_Structure(t *testing.T) {
	info := ClickHouseTableInfo{
		Database:   "default",
		Name:       "events",
		Engine:     "MergeTree",
		TotalRows:  1000000,
		TotalBytes: 52428800,
	}

	assert.Equal(t, "default", info.Database)
	assert.Equal(t, "events", info.Name)
	assert.Equal(t, "MergeTree", info.Engine)
	assert.Equal(t, int64(1000000), info.TotalRows)
	assert.Equal(t, int64(52428800), info.TotalBytes)
}

func TestClickHouseColumnInfo_Structure(t *testing.T) {
	col := ClickHouseColumnInfo{
		Name:              "timestamp",
		Type:              "DateTime64",
		DefaultType:       "DEFAULT",
		DefaultExpression: "now()",
		Comment:           "Event timestamp",
	}

	assert.Equal(t, "timestamp", col.Name)
	assert.Equal(t, "DateTime64", col.Type)
	assert.Equal(t, "DEFAULT", col.DefaultType)
	assert.Equal(t, "now()", col.DefaultExpression)
	assert.Equal(t, "Event timestamp", col.Comment)
}

func TestClickHouseQueryResult_Hints(t *testing.T) {
	result := ClickHouseQueryResult{
		Columns:  []string{"id", "name"},
		Rows:     []map[string]interface{}{},
		RowCount: 0,
		Hints: &EmptyResultHints{
			Summary:          "No data found",
			PossibleCauses:   []string{"Table may not exist"},
			SuggestedActions: []string{"Try a different query"},
		},
	}

	assert.NotNil(t, result.Hints)
	assert.Equal(t, "No data found", result.Hints.Summary)
	assert.Equal(t, 0, result.RowCount)
}

func TestGenerateClickHouseEmptyResultHints(t *testing.T) {
	hints := GenerateEmptyResultHints(HintContext{
		DatasourceType: "clickhouse",
		Query:          "SELECT * FROM test",
	})

	assert.NotNil(t, hints)
	assert.Contains(t, hints.Summary, "ClickHouse")
	// Verify helpful suggestions are included
	found := false
	for _, action := range hints.SuggestedActions {
		if strings.Contains(action, "list_clickhouse_tables") {
			found = true
			break
		}
	}
	assert.True(t, found, "Hints should suggest using list_clickhouse_tables")
}
