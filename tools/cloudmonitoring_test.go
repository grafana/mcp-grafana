package tools

import (
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractDefaultProject(t *testing.T) {
	t.Run("valid jsonData with defaultProject", func(t *testing.T) {
		ds := &models.DataSource{
			UID: "test-uid",
			JSONData: map[string]interface{}{
				"defaultProject": "my-gcp-project",
			},
		}
		proj, err := extractDefaultProject(ds)
		require.NoError(t, err)
		assert.Equal(t, "my-gcp-project", proj)
	})

	t.Run("nil jsonData", func(t *testing.T) {
		ds := &models.DataSource{
			UID:      "test-uid",
			JSONData: nil,
		}
		_, err := extractDefaultProject(ds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no jsonData configured")
	})

	t.Run("missing defaultProject", func(t *testing.T) {
		ds := &models.DataSource{
			UID: "test-uid",
			JSONData: map[string]interface{}{
				"authenticationType": "jwt",
			},
		}
		_, err := extractDefaultProject(ds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no defaultProject configured")
	})

	t.Run("empty defaultProject", func(t *testing.T) {
		ds := &models.DataSource{
			UID: "test-uid",
			JSONData: map[string]interface{}{
				"defaultProject": "",
			},
		}
		_, err := extractDefaultProject(ds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no defaultProject configured")
	})
}

func TestFramesToMatrix(t *testing.T) {
	t.Run("single series", func(t *testing.T) {
		frames := []cloudMonitoringFrame{
			{
				Schema: cloudMonitoringFrameSchema{
					Name: "up",
					Fields: []cloudMonitoringFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number", Labels: map[string]string{"job": "prometheus"}},
					},
				},
				Data: cloudMonitoringFrameData{
					Values: [][]interface{}{
						{float64(1000), float64(2000), float64(3000)},
						{float64(1.0), float64(2.0), float64(3.0)},
					},
				},
			},
		}

		result, err := framesToMatrix(frames)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, model.LabelValue("prometheus"), result[0].Metric["job"])
		assert.Equal(t, model.LabelValue("up"), result[0].Metric[model.MetricNameLabel])
		require.Len(t, result[0].Values, 3)
		assert.Equal(t, model.SampleValue(1.0), result[0].Values[0].Value)
		assert.Equal(t, model.Time(1000), result[0].Values[0].Timestamp)
	})

	t.Run("multiple series", func(t *testing.T) {
		frames := []cloudMonitoringFrame{
			{
				Schema: cloudMonitoringFrameSchema{
					Fields: []cloudMonitoringFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number", Labels: map[string]string{"instance": "a"}},
					},
				},
				Data: cloudMonitoringFrameData{
					Values: [][]interface{}{
						{float64(1000)},
						{float64(10.0)},
					},
				},
			},
			{
				Schema: cloudMonitoringFrameSchema{
					Fields: []cloudMonitoringFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number", Labels: map[string]string{"instance": "b"}},
					},
				},
				Data: cloudMonitoringFrameData{
					Values: [][]interface{}{
						{float64(1000)},
						{float64(20.0)},
					},
				},
			},
		}

		result, err := framesToMatrix(frames)
		require.NoError(t, err)
		require.Len(t, result, 2)
		assert.Equal(t, model.LabelValue("a"), result[0].Metric["instance"])
		assert.Equal(t, model.LabelValue("b"), result[1].Metric["instance"])
	})

	t.Run("frame without time column is skipped", func(t *testing.T) {
		frames := []cloudMonitoringFrame{
			{
				Schema: cloudMonitoringFrameSchema{
					Fields: []cloudMonitoringFrameField{
						{Name: "Value", Type: "number"},
					},
				},
				Data: cloudMonitoringFrameData{
					Values: [][]interface{}{
						{float64(1.0)},
					},
				},
			},
		}

		result, err := framesToMatrix(frames)
		require.NoError(t, err)
		assert.Len(t, result, 0)
	})

	t.Run("empty frames", func(t *testing.T) {
		result, err := framesToMatrix([]cloudMonitoringFrame{})
		require.NoError(t, err)
		assert.Len(t, result, 0)
	})
}

func TestFramesToVector(t *testing.T) {
	t.Run("single sample", func(t *testing.T) {
		frames := []cloudMonitoringFrame{
			{
				Schema: cloudMonitoringFrameSchema{
					Name: "up",
					Fields: []cloudMonitoringFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number", Labels: map[string]string{"job": "test"}},
					},
				},
				Data: cloudMonitoringFrameData{
					Values: [][]interface{}{
						{float64(5000)},
						{float64(42.0)},
					},
				},
			},
		}

		result, err := framesToVector(frames)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, model.SampleValue(42.0), result[0].Value)
		assert.Equal(t, model.Time(5000), result[0].Timestamp)
		assert.Equal(t, model.LabelValue("test"), result[0].Metric["job"])
		assert.Equal(t, model.LabelValue("up"), result[0].Metric[model.MetricNameLabel])
	})

	t.Run("uses last data point from range", func(t *testing.T) {
		frames := []cloudMonitoringFrame{
			{
				Schema: cloudMonitoringFrameSchema{
					Fields: []cloudMonitoringFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number"},
					},
				},
				Data: cloudMonitoringFrameData{
					Values: [][]interface{}{
						{float64(1000), float64(2000), float64(3000)},
						{float64(1.0), float64(2.0), float64(3.0)},
					},
				},
			},
		}

		result, err := framesToVector(frames)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, model.SampleValue(3.0), result[0].Value)
		assert.Equal(t, model.Time(3000), result[0].Timestamp)
	})

	t.Run("empty data is skipped", func(t *testing.T) {
		frames := []cloudMonitoringFrame{
			{
				Schema: cloudMonitoringFrameSchema{
					Fields: []cloudMonitoringFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number"},
					},
				},
				Data: cloudMonitoringFrameData{
					Values: [][]interface{}{
						{},
						{},
					},
				},
			},
		}

		result, err := framesToVector(frames)
		require.NoError(t, err)
		assert.Len(t, result, 0)
	})
}

