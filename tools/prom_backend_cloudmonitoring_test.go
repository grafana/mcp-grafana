//go:build unit

package tools

import (
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFramesToMatrix(t *testing.T) {
	t.Run("single series", func(t *testing.T) {
		frames := []dsQueryFrame{
			{
				Schema: dsQueryFrameSchema{
					Name: "cpu_usage",
					Fields: []dsQueryFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number", Labels: map[string]string{"host": "a"}},
					},
				},
				Data: dsQueryFrameData{
					Values: [][]interface{}{
						{float64(1000), float64(2000), float64(3000)},
						{float64(0.5), float64(0.7), float64(0.9)},
					},
				},
			},
		}

		result, err := framesToMatrix(frames)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, model.LabelValue("cpu_usage"), result[0].Metric["__name__"])
		assert.Equal(t, model.LabelValue("a"), result[0].Metric["host"])
		assert.Len(t, result[0].Values, 3)
		assert.Equal(t, model.SampleValue(0.5), result[0].Values[0].Value)
		assert.Equal(t, model.Time(1000), result[0].Values[0].Timestamp)
	})

	t.Run("multiple series", func(t *testing.T) {
		frames := []dsQueryFrame{
			{
				Schema: dsQueryFrameSchema{
					Name:   "cpu",
					Fields: []dsQueryFrameField{{Name: "Time", Type: "time"}, {Name: "Value", Type: "number", Labels: map[string]string{"host": "a"}}},
				},
				Data: dsQueryFrameData{Values: [][]interface{}{{float64(1000)}, {float64(0.5)}}},
			},
			{
				Schema: dsQueryFrameSchema{
					Name:   "cpu",
					Fields: []dsQueryFrameField{{Name: "Time", Type: "time"}, {Name: "Value", Type: "number", Labels: map[string]string{"host": "b"}}},
				},
				Data: dsQueryFrameData{Values: [][]interface{}{{float64(1000)}, {float64(0.8)}}},
			},
		}

		result, err := framesToMatrix(frames)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("empty frames", func(t *testing.T) {
		result, err := framesToMatrix(nil)
		require.NoError(t, err)
		assert.Len(t, result, 0)
	})

	t.Run("frame missing time field", func(t *testing.T) {
		frames := []dsQueryFrame{
			{
				Schema: dsQueryFrameSchema{
					Fields: []dsQueryFrameField{{Name: "Value", Type: "number"}},
				},
				Data: dsQueryFrameData{Values: [][]interface{}{{float64(1.0)}}},
			},
		}

		result, err := framesToMatrix(frames)
		require.NoError(t, err)
		assert.Len(t, result, 0)
	})
}

func TestFramesToVector(t *testing.T) {
	t.Run("single sample", func(t *testing.T) {
		frames := []dsQueryFrame{
			{
				Schema: dsQueryFrameSchema{
					Name: "up",
					Fields: []dsQueryFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number", Labels: map[string]string{"job": "prometheus"}},
					},
				},
				Data: dsQueryFrameData{
					Values: [][]interface{}{
						{float64(5000)},
						{float64(1.0)},
					},
				},
			},
		}

		result, err := framesToVector(frames)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, model.SampleValue(1.0), result[0].Value)
		assert.Equal(t, model.Time(5000), result[0].Timestamp)
		assert.Equal(t, model.LabelValue("up"), result[0].Metric["__name__"])
		assert.Equal(t, model.LabelValue("prometheus"), result[0].Metric["job"])
	})

	t.Run("takes last value from multi-point frame", func(t *testing.T) {
		frames := []dsQueryFrame{
			{
				Schema: dsQueryFrameSchema{
					Fields: []dsQueryFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number"},
					},
				},
				Data: dsQueryFrameData{
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

	t.Run("empty frames", func(t *testing.T) {
		result, err := framesToVector(nil)
		require.NoError(t, err)
		assert.Len(t, result, 0)
	})
}

func TestFramesToPrometheusValue(t *testing.T) {
	t.Run("missing refId returns empty", func(t *testing.T) {
		resp := &dsQueryResponse{Results: map[string]dsQueryResult{}}
		v, err := framesToPrometheusValue(resp, "range")
		require.NoError(t, err)
		assert.Equal(t, model.Matrix{}, v)

		v, err = framesToPrometheusValue(resp, "instant")
		require.NoError(t, err)
		assert.Equal(t, model.Vector{}, v)
	})

	t.Run("error in result", func(t *testing.T) {
		resp := &dsQueryResponse{Results: map[string]dsQueryResult{
			"A": {Error: "something went wrong"},
		}}
		_, err := framesToPrometheusValue(resp, "range")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "something went wrong")
	})
}

func TestToMillis(t *testing.T) {
	v, ok := toMillis(float64(1234))
	assert.True(t, ok)
	assert.Equal(t, int64(1234), v)

	_, ok = toMillis("not a number")
	assert.False(t, ok)
}

func TestToFloat(t *testing.T) {
	v, ok := toFloat(float64(3.14))
	assert.True(t, ok)
	assert.InDelta(t, 3.14, v, 0.001)

	_, ok = toFloat(nil)
	assert.False(t, ok)

	_, ok = toFloat("not a number")
	assert.False(t, ok)
}

func TestExtractNameMatcher(t *testing.T) {
	tests := []struct {
		name     string
		matchers []string
		want     string
	}{
		{"empty", nil, ""},
		{"no name matcher", []string{`{job="prometheus"}`}, ""},
		{"exact match", []string{`{__name__="up"}`}, "up"},
		{"unquoted", []string{`{__name__=up}`}, "up"},
		{"with other matchers", []string{`{__name__="cpu_total", job="node"}`}, "cpu_total"},
		{"regex matcher skipped", []string{`{__name__=~"cpu.*"}`}, ""},
		{"negative matcher skipped", []string{`{__name__!="up"}`}, ""},
		{"negative regex skipped", []string{`{__name__!~"cpu.*"}`}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, extractNameMatcher(tc.matchers))
		})
	}
}

