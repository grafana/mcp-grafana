//go:build unit

package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnforceCloudWatchLogsLimit(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{name: "zero returns default", input: 0, expected: DefaultCloudWatchLogsLimit},
		{name: "negative returns default", input: -1, expected: DefaultCloudWatchLogsLimit},
		{name: "within range", input: 50, expected: 50},
		{name: "exactly default", input: 100, expected: 100},
		{name: "exactly max", input: MaxCloudWatchLogsLimit, expected: MaxCloudWatchLogsLimit},
		{name: "exceeds max", input: 5000, expected: MaxCloudWatchLogsLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, enforceCloudWatchLogsLimit(tt.input))
		})
	}
}

func TestParseCloudWatchLogGroupsResponse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    []string
		expectError bool
	}{
		{
			name:     "valid single log group",
			input:    `[{"value":{"arn":"arn:aws:logs:us-east-1:123:log-group:/ecs/core-prod","name":"/ecs/core-prod"},"accountId":"123"}]`,
			expected: []string{"/ecs/core-prod"},
		},
		{
			name:     "multiple log groups",
			input:    `[{"value":{"arn":"arn:aws:logs:us-east-1:123:log-group:/ecs/prod","name":"/ecs/prod"}},{"value":{"arn":"arn:aws:logs:us-east-1:123:log-group:/ecs/staging","name":"/ecs/staging"}}]`,
			expected: []string{"/ecs/prod", "/ecs/staging"},
		},
		{
			name:     "empty array",
			input:    `[]`,
			expected: []string{},
		},
		{
			name:        "invalid JSON",
			input:       `not json`,
			expectError: true,
		},
		{
			name:     "no accountId field",
			input:    `[{"value":{"arn":"some-arn","name":"/app/logs"}}]`,
			expected: []string{"/app/logs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseCloudWatchLogGroupsResponse([]byte(tt.input), 1024*1024)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseCloudWatchLogGroupFieldsResponse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    []string
		expectError bool
	}{
		{
			name:     "valid fields",
			input:    `[{"value":{"name":"@timestamp","percent":100}},{"value":{"name":"@message","percent":95}}]`,
			expected: []string{"@timestamp", "@message"},
		},
		{
			name:     "single field",
			input:    `[{"value":{"name":"level","percent":50}}]`,
			expected: []string{"level"},
		},
		{
			name:     "empty array",
			input:    `[]`,
			expected: []string{},
		},
		{
			name:        "invalid JSON",
			input:       `{bad`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseCloudWatchLogGroupFieldsResponse([]byte(tt.input), 1024*1024)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatLogValue(t *testing.T) {
	tests := []struct {
		name      string
		value     interface{}
		fieldType string
		expected  string
	}{
		{name: "string value", value: "hello world", fieldType: "string", expected: "hello world"},
		{name: "float64 number", value: float64(25.5), fieldType: "number", expected: "25.5"},
		{name: "float64 integer", value: float64(42), fieldType: "number", expected: "42"},
		{name: "float64 timestamp", value: float64(1705312800000), fieldType: "time",
			expected: time.UnixMilli(1705312800000).UTC().Format(time.RFC3339Nano)},
		{name: "int64 number", value: int64(42), fieldType: "number", expected: "42"},
		{name: "int64 timestamp", value: int64(1705312800000), fieldType: "time",
			expected: time.UnixMilli(1705312800000).UTC().Format(time.RFC3339Nano)},
		{name: "nil value", value: nil, fieldType: "string", expected: ""},
		{name: "bool fallback", value: true, fieldType: "boolean", expected: "true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatLogValue(tt.value, tt.fieldType))
		})
	}
}

func TestExtractQueryID(t *testing.T) {
	t.Run("valid queryId field", func(t *testing.T) {
		resp := buildLogsResponse(t, "",
			[]fieldDef{{Name: "queryId", Type: "string"}},
			[][]interface{}{{"abc-123-query-id"}},
			"",
		)
		qid, err := extractQueryID(resp)
		require.NoError(t, err)
		assert.Equal(t, "abc-123-query-id", qid)
	})

	t.Run("no frames", func(t *testing.T) {
		resp := buildLogsResponse(t, "", nil, nil, "")
		_, err := extractQueryID(resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no queryId found")
	})

	t.Run("error in response", func(t *testing.T) {
		resp := buildLogsResponse(t, "", nil, nil, "something went wrong")
		_, err := extractQueryID(resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "something went wrong")
	})
}

func TestExtractQueryStatus(t *testing.T) {
	t.Run("complete status", func(t *testing.T) {
		resp := buildLogsResponse(t, "Complete", nil, nil, "")
		assert.Equal(t, "Complete", extractQueryStatus(resp))
	})

	t.Run("running status", func(t *testing.T) {
		resp := buildLogsResponse(t, "Running", nil, nil, "")
		assert.Equal(t, "Running", extractQueryStatus(resp))
	})

	t.Run("no meta", func(t *testing.T) {
		resp := buildLogsResponse(t, "", nil, nil, "")
		assert.Equal(t, "", extractQueryStatus(resp))
	})

	t.Run("no frames", func(t *testing.T) {
		resp := &cloudWatchQueryResponse{}
		data := `{"results":{"A":{"frames":[]}}}`
		_ = json.Unmarshal([]byte(data), resp)
		assert.Equal(t, "", extractQueryStatus(resp))
	})
}

func TestParseLogsQueryResponse(t *testing.T) {
	t.Run("complete response with multiple fields", func(t *testing.T) {
		fields := []fieldDef{
			{Name: "@timestamp", Type: "time"},
			{Name: "@message", Type: "string"},
			{Name: "level", Type: "string"},
		}
		values := [][]interface{}{
			{float64(1705312800000), float64(1705312900000)},
			{"ERROR: something failed", "INFO: all good"},
			{"error", "info"},
		}
		resp := buildLogsResponse(t, "Complete", fields, values, "")

		result, err := parseLogsQueryResponse(resp, "fields @timestamp, @message, level", 100)
		require.NoError(t, err)
		assert.Equal(t, "Complete", result.Status)
		assert.Equal(t, 2, result.TotalFound)
		assert.Len(t, result.Logs, 2)

		assert.Equal(t, "ERROR: something failed", result.Logs[0].Fields["@message"])
		assert.Equal(t, "error", result.Logs[0].Fields["level"])
		assert.Contains(t, result.Logs[0].Fields["@timestamp"], "2024-01-15")
	})

	t.Run("strips @ptr field", func(t *testing.T) {
		fields := []fieldDef{
			{Name: "@timestamp", Type: "time"},
			{Name: "@message", Type: "string"},
			{Name: "@ptr", Type: "string"},
		}
		values := [][]interface{}{
			{float64(1705312800000)},
			{"test message"},
			{"some-internal-pointer"},
		}
		resp := buildLogsResponse(t, "Complete", fields, values, "")

		result, err := parseLogsQueryResponse(resp, "test", 100)
		require.NoError(t, err)
		assert.Len(t, result.Logs, 1)
		_, hasPtr := result.Logs[0].Fields["@ptr"]
		assert.False(t, hasPtr, "@ptr should be stripped")
		assert.Equal(t, "test message", result.Logs[0].Fields["@message"])
	})

	t.Run("strips grafana internal fields", func(t *testing.T) {
		fields := []fieldDef{
			{Name: "@timestamp", Type: "time"},
			{Name: "@message", Type: "string"},
			{Name: "__log__grafana_internal__", Type: "string"},
			{Name: "__logstream__grafana_internal__", Type: "string"},
		}
		values := [][]interface{}{
			{float64(1705312800000)},
			{"test message"},
			{"442042515479:/ecs/core-prod"},
			{"api/apiContainer/abc123"},
		}
		resp := buildLogsResponse(t, "Complete", fields, values, "")

		result, err := parseLogsQueryResponse(resp, "test", 100)
		require.NoError(t, err)
		assert.Len(t, result.Logs, 1)
		_, hasLog := result.Logs[0].Fields["__log__grafana_internal__"]
		assert.False(t, hasLog, "__log__grafana_internal__ should be stripped")
		_, hasLogStream := result.Logs[0].Fields["__logstream__grafana_internal__"]
		assert.False(t, hasLogStream, "__logstream__grafana_internal__ should be stripped")
	})

	t.Run("empty result generates hints", func(t *testing.T) {
		fields := []fieldDef{
			{Name: "@timestamp", Type: "time"},
			{Name: "@message", Type: "string"},
		}
		values := [][]interface{}{
			{},
			{},
		}
		resp := buildLogsResponse(t, "Complete", fields, values, "")

		result, err := parseLogsQueryResponse(resp, "test", 100)
		require.NoError(t, err)
		assert.Equal(t, 0, result.TotalFound)
		assert.NotEmpty(t, result.Hints)
	})

	t.Run("error in response", func(t *testing.T) {
		resp := buildLogsResponse(t, "", nil, nil, "query syntax error")

		_, err := parseLogsQueryResponse(resp, "bad query", 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "query syntax error")
	})

	t.Run("respects limit", func(t *testing.T) {
		fields := []fieldDef{
			{Name: "@message", Type: "string"},
		}
		values := [][]interface{}{
			{"msg1", "msg2", "msg3", "msg4", "msg5"},
		}
		resp := buildLogsResponse(t, "Complete", fields, values, "")

		result, err := parseLogsQueryResponse(resp, "test", 3)
		require.NoError(t, err)
		assert.Len(t, result.Logs, 3)
		assert.Equal(t, 3, result.TotalFound)
	})
}

func TestGenerateCloudWatchLogsEmptyResultHints(t *testing.T) {
	hints := generateCloudWatchLogsEmptyResultHints()

	assert.NotEmpty(t, hints)
	assert.GreaterOrEqual(t, len(hints), 5)
	assert.Contains(t, hints[0], "No log data found")

	hintsStr := strings.Join(hints, " ")
	assert.Contains(t, hintsStr, "list_cloudwatch_log_groups")
	assert.Contains(t, hintsStr, "list_cloudwatch_log_group_fields")
}

func TestCloudWatchLogsQueryResult_Structure(t *testing.T) {
	result := CloudWatchLogsQueryResult{
		Logs: []CloudWatchLogEntry{
			{Fields: map[string]string{"@timestamp": "2024-01-15T10:00:00Z", "@message": "ERROR: test"}},
			{Fields: map[string]string{"@timestamp": "2024-01-15T10:01:00Z", "@message": "INFO: ok"}},
		},
		Query:      "fields @timestamp, @message",
		TotalFound: 2,
		Status:     "Complete",
	}

	assert.Len(t, result.Logs, 2)
	assert.Equal(t, "ERROR: test", result.Logs[0].Fields["@message"])
	assert.Equal(t, 2, result.TotalFound)
	assert.Equal(t, "Complete", result.Status)
	assert.Nil(t, result.Hints)
}

func TestCloudWatchLogsQueryResult_JSONSerialization(t *testing.T) {
	t.Run("hints omitted when nil", func(t *testing.T) {
		result := CloudWatchLogsQueryResult{
			Logs:       []CloudWatchLogEntry{},
			Query:      "test",
			TotalFound: 0,
			Status:     "Complete",
		}

		data, err := json.Marshal(result)
		require.NoError(t, err)

		var m map[string]interface{}
		require.NoError(t, json.Unmarshal(data, &m))
		_, hasHints := m["hints"]
		assert.False(t, hasHints, "hints should be omitted when nil")
	})

	t.Run("hints included when present", func(t *testing.T) {
		result := CloudWatchLogsQueryResult{
			Logs:       []CloudWatchLogEntry{},
			Query:      "test",
			TotalFound: 0,
			Status:     "Complete",
			Hints:      []string{"hint1"},
		}

		data, err := json.Marshal(result)
		require.NoError(t, err)

		var m map[string]interface{}
		require.NoError(t, json.Unmarshal(data, &m))
		_, hasHints := m["hints"]
		assert.True(t, hasHints, "hints should be included when present")
	})
}

// Helper types and functions for building test responses

type fieldDef struct {
	Name string
	Type string
}

// buildLogsResponse constructs a cloudWatchQueryResponse via JSON round-trip,
// avoiding inline anonymous struct matching issues.
func buildLogsResponse(t *testing.T, status string, fields []fieldDef, values [][]interface{}, errMsg string) *cloudWatchQueryResponse {
	t.Helper()

	type frameMeta struct {
		Custom struct {
			Status string `json:"Status"`
		} `json:"custom,omitempty"`
	}
	type schemaField struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	type frame struct {
		Schema struct {
			Meta   *frameMeta    `json:"meta,omitempty"`
			Fields []schemaField `json:"fields"`
		} `json:"schema"`
		Data struct {
			Values [][]interface{} `json:"values"`
		} `json:"data"`
	}
	type resultEntry struct {
		Frames []frame `json:"frames,omitempty"`
		Error  string  `json:"error,omitempty"`
	}

	r := resultEntry{Error: errMsg}

	if fields != nil || status != "" {
		f := frame{}
		if status != "" {
			f.Schema.Meta = &frameMeta{}
			f.Schema.Meta.Custom.Status = status
		}
		if fields != nil {
			f.Schema.Fields = make([]schemaField, len(fields))
			for i, fd := range fields {
				f.Schema.Fields[i] = schemaField{Name: fd.Name, Type: fd.Type}
			}
		}
		if values != nil {
			f.Data.Values = values
		}
		r.Frames = []frame{f}
	}

	wrapper := map[string]map[string]resultEntry{
		"results": {"A": r},
	}

	data, err := json.Marshal(wrapper)
	require.NoError(t, err)

	var resp cloudWatchQueryResponse
	require.NoError(t, json.Unmarshal(data, &resp))
	return &resp
}