func TestFramesToPrometheusValue(t *testing.T) {
	t.Run("range query returns matrix", func(t *testing.T) {
		resp := &cloudMonitoringQueryResponse{
			Results: map[string]struct {
				Status int                    `json:"status,omitempty"`
				Frames []cloudMonitoringFrame `json:"frames,omitempty"`
				Error  string                 `json:"error,omitempty"`
			}{
				"A": {
					Frames: []cloudMonitoringFrame{
						{
							Schema: cloudMonitoringFrameSchema{
								Fields: []cloudMonitoringFrameField{
									{Name: "Time", Type: "time"},
									{Name: "Value", Type: "number"},
								},
							},
							Data: cloudMonitoringFrameData{
								Values: [][]interface{}{
									{float64(1000)},
									{float64(1.0)},
								},
							},
						},
					},
				},
			},
		}

		result, err := framesToPrometheusValue(resp, "range")
		require.NoError(t, err)
		matrix, ok := result.(model.Matrix)
		require.True(t, ok)
		assert.Len(t, matrix, 1)
	})

	t.Run("instant query returns vector", func(t *testing.T) {
		resp := &cloudMonitoringQueryResponse{
			Results: map[string]struct {
				Status int                    `json:"status,omitempty"`
				Frames []cloudMonitoringFrame `json:"frames,omitempty"`
				Error  string                 `json:"error,omitempty"`
			}{
				"A": {
					Frames: []cloudMonitoringFrame{
						{
							Schema: cloudMonitoringFrameSchema{
								Fields: []cloudMonitoringFrameField{
									{Name: "Time", Type: "time"},
									{Name: "Value", Type: "number"},
								},
							},
							Data: cloudMonitoringFrameData{
								Values: [][]interface{}{
									{float64(1000)},
									{float64(1.0)},
								},
							},
						},
					},
				},
			},
		}

		result, err := framesToPrometheusValue(resp, "instant")
		require.NoError(t, err)
		vector, ok := result.(model.Vector)
		require.True(t, ok)
		assert.Len(t, vector, 1)
	})

	t.Run("error in response", func(t *testing.T) {
		resp := &cloudMonitoringQueryResponse{
			Results: map[string]struct {
				Status int                    `json:"status,omitempty"`
				Frames []cloudMonitoringFrame `json:"frames,omitempty"`
				Error  string                 `json:"error,omitempty"`
			}{
				"A": {
					Error: "something went wrong",
				},
			},
		}

		_, err := framesToPrometheusValue(resp, "range")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "something went wrong")
	})

	t.Run("empty response returns empty matrix for range", func(t *testing.T) {
		resp := &cloudMonitoringQueryResponse{
			Results: map[string]struct {
				Status int                    `json:"status,omitempty"`
				Frames []cloudMonitoringFrame `json:"frames,omitempty"`
				Error  string                 `json:"error,omitempty"`
			}{},
		}

		result, err := framesToPrometheusValue(resp, "range")
		require.NoError(t, err)
		matrix, ok := result.(model.Matrix)
		require.True(t, ok)
		assert.Len(t, matrix, 0)
	})

	t.Run("empty response returns empty vector for instant", func(t *testing.T) {
		resp := &cloudMonitoringQueryResponse{
			Results: map[string]struct {
				Status int                    `json:"status,omitempty"`
				Frames []cloudMonitoringFrame `json:"frames,omitempty"`
				Error  string                 `json:"error,omitempty"`
			}{},
		}

		result, err := framesToPrometheusValue(resp, "instant")
		require.NoError(t, err)
		vector, ok := result.(model.Vector)
		require.True(t, ok)
		assert.Len(t, vector, 0)
	})
}

func TestToMilliseconds(t *testing.T) {
	t.Run("float64", func(t *testing.T) {
		v, ok := toMilliseconds(float64(1234567890))
		assert.True(t, ok)
		assert.Equal(t, int64(1234567890), v)
	})

	t.Run("int64", func(t *testing.T) {
		v, ok := toMilliseconds(int64(1234567890))
		assert.True(t, ok)
		assert.Equal(t, int64(1234567890), v)
	})

	t.Run("nil", func(t *testing.T) {
		_, ok := toMilliseconds(nil)
		assert.False(t, ok)
	})

	t.Run("string", func(t *testing.T) {
		_, ok := toMilliseconds("not a number")
		assert.False(t, ok)
	})
}

func TestToFloat64(t *testing.T) {
	t.Run("float64", func(t *testing.T) {
		v, ok := toFloat64(float64(3.14))
		assert.True(t, ok)
		assert.Equal(t, 3.14, v)
	})

	t.Run("int64", func(t *testing.T) {
		v, ok := toFloat64(int64(42))
		assert.True(t, ok)
		assert.Equal(t, float64(42), v)
	})

	t.Run("nil", func(t *testing.T) {
		_, ok := toFloat64(nil)
		assert.False(t, ok)
	})

	t.Run("string", func(t *testing.T) {
		_, ok := toFloat64("not a number")
		assert.False(t, ok)
	})
}
