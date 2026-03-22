package tools

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	typesv1 "github.com/grafana/pyroscope/api/gen/proto/go/types/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatSeriesResponse_Empty(t *testing.T) {
	result := formatSeriesResponse(nil, time.Now().Add(-time.Hour), time.Now(), 15)
	assert.Equal(t, "No series data returned for the given query and time range.", result)
}

func TestFormatSeriesResponse_SingleSeries(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)

	series := []*typesv1.Series{
		{
			Labels: []*typesv1.LabelPair{
				{Name: "service_name", Value: "web"},
			},
			Points: []*typesv1.Point{
				{Timestamp: start.UnixMilli(), Value: 10.0},
				{Timestamp: start.Add(30 * time.Second).UnixMilli(), Value: 50.0},
				{Timestamp: start.Add(60 * time.Second).UnixMilli(), Value: 20.0},
			},
		},
	}

	result := formatSeriesResponse(series, start, end, 30)

	var parsed struct {
		Series    []rawSeries       `json:"series"`
		TimeRange map[string]string `json:"time_range"`
		StepSecs  float64           `json:"step_seconds"`
	}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))

	require.Len(t, parsed.Series, 1)
	s := parsed.Series[0]
	assert.Equal(t, map[string]string{"service_name": "web"}, s.Labels)
	assert.Len(t, s.Points, 3)
	assert.InDelta(t, 10.0, s.Points[0][1], 0.01)
	assert.InDelta(t, 50.0, s.Points[1][1], 0.01)
	assert.InDelta(t, 20.0, s.Points[2][1], 0.01)

	assert.Equal(t, start.Format(time.RFC3339), parsed.TimeRange["from"])
	assert.Equal(t, end.Format(time.RFC3339), parsed.TimeRange["to"])
	assert.InDelta(t, 30.0, parsed.StepSecs, 0.01)
}

func TestFormatSeriesResponse_MultipleSeries(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	series := []*typesv1.Series{
		{
			Labels: []*typesv1.LabelPair{{Name: "pod", Value: "a"}},
			Points: []*typesv1.Point{
				{Timestamp: start.UnixMilli(), Value: 100},
			},
		},
		{
			Labels: []*typesv1.LabelPair{{Name: "pod", Value: "b"}},
			Points: []*typesv1.Point{
				{Timestamp: start.UnixMilli(), Value: 200},
				{Timestamp: start.Add(time.Minute).UnixMilli(), Value: 300},
			},
		},
	}

	result := formatSeriesResponse(series, start, end, 60)

	var parsed struct {
		Series []rawSeries `json:"series"`
	}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))

	require.Len(t, parsed.Series, 2)
	assert.Equal(t, "a", parsed.Series[0].Labels["pod"])
	assert.Len(t, parsed.Series[0].Points, 1)
	assert.Equal(t, "b", parsed.Series[1].Labels["pod"])
	assert.Len(t, parsed.Series[1].Points, 2)
	assert.InDelta(t, 300.0, parsed.Series[1].Points[1][1], 0.01)
}

func TestFormatSeriesResponse_ZeroPointsSkipped(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	series := []*typesv1.Series{
		{
			Labels: []*typesv1.LabelPair{{Name: "pod", Value: "empty"}},
			Points: []*typesv1.Point{}, // no data points
		},
		{
			Labels: []*typesv1.LabelPair{{Name: "pod", Value: "has-data"}},
			Points: []*typesv1.Point{
				{Timestamp: start.UnixMilli(), Value: 42},
			},
		},
	}

	result := formatSeriesResponse(series, start, end, 60)

	var parsed struct {
		Series []rawSeries `json:"series"`
	}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))

	require.Len(t, parsed.Series, 1)
	assert.Equal(t, "has-data", parsed.Series[0].Labels["pod"])
}

func TestFormatSeriesResponse_AllZeroPointsReturnsMessage(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	series := []*typesv1.Series{
		{
			Labels: []*typesv1.LabelPair{{Name: "pod", Value: "a"}},
			Points: []*typesv1.Point{},
		},
		{
			Labels: []*typesv1.LabelPair{{Name: "pod", Value: "b"}},
			Points: []*typesv1.Point{},
		},
	}

	result := formatSeriesResponse(series, start, end, 60)
	assert.Equal(t, "No series data returned for the given query and time range.", result)
}

func TestFetchPyroscopeUnified_QueryTypeValidation(t *testing.T) {
	tests := []struct {
		name      string
		queryType string
		wantErr   string
	}{
		{name: "invalid rejected", queryType: "unknown", wantErr: `invalid query_type "unknown"`},
		{name: "typo rejected", queryType: "profle", wantErr: `invalid query_type "profle"`},
		{name: "number rejected", queryType: "123", wantErr: `invalid query_type "123"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fetchPyroscopeUnified(t.Context(), FetchPyroscopeUnifiedParams{
				DataSourceUID: "fake",
				ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
				QueryType:     tc.queryType,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestAutoStepCalculation(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		wantStep float64
	}{
		{
			name:     "1 hour range gives 72s step (3600/50)",
			duration: time.Hour,
			wantStep: 72,
		},
		{
			name:     "short range clamps to 15s minimum",
			duration: 5 * time.Minute,
			wantStep: 15,
		},
		{
			name:     "24 hour range gives 1728s step",
			duration: 24 * time.Hour,
			wantStep: 1728,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			durationSec := tc.duration.Seconds()
			step := math.Max(durationSec/50.0, 15.0)
			assert.InDelta(t, tc.wantStep, step, 0.01)
		})
	}
}
