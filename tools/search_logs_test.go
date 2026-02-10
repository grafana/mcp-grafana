//go:build unit

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnforceSearchLogsLimit(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{
			name:     "zero returns default",
			input:    0,
			expected: DefaultSearchLogsLimit,
		},
		{
			name:     "negative returns default",
			input:    -1,
			expected: DefaultSearchLogsLimit,
		},
		{
			name:     "valid limit returned as-is",
			input:    50,
			expected: 50,
		},
		{
			name:     "exceeds max returns max",
			input:    5000,
			expected: MaxSearchLogsLimit,
		},
		{
			name:     "exactly max returns max",
			input:    MaxSearchLogsLimit,
			expected: MaxSearchLogsLimit,
		},
		{
			name:     "default value",
			input:    100,
			expected: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enforceSearchLogsLimit(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsRegexPattern(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		expected bool
	}{
		{
			name:     "simple text",
			pattern:  "error",
			expected: false,
		},
		{
			name:     "text with spaces",
			pattern:  "connection refused",
			expected: false,
		},
		{
			name:     "dot character",
			pattern:  "error.message",
			expected: true,
		},
		{
			name:     "asterisk wildcard",
			pattern:  "error*",
			expected: true,
		},
		{
			name:     "plus quantifier",
			pattern:  "error+",
			expected: true,
		},
		{
			name:     "question mark",
			pattern:  "error?",
			expected: true,
		},
		{
			name:     "caret anchor",
			pattern:  "^error",
			expected: true,
		},
		{
			name:     "dollar anchor",
			pattern:  "error$",
			expected: true,
		},
		{
			name:     "character class",
			pattern:  "[Ee]rror",
			expected: true,
		},
		{
			name:     "grouping",
			pattern:  "(error|warning)",
			expected: true,
		},
		{
			name:     "curly braces quantifier",
			pattern:  "e{2,3}",
			expected: true,
		},
		{
			name:     "pipe alternation",
			pattern:  "error|warning",
			expected: true,
		},
		{
			name:     "backslash escape",
			pattern:  `error\.log`,
			expected: true,
		},
		{
			name:     "complex regex",
			pattern:  `\d{4}-\d{2}-\d{2}`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRegexPattern(tt.pattern)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEscapeLogQLPattern(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple text",
			input:    "error",
			expected: "error",
		},
		{
			name:     "text with double quote",
			input:    `say "hello"`,
			expected: `say \"hello\"`,
		},
		{
			name:     "text with backslash",
			input:    `path\to\file`,
			expected: `path\\to\\file`,
		},
		{
			name:     "mixed special characters",
			input:    `"path\file"`,
			expected: `\"path\\file\"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeLogQLPattern(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEscapeClickHousePattern(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple text",
			input:    "error",
			expected: "error",
		},
		{
			name:     "text with single quote",
			input:    "it's an error",
			expected: "it''s an error",
		},
		{
			name:     "text with percent",
			input:    "100% complete",
			expected: `100\% complete`,
		},
		{
			name:     "text with underscore",
			input:    "error_code",
			expected: `error\_code`,
		},
		{
			name:     "mixed special characters",
			input:    "it's 100% done_now",
			expected: `it''s 100\% done\_now`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeClickHousePattern(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateLokiQuery(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		expected string
	}{
		{
			name:     "simple text uses line contains",
			pattern:  "error",
			expected: `{} |= "error"`,
		},
		{
			name:     "text with spaces",
			pattern:  "connection refused",
			expected: `{} |= "connection refused"`,
		},
		{
			name:     "regex pattern uses regex filter",
			pattern:  "error|warning",
			expected: `{} |~ "error|warning"`,
		},
		{
			name:     "pattern with dot uses regex",
			pattern:  "error.message",
			expected: `{} |~ "error.message"`,
		},
		{
			name:     "pattern with double quotes escaped",
			pattern:  `say "hello"`,
			expected: `{} |= "say \"hello\""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateLokiQuery(tt.pattern)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateClickHouseLogQuery(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		limit    int
		useRegex bool
		contains []string // Substrings that must be present
	}{
		{
			name:     "simple text query",
			pattern:  "error",
			limit:    100,
			useRegex: false,
			contains: []string{
				"SELECT",
				"Timestamp",
				"Body",
				"FROM otel_logs",
				"ILIKE '%error%'",
				"$__timeFilter(Timestamp)",
				"ORDER BY Timestamp DESC",
				"LIMIT 100",
			},
		},
		{
			name:     "pattern with special SQL chars",
			pattern:  "it's an error",
			limit:    50,
			useRegex: false,
			contains: []string{
				"ILIKE '%it''s an error%'",
				"LIMIT 50",
			},
		},
		{
			name:     "pattern with ILIKE special chars",
			pattern:  "100% done",
			limit:    100,
			useRegex: false,
			contains: []string{
				`ILIKE '%100\% done%'`,
			},
		},
		{
			name:     "regex pattern with match()",
			pattern:  "timeout|connection.*refused",
			limit:    100,
			useRegex: true,
			contains: []string{
				"match(Body, 'timeout|connection.*refused')",
				"$__timeFilter(Timestamp)",
				"LIMIT 100",
			},
		},
		{
			name:     "regex pattern with single quotes",
			pattern:  "it's.*error",
			limit:    50,
			useRegex: true,
			contains: []string{
				"match(Body, 'it''s.*error')",
				"LIMIT 50",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateClickHouseLogQuery("", tt.pattern, tt.limit, tt.useRegex) // empty table = default otel_logs
			for _, substr := range tt.contains {
				assert.Contains(t, result, substr)
			}
		})
	}
}

func TestSearchLogsParams_Structure(t *testing.T) {
	// Test that the struct can be properly instantiated with all fields
	params := SearchLogsParams{
		DatasourceUID: "test-uid",
		Pattern:       "error",
		Start:         "now-1h",
		End:           "now",
		Limit:         100,
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "error", params.Pattern)
	assert.Equal(t, "now-1h", params.Start)
	assert.Equal(t, "now", params.End)
	assert.Equal(t, 100, params.Limit)
}

func TestSearchLogsResult_Structure(t *testing.T) {
	// Test the result structure
	result := SearchLogsResult{
		Logs: []LogResult{
			{
				Timestamp: "2024-01-15T10:00:00Z",
				Message:   "Error occurred",
				Labels:    map[string]string{"service": "api", "level": "error"},
			},
			{
				Timestamp: "2024-01-15T10:00:01Z",
				Message:   "Another error",
				Labels:    map[string]string{"service": "web"},
			},
		},
		DatasourceType: "loki",
		Query:          `{} |= "error"`,
		TotalFound:     2,
	}

	assert.Len(t, result.Logs, 2)
	assert.Equal(t, "loki", result.DatasourceType)
	assert.Equal(t, `{} |= "error"`, result.Query)
	assert.Equal(t, 2, result.TotalFound)
	assert.Equal(t, "Error occurred", result.Logs[0].Message)
	assert.Equal(t, "api", result.Logs[0].Labels["service"])
}

func TestLogResult_Structure(t *testing.T) {
	// Test individual log result
	log := LogResult{
		Timestamp: "2024-01-15T10:00:00.123456789Z",
		Message:   "This is a test log message",
		Labels: map[string]string{
			"service": "my-service",
			"level":   "info",
			"pod":     "pod-abc123",
		},
	}

	assert.Equal(t, "2024-01-15T10:00:00.123456789Z", log.Timestamp)
	assert.Equal(t, "This is a test log message", log.Message)
	assert.Len(t, log.Labels, 3)
	assert.Equal(t, "my-service", log.Labels["service"])
}

func TestSearchLogsResult_WithHints(t *testing.T) {
	// Test result with hints when no data found
	result := SearchLogsResult{
		Logs:           []LogResult{},
		DatasourceType: "loki",
		Query:          `{} |= "nonexistent"`,
		TotalFound:     0,
		Hints: []string{
			"No logs found matching the pattern. Possible reasons:",
			"- Pattern may not match any log content - try a simpler pattern",
		},
	}

	assert.Empty(t, result.Logs)
	assert.Equal(t, 0, result.TotalFound)
	assert.NotEmpty(t, result.Hints)
	assert.Contains(t, result.Hints[0], "No logs found")
}

func TestGenerateSearchLogsHints_Loki(t *testing.T) {
	hints := generateSearchLogsHints(LokiDatasourceType, "error")

	assert.NotEmpty(t, hints)
	assert.Contains(t, hints[0], "No logs found")

	// Check that Loki-specific hints are included
	hintsStr := ""
	for _, h := range hints {
		hintsStr += h + " "
	}
	assert.Contains(t, hintsStr, "list_loki_label_names")
	assert.Contains(t, hintsStr, "query_loki_stats")
}

func TestGenerateSearchLogsHints_ClickHouse(t *testing.T) {
	hints := generateSearchLogsHints(ClickHouseDatasourceType, "error")

	assert.NotEmpty(t, hints)
	assert.Contains(t, hints[0], "No logs found")

	// Check that ClickHouse-specific hints are included
	hintsStr := ""
	for _, h := range hints {
		hintsStr += h + " "
	}
	assert.Contains(t, hintsStr, "otel_logs")
	assert.Contains(t, hintsStr, "list_clickhouse_tables")
	assert.Contains(t, hintsStr, "describe_clickhouse_table")
}

func TestGenerateSearchLogsHints_RegexPattern(t *testing.T) {
	// When using a regex pattern in Loki, should include regex-specific hint
	hints := generateSearchLogsHints(LokiDatasourceType, "error|warning")

	hintsStr := ""
	for _, h := range hints {
		hintsStr += h + " "
	}
	assert.Contains(t, hintsStr, "Regex pattern")
}

func TestGenerateSearchLogsHints_UnknownDatasource(t *testing.T) {
	hints := generateSearchLogsHints("unknown-type", "error")

	assert.NotEmpty(t, hints)
	// Should have generic hints
	hintsStr := ""
	for _, h := range hints {
		hintsStr += h + " "
	}
	assert.Contains(t, hintsStr, "datasource is accessible")
}

func TestConstants(t *testing.T) {
	// Verify constant values
	assert.Equal(t, 100, DefaultSearchLogsLimit)
	assert.Equal(t, 1000, MaxSearchLogsLimit)
	assert.Equal(t, "loki", LokiDatasourceType)
}
