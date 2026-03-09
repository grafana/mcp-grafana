//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsNativePrometheusType(t *testing.T) {
	tests := []struct {
		dsType string
		want   bool
	}{
		{"prometheus", true},
		{"Prometheus", true},
		{"PROMETHEUS", true},
		{"mimir", true},
		{"cortex", true},
		{"thanos", true},
		{"stackdriver", false},
		{"loki", false},
		{"cloudwatch", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.dsType, func(t *testing.T) {
			assert.Equal(t, tt.want, isNativePrometheusType(tt.dsType))
		})
	}
}

func TestSupportsPromQLViaBackend(t *testing.T) {
	tests := []struct {
		dsType string
		want   bool
	}{
		{"stackdriver", true},
		{"Stackdriver", true},
		{"STACKDRIVER", true},
		{"prometheus", false},
		{"loki", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.dsType, func(t *testing.T) {
			assert.Equal(t, tt.want, supportsPromQLViaBackend(tt.dsType))
		})
	}
}

func TestBackendPromClientBuildPayload(t *testing.T) {
	c := &backendPromClient{
		datasourceUID:  "test-uid",
		datasourceType: "stackdriver",
	}

	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)
	step := 15 * time.Second

	payload := c.buildDSQueryPayload("up{job='test'}", start, end, step)

	// Verify structure
	queries, ok := payload["queries"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, queries, 1)

	q := queries[0]
	assert.Equal(t, "up{job='test'}", q["expr"])
	assert.Equal(t, "A", q["refId"])
	assert.Equal(t, int64(15000), q["intervalMs"])
	assert.Equal(t, 1000, q["maxDataPoints"])

	ds, ok := q["datasource"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "test-uid", ds["uid"])
	assert.Equal(t, "stackdriver", ds["type"])

	assert.Equal(t, fmt.Sprintf("%d", start.UnixMilli()), payload["from"])
	assert.Equal(t, fmt.Sprintf("%d", end.UnixMilli()), payload["to"])
}

