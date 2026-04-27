//go:build unit

package tools

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveInfluxDBDialect(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		jsonData  map[string]interface{}
		want      string
		wantErr   bool
	}{
		{
			name:      "explicit influxql",
			requested: "influxql",
			want:      InfluxDBDialectInfluxQL,
		},
		{
			name:      "explicit flux",
			requested: "flux",
			want:      InfluxDBDialectFlux,
		},
		{
			name:      "explicit case-insensitive",
			requested: "Flux",
			want:      InfluxDBDialectFlux,
		},
		{
			name:      "reject unknown dialect",
			requested: "sql",
			wantErr:   true,
		},
		{
			name:      "reject garbage",
			requested: "not-a-dialect",
			wantErr:   true,
		},
		{
			name:     "infer flux from jsonData.version=Flux",
			jsonData: map[string]interface{}{"version": "Flux"},
			want:     InfluxDBDialectFlux,
		},
		{
			name:     "infer influxql from jsonData.version=InfluxQL",
			jsonData: map[string]interface{}{"version": "InfluxQL"},
			want:     InfluxDBDialectInfluxQL,
		},
		{
			name:     "fallback to influxql when jsonData missing version",
			jsonData: map[string]interface{}{},
			want:     InfluxDBDialectInfluxQL,
		},
		{
			name:     "fallback to influxql when jsonData is nil",
			jsonData: nil,
			want:     InfluxDBDialectInfluxQL,
		},
		{
			name:     "fallback to influxql when version is SQL (v3 not yet supported)",
			jsonData: map[string]interface{}{"version": "SQL"},
			want:     InfluxDBDialectInfluxQL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveInfluxDBDialect(tt.requested, tt.jsonData)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildInfluxDBPayload(t *testing.T) {
	from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	payload := buildInfluxDBPayload("my-uid", InfluxDBDialectFlux, `from(bucket: "b") |> range(start: -1h)`, from, to, 0)

	// Top-level time bounds are Unix ms strings, matching /api/ds/query.
	assert.Equal(t, "1705312800000", payload["from"])
	assert.Equal(t, "1705316400000", payload["to"])

	queries, ok := payload["queries"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, queries, 1)

	q := queries[0]
	assert.Equal(t, "A", q["refId"])
	assert.Equal(t, `from(bucket: "b") |> range(start: -1h)`, q["query"])
	assert.Equal(t, true, q["rawQuery"])
	assert.Equal(t, InfluxDBDialectFlux, q["queryType"])
	assert.Equal(t, DefaultInfluxDBMaxDataPoints, q["maxDataPoints"])

	ds, ok := q["datasource"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "my-uid", ds["uid"])
	assert.Equal(t, InfluxDBDatasourceType, ds["type"])
}

func TestBuildInfluxDBPayload_MaxDataPointsOverride(t *testing.T) {
	now := time.Now()
	payload := buildInfluxDBPayload("uid", InfluxDBDialectInfluxQL, "SELECT 1", now, now, 42)
	q := payload["queries"].([]map[string]interface{})[0]
	assert.Equal(t, 42, q["maxDataPoints"])
}

func TestBuildInfluxDBPayload_SerializesToValidJSON(t *testing.T) {
	// If the payload map contains anything that json.Marshal can't encode,
	// we'd find out at request time rather than in a test. Catch it here.
	now := time.Now()
	payload := buildInfluxDBPayload("uid", InfluxDBDialectFlux, "q", now, now, 0)
	_, err := json.Marshal(payload)
	require.NoError(t, err)
}

func TestFramesToRows(t *testing.T) {
	frame := `{
		"schema": {
			"name": "A",
			"refId": "A",
			"fields": [
				{"name": "Time", "type": "time"},
				{"name": "value", "type": "number"}
			]
		},
		"data": {
			"values": [
				[1705312800000, 1705312860000, 1705312920000],
				[1.0, 2.5, 3.7]
			]
		}
	}`

	cols, rows, err := framesToRows([]json.RawMessage{json.RawMessage(frame)})
	require.NoError(t, err)

	assert.Equal(t, []string{"Time", "value"}, cols)
	require.Len(t, rows, 3)
	assert.Equal(t, float64(1705312800000), rows[0]["Time"])
	assert.Equal(t, 1.0, rows[0]["value"])
	assert.Equal(t, 3.7, rows[2]["value"])
}

func TestFramesToRows_EmptyFrame(t *testing.T) {
	frame := `{"schema": {"fields": [{"name": "Time"}]}, "data": {"values": []}}`
	cols, rows, err := framesToRows([]json.RawMessage{json.RawMessage(frame)})
	require.NoError(t, err)
	assert.Equal(t, []string{"Time"}, cols)
	assert.Empty(t, rows)
}

func TestFramesToRows_InvalidJSON(t *testing.T) {
	_, _, err := framesToRows([]json.RawMessage{json.RawMessage(`{not json`)})
	require.Error(t, err)
}
