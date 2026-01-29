package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestListLokiMetricNamesParams_Structure(t *testing.T) {
	params := ListLokiMetricNamesParams{
		DatasourceUID: "test-uid",
		StartRFC3339:  "2024-01-15T00:00:00Z",
		EndRFC3339:    "2024-01-15T06:00:00Z",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "2024-01-15T00:00:00Z", params.StartRFC3339)
	assert.Equal(t, "2024-01-15T06:00:00Z", params.EndRFC3339)
}

func TestLokiQueryResult_Structure(t *testing.T) {
	result := LokiQueryResult{
		Entries: []LogEntry{
			{
				Timestamp: "1234567890",
				Line:      "test log line",
				Labels:    map[string]string{"app": "test"},
			},
		},
		Hints: []string{},
	}

	assert.Len(t, result.Entries, 1)
	assert.Equal(t, "test log line", result.Entries[0].Line)
	assert.Empty(t, result.Hints)
}

func TestLokiQueryResult_WithHints(t *testing.T) {
	result := LokiQueryResult{
		Entries: []LogEntry{},
		Hints: []string{
			"No data found",
			"Try a broader query",
		},
	}

	assert.Empty(t, result.Entries)
	assert.Len(t, result.Hints, 2)
	assert.Equal(t, "No data found", result.Hints[0])
}

func TestLogEntry_Structure(t *testing.T) {
	t.Run("log entry with line", func(t *testing.T) {
		entry := LogEntry{
			Timestamp: "1705312800000",
			Line:      "ERROR: Connection refused",
			Labels: map[string]string{
				"app": "myservice",
				"env": "prod",
			},
		}

		assert.Equal(t, "1705312800000", entry.Timestamp)
		assert.Equal(t, "ERROR: Connection refused", entry.Line)
		assert.Equal(t, "myservice", entry.Labels["app"])
		assert.Equal(t, "prod", entry.Labels["env"])
	})

	t.Run("metric entry with value", func(t *testing.T) {
		value := 42.5
		entry := LogEntry{
			Timestamp: "1705312800.000",
			Value:     &value,
			Labels: map[string]string{
				"__name__": "log_count",
			},
		}

		assert.NotNil(t, entry.Value)
		assert.Equal(t, 42.5, *entry.Value)
		assert.Equal(t, "log_count", entry.Labels["__name__"])
	})

	t.Run("metric entry with values", func(t *testing.T) {
		entry := LogEntry{
			Values: []MetricValue{
				{Timestamp: "1705312800.000", Value: 10.0},
				{Timestamp: "1705312860.000", Value: 15.0},
				{Timestamp: "1705312920.000", Value: 12.0},
			},
			Labels: map[string]string{
				"job": "myapp",
			},
		}

		assert.Len(t, entry.Values, 3)
		assert.Equal(t, 10.0, entry.Values[0].Value)
		assert.Equal(t, 15.0, entry.Values[1].Value)
	})
}

func TestMetricValue_Structure(t *testing.T) {
	mv := MetricValue{
		Timestamp: "1705312800.000",
		Value:     123.456,
	}

	assert.Equal(t, "1705312800.000", mv.Timestamp)
	assert.InDelta(t, 123.456, mv.Value, 0.001)
}

func TestEnforceLogLimit(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{
			name:     "zero returns default",
			input:    0,
			expected: DefaultLokiLogLimit,
		},
		{
			name:     "negative returns default",
			input:    -1,
			expected: DefaultLokiLogLimit,
		},
		{
			name:     "within bounds unchanged",
			input:    50,
			expected: 50,
		},
		{
			name:     "exceeds max capped",
			input:    1000,
			expected: MaxLokiLogLimit,
		},
		{
			name:     "at max unchanged",
			input:    MaxLokiLogLimit,
			expected: MaxLokiLogLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enforceLogLimit(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