func TestFramesToMatrix(t *testing.T) {
	t.Run("single series", func(t *testing.T) {
		resp := &dsQueryResponse{
			Results: map[string]dsQueryResult{
				"A": {
					Frames: []dsQueryFrame{
						{
							Schema: dsQueryFrameSchema{
								Name: "up",
								Fields: []dsQueryFieldSchema{
									{Name: "Time", Type: "time"},
									{Name: "Value", Type: "number", Labels: map[string]string{"job": "test"}},
								},
							},
							Data: dsQueryFrameData{
								Values: [][]interface{}{
									{float64(1000), float64(2000), float64(3000)},
									{float64(1.0), float64(2.0), float64(3.0)},
								},
							},
						},
					},
				},
			},
		}

		matrix, err := framesToMatrix(resp)
		require.NoError(t, err)
		require.Len(t, matrix, 1)

		stream := matrix[0]
		assert.Equal(t, model.LabelValue("up"), stream.Metric[model.MetricNameLabel])
		assert.Equal(t, model.LabelValue("test"), stream.Metric["job"])
		require.Len(t, stream.Values, 3)
		assert.Equal(t, model.SampleValue(1.0), stream.Values[0].Value)
		assert.Equal(t, model.SampleValue(2.0), stream.Values[1].Value)
		assert.Equal(t, model.SampleValue(3.0), stream.Values[2].Value)
	})

	t.Run("multiple value columns", func(t *testing.T) {
		resp := &dsQueryResponse{
			Results: map[string]dsQueryResult{
				"A": {
					Frames: []dsQueryFrame{
						{
							Schema: dsQueryFrameSchema{
								Fields: []dsQueryFieldSchema{
									{Name: "Time", Type: "time"},
									{Name: "series_a", Type: "number", Labels: map[string]string{"instance": "a"}},
									{Name: "series_b", Type: "number", Labels: map[string]string{"instance": "b"}},
								},
							},
							Data: dsQueryFrameData{
								Values: [][]interface{}{
									{float64(1000), float64(2000)},
									{float64(10.0), float64(20.0)},
									{float64(100.0), float64(200.0)},
								},
							},
						},
					},
				},
			},
		}

		matrix, err := framesToMatrix(resp)
		require.NoError(t, err)
		require.Len(t, matrix, 2)

		assert.Equal(t, model.LabelValue("a"), matrix[0].Metric["instance"])
		assert.Equal(t, model.LabelValue("b"), matrix[1].Metric["instance"])
	})

	t.Run("empty frames", func(t *testing.T) {
		resp := &dsQueryResponse{
			Results: map[string]dsQueryResult{
				"A": {
					Frames: []dsQueryFrame{},
				},
			},
		}

		matrix, err := framesToMatrix(resp)
		require.NoError(t, err)
		assert.Len(t, matrix, 0)
	})

	t.Run("error in result", func(t *testing.T) {
		resp := &dsQueryResponse{
			Results: map[string]dsQueryResult{
				"A": {
					Error: "query failed: bad expression",
				},
			},
		}

		_, err := framesToMatrix(resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad expression")
	})

	t.Run("no time column", func(t *testing.T) {
		resp := &dsQueryResponse{
			Results: map[string]dsQueryResult{
				"A": {
					Frames: []dsQueryFrame{
						{
							Schema: dsQueryFrameSchema{
								Fields: []dsQueryFieldSchema{
									{Name: "Value", Type: "number"},
								},
							},
							Data: dsQueryFrameData{
								Values: [][]interface{}{
									{float64(1.0)},
								},
							},
						},
					},
				},
			},
		}

		matrix, err := framesToMatrix(resp)
		require.NoError(t, err)
		assert.Len(t, matrix, 0)
	})
}

func TestFramesToVector(t *testing.T) {
	resp := &dsQueryResponse{
		Results: map[string]dsQueryResult{
			"A": {
				Frames: []dsQueryFrame{
					{
						Schema: dsQueryFrameSchema{
							Name: "up",
							Fields: []dsQueryFieldSchema{
								{Name: "Time", Type: "time"},
								{Name: "Value", Type: "number", Labels: map[string]string{"job": "test"}},
							},
						},
						Data: dsQueryFrameData{
							Values: [][]interface{}{
								{float64(1000), float64(2000), float64(3000)},
								{float64(1.0), float64(2.0), float64(3.0)},
							},
						},
					},
				},
			},
		},
	}

	vector, err := framesToVector(resp)
	require.NoError(t, err)
	require.Len(t, vector, 1)

	// Should take last value
	assert.Equal(t, model.SampleValue(3.0), vector[0].Value)
	assert.Equal(t, model.Time(3000), vector[0].Timestamp)
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    float64
		wantOK  bool
	}{
		{"float64", float64(3.14), 3.14, true},
		{"int64", int64(42), 42.0, true},
		{"json.Number", json.Number("2.5"), 2.5, true},
		{"nil", nil, 0, false},
		{"string", "hello", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toFloat64(tt.input)
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.InDelta(t, tt.want, got, 0.001)
			}
		})
	}
}

func TestBackendPromClientMetadataErrors(t *testing.T) {
	c := &backendPromClient{
		datasourceType: "stackdriver",
	}

	ctx := context.Background()

	t.Run("LabelNames returns error", func(t *testing.T) {
		_, _, err := c.LabelNames(ctx, nil, time.Time{}, time.Time{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not supported for stackdriver")
	})

	t.Run("LabelValues returns error", func(t *testing.T) {
		_, _, err := c.LabelValues(ctx, "__name__", nil, time.Time{}, time.Time{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not supported for stackdriver")
	})

	t.Run("Metadata returns error", func(t *testing.T) {
		_, err := c.Metadata(ctx, "", "10")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not supported for stackdriver")
	})
}

func TestNormalizeDatasourceTypeStackdriver(t *testing.T) {
	assert.Equal(t, "prometheus", normalizeDatasourceType("stackdriver"))
	assert.Equal(t, "prometheus", normalizeDatasourceType("Stackdriver"))
}
