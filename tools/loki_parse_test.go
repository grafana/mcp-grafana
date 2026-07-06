//go:build unit

package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLokiQueryResponse_StreamsTimestampNotDoubleEncoded(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [
				{
					"stream": {"app": "myapp"},
					"values": [
						["1234567890123456789", "{\"level\":\"INFO\",\"message\":\"hello\"}"]
					]
				}
			]
		}
	}`

	var resp lokiQueryResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))

	entries, err := parseLokiQueryResponse(&resp)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, "1234567890123456789", entry.Timestamp)
	assert.Equal(t, `{"level":"INFO","message":"hello"}`, entry.Line)

	// The whole entry must itself be valid, singly-encoded JSON: marshaling
	// it and unmarshaling into a generic map should round-trip cleanly with
	// no leftover escaped quotes in the timestamp.
	out, err := json.Marshal(entry)
	require.NoError(t, err)

	var generic map[string]any
	require.NoError(t, json.Unmarshal(out, &generic))
	assert.Equal(t, "1234567890123456789", generic["timestamp"])
}
