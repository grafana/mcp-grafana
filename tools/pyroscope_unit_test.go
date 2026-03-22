package tools

import (
	"testing"
	"time"

	typesv1 "github.com/grafana/pyroscope/api/gen/proto/go/types/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSeriesResponse_Empty(t *testing.T) {
	result := buildSeriesResponse(nil, time.Now().Add(-time.Hour), time.Now(), 15)
	assert.Empty(t, result.Series)
}

func TestBuildSeriesResponse_SingleSeries(t *testing.T) {
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

	result := buildSeriesResponse(series, start, end, 30)

	require.Len(t, result.Series, 1)
	s := result.Series[0]
	assert.Equal(t, map[string]string{"service_name": "web"}, s.Labels)
	assert.Len(t, s.Points, 3)
	assert.InDelta(t, 10.0, s.Points[0][1], 0.01)
	assert.InDelta(t, 50.0, s.Points[1][1], 0.01)
	assert.InDelta(t, 20.0, s.Points[2][1], 0.01)

	assert.Equal(t, start.Format(time.RFC3339), result.TimeRange["from"])
	assert.Equal(t, end.Format(time.RFC3339), result.TimeRange["to"])
	assert.InDelta(t, 30.0, result.StepSecs, 0.01)
}

func TestBuildSeriesResponse_MultipleSeries(t *testing.T) {
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

	result := buildSeriesResponse(series, start, end, 60)

	require.Len(t, result.Series, 2)
	assert.Equal(t, "a", result.Series[0].Labels["pod"])
	assert.Len(t, result.Series[0].Points, 1)
	assert.Equal(t, "b", result.Series[1].Labels["pod"])
	assert.Len(t, result.Series[1].Points, 2)
	assert.InDelta(t, 300.0, result.Series[1].Points[1][1], 0.01)
}

func TestBuildSeriesResponse_ZeroPointsSkipped(t *testing.T) {
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

	result := buildSeriesResponse(series, start, end, 60)

	require.Len(t, result.Series, 1)
	assert.Equal(t, "has-data", result.Series[0].Labels["pod"])
}

func TestBuildSeriesResponse_AllZeroPointsReturnsEmpty(t *testing.T) {
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

	result := buildSeriesResponse(series, start, end, 60)
	assert.Empty(t, result.Series)
}

func TestQueryPyroscope_QueryTypeValidation(t *testing.T) {
	tests := []struct {
		name      string
		queryType string
		wantErr   string
	}{
		{name: "invalid rejected", queryType: "unknown", wantErr: `invalid query_type "unknown"`},
		{name: "typo rejected", queryType: "profle", wantErr: `invalid query_type "profle"`},
		{name: "number rejected", queryType: "123", wantErr: `invalid query_type "123"`},
		{name: "plural profiles rejected", queryType: "profiles", wantErr: `invalid query_type "profiles"`},
		{name: "singular metric rejected", queryType: "metric", wantErr: `invalid query_type "metric"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := queryPyroscope(t.Context(), QueryPyroscopeParams{
				DataSourceUID: "fake",
				ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
				QueryType:     tc.queryType,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
