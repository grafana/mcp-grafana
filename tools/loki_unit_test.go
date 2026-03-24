package tools

import (
	"context"
	"encoding/json"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
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