func TestMatchesSimpleFilter(t *testing.T) {
	assert.True(t, matchesSimpleFilter("foo", ""))
	assert.True(t, matchesSimpleFilter("foo", "foo"))
	assert.False(t, matchesSimpleFilter("foo", "bar"))
	assert.True(t, matchesSimpleFilter("foobar", "foo*"))
	assert.False(t, matchesSimpleFilter("bazbar", "foo*"))
	assert.True(t, matchesSimpleFilter("bazfoo", "*foo"))
	assert.False(t, matchesSimpleFilter("foobar", "*foo"))
	assert.True(t, matchesSimpleFilter("bazfooqux", "*foo*"))
	assert.False(t, matchesSimpleFilter("bazbar", "*foo*"))
}

func TestMapGCPMetricKind(t *testing.T) {
	assert.Equal(t, promv1.MetricTypeHistogram, mapGCPMetricKind("CUMULATIVE", "DISTRIBUTION"))
	assert.Equal(t, promv1.MetricTypeCounter, mapGCPMetricKind("CUMULATIVE", "INT64"))
	assert.Equal(t, promv1.MetricTypeGauge, mapGCPMetricKind("GAUGE", "DOUBLE"))
	assert.Equal(t, promv1.MetricTypeUnknown, mapGCPMetricKind("DELTA", "DOUBLE"))
}

func TestExtractLabelValuesFromFrames(t *testing.T) {
	resp := &dsQueryResponse{
		Results: map[string]dsQueryResult{
			"A": {
				Frames: []dsQueryFrame{
					{
						Schema: dsQueryFrameSchema{
							Fields: []dsQueryFrameField{
								{Name: "Time", Type: "time"},
								{Name: "Value", Type: "number", Labels: map[string]string{"zone": "us-east1-b", "project_id": "my-project"}},
							},
						},
					},
					{
						Schema: dsQueryFrameSchema{
							Fields: []dsQueryFrameField{
								{Name: "Time", Type: "time"},
								{Name: "Value", Type: "number", Labels: map[string]string{"zone": "us-west1-a", "project_id": "my-project"}},
							},
						},
					},
					{
						Schema: dsQueryFrameSchema{
							Fields: []dsQueryFrameField{
								{Name: "Time", Type: "time"},
								{Name: "Value", Type: "number", Labels: map[string]string{"zone": "us-east1-b", "project_id": "other-project"}},
							},
						},
					},
				},
			},
		},
	}

	zones := extractLabelValuesFromFrames(resp, "zone")
	assert.Len(t, zones, 2)
	assert.ElementsMatch(t, []string{"us-east1-b", "us-west1-a"}, zones)

	projects := extractLabelValuesFromFrames(resp, "project_id")
	assert.Len(t, projects, 2)
	assert.ElementsMatch(t, []string{"my-project", "other-project"}, projects)

	missing := extractLabelValuesFromFrames(resp, "nonexistent")
	assert.Empty(t, missing)
}

func TestExtractCMDefaultProject(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		ds := &models.DataSource{
			UID:      "test",
			JSONData: map[string]interface{}{"defaultProject": "my-project"},
		}
		proj := extractCMDefaultProject(ds)
		assert.Equal(t, "my-project", proj)
	})

	t.Run("nil jsonData", func(t *testing.T) {
		ds := &models.DataSource{UID: "test"}
		proj := extractCMDefaultProject(ds)
		assert.Equal(t, "", proj)
	})

	t.Run("missing project", func(t *testing.T) {
		ds := &models.DataSource{
			UID:      "test",
			JSONData: map[string]interface{}{"somethingElse": "value"},
		}
		proj := extractCMDefaultProject(ds)
		assert.Equal(t, "", proj)
	})
}
