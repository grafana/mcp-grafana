package tools

import (
	"context"
	"encoding/json"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnforceLogLimit(t *testing.T) {
	tests := []struct {
		name           string
		maxLokiLimit   int
		requestedLimit int
		expectedLimit  int
	}{
		{
			name:           "default limit when requested is 0",
			maxLokiLimit:   100,
			requestedLimit: 0,
			expectedLimit:  DefaultLokiLogLimit,
		},
		{
			name:           "default limit when requested is negative",
			maxLokiLimit:   100,
			requestedLimit: -5,
			expectedLimit:  DefaultLokiLogLimit,
		},
		{
			name:           "requested limit within bounds",
			maxLokiLimit:   100,
			requestedLimit: 50,
			expectedLimit:  50,
		},
		{
			name:           "requested limit exceeds max",
			maxLokiLimit:   100,
			requestedLimit: 150,
			expectedLimit:  100,
		},
		{
			name:           "custom max limit from config",
			maxLokiLimit:   500,
			requestedLimit: 300,
			expectedLimit:  300,
		},
		{
			name:           "requested limit exceeds custom max",
			maxLokiLimit:   500,
			requestedLimit: 600,
			expectedLimit:  500,
		},
		{
			name:           "fallback to default max when config is 0",
			maxLokiLimit:   0,
			requestedLimit: 150,
			expectedLimit:  MaxLokiLogLimit, // 100
		},
		{
			name:           "fallback to default max when config is negative",
			maxLokiLimit:   -10,
			requestedLimit: 150,
			expectedLimit:  MaxLokiLogLimit, // 100
		},
		{
			name:           "default limit capped to maxLimit when maxLimit is lower",
			maxLokiLimit:   5,
			requestedLimit: 0,
			expectedLimit:  5, // DefaultLokiLogLimit (10) > maxLimit (5), so use maxLimit
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mcpgrafana.GrafanaConfig{
				MaxLokiLogLimit: tc.maxLokiLimit,
			}
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)

			result := enforceLogLimit(ctx, tc.requestedLimit)
			assert.Equal(t, tc.expectedLimit, result)
		})
	}
}

func TestHasCategorizeLabelsFlag(t *testing.T) {
	assert.True(t, hasCategorizeLabelsFlag([]string{"categorize-labels"}))
	assert.True(t, hasCategorizeLabelsFlag([]string{"other", "categorize-labels"}))
	assert.False(t, hasCategorizeLabelsFlag(nil))
	assert.False(t, hasCategorizeLabelsFlag([]string{}))
	assert.False(t, hasCategorizeLabelsFlag([]string{"other"}))
}

func TestCategorizedLabelsParsing(t *testing.T) {
	// Simulate a Loki response with categorize-labels encoding flag.
	// values[2] carries the categorized labels object.
	rawResponse := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"encodingFlags": ["categorize-labels"],
			"result": [
				{
					"stream": {"app": "frontend", "namespace": "default"},
					"values": [
						[
							"1693996529000222496",
							"level=info msg=\"request handled\"",
							{
								"structuredMetadata": {"traceID": "abc123", "service_name": "web"},
								"parsed": {"level": "info", "msg": "request handled"}
							}
						],
						[
							"1693996530000000000",
							"level=error msg=\"timeout\"",
							{
								"structuredMetadata": {"traceID": "def456"},
								"parsed": {"level": "error"}
							}
						]
					]
				}
			]
		}
	}`

	var response lokiQueryResponse
	require.NoError(t, json.Unmarshal([]byte(rawResponse), &response))

	assert.Equal(t, "streams", response.Data.ResultType)
	assert.True(t, hasCategorizeLabelsFlag(response.Data.EncodingFlags))

	// Parse streams
	var streams []LokiLogStream
	require.NoError(t, json.Unmarshal(response.Data.Result, &streams))
	require.Len(t, streams, 1)

	stream := streams[0]
	// Stream labels should only contain index labels
	assert.Equal(t, map[string]string{"app": "frontend", "namespace": "default"}, stream.Stream)
	require.Len(t, stream.Values, 2)

	// First entry — parse the third element
	require.Len(t, stream.Values[0], 3)
	var cats1 categorizedLabels
	require.NoError(t, json.Unmarshal(stream.Values[0][2], &cats1))
	assert.Equal(t, map[string]string{"traceID": "abc123", "service_name": "web"}, cats1.StructuredMetadata)
	assert.Equal(t, map[string]string{"level": "info", "msg": "request handled"}, cats1.Parsed)

	// Second entry
	var cats2 categorizedLabels
	require.NoError(t, json.Unmarshal(stream.Values[1][2], &cats2))
	assert.Equal(t, map[string]string{"traceID": "def456"}, cats2.StructuredMetadata)
	assert.Equal(t, map[string]string{"level": "error"}, cats2.Parsed)
}

func TestCategorizedLabelsBackwardCompat(t *testing.T) {
	// Without the encoding flag, values only have 2 elements (old Loki).
	rawResponse := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [
				{
					"stream": {"app": "backend"},
					"values": [
						["1693996529000222496", "some log line"]
					]
				}
			]
		}
	}`

	var response lokiQueryResponse
	require.NoError(t, json.Unmarshal([]byte(rawResponse), &response))

	assert.False(t, hasCategorizeLabelsFlag(response.Data.EncodingFlags))

	var streams []LokiLogStream
	require.NoError(t, json.Unmarshal(response.Data.Result, &streams))
	require.Len(t, streams, 1)
	require.Len(t, streams[0].Values, 1)
	require.Len(t, streams[0].Values[0], 2) // Only timestamp + line, no third element
}

func TestCompactLogEntries(t *testing.T) {
	t.Run("empty input returns empty, non-nil slice", func(t *testing.T) {
		got := compactLogEntries(nil)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})

	t.Run("groups lines by stream preserving order", func(t *testing.T) {
		backend := map[string]string{"app": "backend", "pod": "backend-1"}
		worker := map[string]string{"app": "worker", "pod": "worker-1"}
		entries := []LogEntry{
			{Timestamp: "t1", Line: "b-one", Labels: backend, StructuredMetadata: map[string]string{"trace_id": "x"}},
			{Timestamp: "t2", Line: "w-one", Labels: worker},
			{Timestamp: "t3", Line: "b-two", Labels: backend, Parsed: map[string]string{"level": "error"}},
		}

		got := compactLogEntries(entries)

		// Two distinct streams, in first-seen order.
		require.Len(t, got, 2)
		assert.Equal(t, backend, got[0].Labels)
		assert.Equal(t, worker, got[1].Labels)

		require.Len(t, got[0].Lines, 2)
		assert.Equal(t, "t1", got[0].Lines[0].Timestamp)
		assert.Equal(t, "b-one", got[0].Lines[0].Line)
		assert.Equal(t, "t3", got[0].Lines[1].Timestamp)
		assert.Equal(t, "b-two", got[0].Lines[1].Line)

		require.Len(t, got[1].Lines, 1)
		assert.Equal(t, "t2", got[1].Lines[0].Timestamp)
		assert.Equal(t, "w-one", got[1].Lines[0].Line)

		// trace_id only appears on one backend entry → varying, so per-line.
		assert.Equal(t, map[string]string{"trace_id": "x"}, got[0].Lines[0].StructuredMetadata)
		assert.Nil(t, got[0].Lines[1].StructuredMetadata)
		// Parsed "level" only on one entry → per-line.
		assert.Equal(t, map[string]string{"level": "error"}, got[0].Lines[1].Parsed)
	})

	t.Run("label sets are compared by content, not map identity", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "t1", Line: "one", Labels: map[string]string{"a": "1", "b": "2"}},
			{Timestamp: "t2", Line: "two", Labels: map[string]string{"b": "2", "a": "1"}},
		}

		got := compactLogEntries(entries)

		require.Len(t, got, 1)
		assert.Equal(t, "t1", got[0].Lines[0].Timestamp)
		assert.Equal(t, "t2", got[0].Lines[1].Timestamp)
	})

	t.Run("constant metadata hoisted to stream header", func(t *testing.T) {
		entries := []LogEntry{
			{
				Timestamp:          "t1",
				Line:               "line1",
				Labels:             map[string]string{"app": "x"},
				StructuredMetadata: map[string]string{"deployment": "d1", "detected_level": "info"},
				Parsed:             map[string]string{"caller": "main.go"},
			},
			{
				Timestamp:          "t2",
				Line:               "line2",
				Labels:             map[string]string{"app": "x"},
				StructuredMetadata: map[string]string{"deployment": "d1", "detected_level": "info"},
				Parsed:             map[string]string{"caller": "main.go"},
			},
		}
		streams := compactLogEntries(entries)
		require.Len(t, streams, 1)
		assert.Equal(t, map[string]string{"deployment": "d1", "detected_level": "info"}, streams[0].StructuredMetadata)
		assert.Equal(t, map[string]string{"caller": "main.go"}, streams[0].Parsed)
		for _, line := range streams[0].Lines {
			assert.Nil(t, line.StructuredMetadata, "constant keys should not appear per-line")
			assert.Nil(t, line.Parsed, "constant keys should not appear per-line")
		}
	})

	t.Run("varying metadata kept per-line", func(t *testing.T) {
		entries := []LogEntry{
			{
				Timestamp:          "t1",
				Line:               "line1",
				Labels:             map[string]string{"app": "x"},
				StructuredMetadata: map[string]string{"pod": "pod-a", "node": "node-1"},
			},
			{
				Timestamp:          "t2",
				Line:               "line2",
				Labels:             map[string]string{"app": "x"},
				StructuredMetadata: map[string]string{"pod": "pod-b", "node": "node-2"},
			},
		}
		streams := compactLogEntries(entries)
		require.Len(t, streams, 1)
		assert.Nil(t, streams[0].StructuredMetadata, "no constant keys to hoist")
		assert.Equal(t, map[string]string{"pod": "pod-a", "node": "node-1"}, streams[0].Lines[0].StructuredMetadata)
		assert.Equal(t, map[string]string{"pod": "pod-b", "node": "node-2"}, streams[0].Lines[1].StructuredMetadata)
	})

	t.Run("mixed constant and varying metadata", func(t *testing.T) {
		entries := []LogEntry{
			{
				Timestamp:          "t1",
				Line:               "line1",
				Labels:             map[string]string{"app": "x"},
				StructuredMetadata: map[string]string{"deployment": "d1", "pod": "pod-a"},
				Parsed:             map[string]string{"level": "info", "caller": "a.go"},
			},
			{
				Timestamp:          "t2",
				Line:               "line2",
				Labels:             map[string]string{"app": "x"},
				StructuredMetadata: map[string]string{"deployment": "d1", "pod": "pod-b"},
				Parsed:             map[string]string{"level": "info", "caller": "b.go"},
			},
			{
				Timestamp:          "t3",
				Line:               "line3",
				Labels:             map[string]string{"app": "x"},
				StructuredMetadata: map[string]string{"deployment": "d1", "pod": "pod-c"},
				Parsed:             map[string]string{"level": "info", "caller": "c.go"},
			},
		}
		streams := compactLogEntries(entries)
		require.Len(t, streams, 1)
		s := streams[0]

		assert.Equal(t, map[string]string{"deployment": "d1"}, s.StructuredMetadata)
		assert.Equal(t, map[string]string{"level": "info"}, s.Parsed)

		assert.Equal(t, map[string]string{"pod": "pod-a"}, s.Lines[0].StructuredMetadata)
		assert.Equal(t, map[string]string{"pod": "pod-b"}, s.Lines[1].StructuredMetadata)
		assert.Equal(t, map[string]string{"pod": "pod-c"}, s.Lines[2].StructuredMetadata)

		assert.Equal(t, map[string]string{"caller": "a.go"}, s.Lines[0].Parsed)
		assert.Equal(t, map[string]string{"caller": "b.go"}, s.Lines[1].Parsed)
		assert.Equal(t, map[string]string{"caller": "c.go"}, s.Lines[2].Parsed)
	})

	t.Run("single-entry stream hoists metadata to header", func(t *testing.T) {
		entries := []LogEntry{
			{
				Timestamp:          "t1",
				Line:               "line1",
				Labels:             map[string]string{"app": "x"},
				StructuredMetadata: map[string]string{"pod": "pod-a"},
			},
			{
				Timestamp:          "t2",
				Line:               "line2",
				Labels:             map[string]string{"app": "y"},
				StructuredMetadata: map[string]string{"pod": "pod-b"},
			},
		}
		streams := compactLogEntries(entries)
		require.Len(t, streams, 2)
		assert.Equal(t, map[string]string{"pod": "pod-a"}, streams[0].StructuredMetadata)
		assert.Nil(t, streams[0].Lines[0].StructuredMetadata)
		assert.Equal(t, map[string]string{"pod": "pod-b"}, streams[1].StructuredMetadata)
		assert.Nil(t, streams[1].Lines[0].StructuredMetadata)
	})

	t.Run("metadata key present on some lines but not others", func(t *testing.T) {
		entries := []LogEntry{
			{
				Timestamp:          "t1",
				Line:               "line1",
				Labels:             map[string]string{"app": "x"},
				StructuredMetadata: map[string]string{"pod": "pod-a", "extra": "val"},
			},
			{
				Timestamp:          "t2",
				Line:               "line2",
				Labels:             map[string]string{"app": "x"},
				StructuredMetadata: map[string]string{"pod": "pod-a"},
			},
		}
		streams := compactLogEntries(entries)
		require.Len(t, streams, 1)
		// "pod" is constant, "extra" is only on line 1 (absent from line 2 → varying)
		assert.Equal(t, map[string]string{"pod": "pod-a"}, streams[0].StructuredMetadata)
		assert.Equal(t, map[string]string{"extra": "val"}, streams[0].Lines[0].StructuredMetadata)
		assert.Nil(t, streams[0].Lines[1].StructuredMetadata)
	})
}

func TestQueryLokiLogsFormatValidation(t *testing.T) {
	// Unknown format values must be rejected before any backend call, so a typo
	// can't silently fall back to full output. Datasource resolution happens
	// after validation, so an invalid format fails fast without a live backend.
	for _, bad := range []string{"flat", "summary", "comapct", "compact-ish"} {
		_, err := queryLokiLogs(context.Background(), QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{app="x"}`,
			Format:        bad,
		})
		require.Error(t, err, "format %q should be rejected", bad)
		assert.Contains(t, err.Error(), "invalid format")
	}
}
