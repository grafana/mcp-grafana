package tools

import (
	"strings"
	"testing"
	"time"

	querierv1 "github.com/grafana/pyroscope/api/gen/proto/go/querier/v1"
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

func TestQueryPyroscope_FormatValidation(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		wantErr string
	}{
		{name: "invalid rejected", format: "unknown", wantErr: `invalid format "unknown"`},
		{name: "flamegraph rejected", format: "flamegraph", wantErr: `invalid format "flamegraph"`},
		{name: "json rejected", format: "json", wantErr: `invalid format "json"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := queryPyroscope(t.Context(), QueryPyroscopeParams{
				DataSourceUID: "fake",
				ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
				Format:        tc.format,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// testFlameGraph builds the flame graph for the call tree (values are quads
// of [xOffset, total, self, nameIndex] with delta-encoded offsets):
//
//	total (180)
//	+- main.entry (150) -> main.mid (150, self 50) -> main.mid (100, recursion) -> main.leaf (100)
//	+- other (30, server-side truncation bucket)
func testFlameGraph() *querierv1.FlameGraph {
	return &querierv1.FlameGraph{
		Names: []string{"total", "main.entry", "main.mid", "main.leaf", "other"},
		Total: 180,
		Levels: []*querierv1.Level{
			{Values: []int64{0, 180, 0, 0}},
			{Values: []int64{0, 150, 0, 1, 0, 30, 30, 4}},
			{Values: []int64{0, 150, 50, 2}},
			{Values: []int64{50, 100, 0, 2}},
			{Values: []int64{50, 100, 100, 3}},
		},
	}
}

func TestBuildProfileTable(t *testing.T) {
	table, err := buildProfileTable(testFlameGraph(), "nanoseconds", 100)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	require.Len(t, lines, 7, table)

	assert.Equal(t, "Total: 180ns", lines[0])
	assert.Equal(t, "Showing top 3 out of 3 functions by flat (self) value, accounting for 83.33% of the total", lines[1])
	assert.Contains(t, lines[2], `16.67% of the total was collapsed into an "other" bucket`)
	assert.Contains(t, lines[3], "flat%")
	assert.Contains(t, lines[3], "cum%")

	// Ranked by flat; recursion counted once in cum; entry gets cum only.
	assert.Regexp(t, `^\s+100ns\s+55.56%\s+55.56%\s+100ns\s+55.56%\s+main.leaf$`, lines[4])
	assert.Regexp(t, `^\s+50ns\s+27.78%\s+83.33%\s+150ns\s+83.33%\s+main.mid$`, lines[5])
	assert.Regexp(t, `^\s+0\S*\s+0.00%\s+83.33%\s+150ns\s+83.33%\s+main.entry$`, lines[6])

	assert.NotContains(t, table, `  other`, "truncation bucket must not appear as a function row")
	assert.NotContains(t, table, `  total`, "synthetic root must not appear as a function row")
}

func TestBuildProfileTable_MaxRows(t *testing.T) {
	table, err := buildProfileTable(testFlameGraph(), "nanoseconds", 1)
	require.NoError(t, err)

	assert.Contains(t, table, "Showing top 1 out of 3 functions by flat (self) value, accounting for 55.56% of the total")
	assert.Contains(t, table, "main.leaf")
	assert.NotContains(t, table, "main.mid")
}

func TestBuildProfileTable_Empty(t *testing.T) {
	_, err := buildProfileTable(&querierv1.FlameGraph{Names: []string{"total"}, Levels: []*querierv1.Level{{Values: []int64{0, 0, 0, 0}}}}, "nanoseconds", 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty profile")

	_, err = buildProfileTable(nil, "nanoseconds", 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty profile")
}

func TestSampleUnitFromProfileType(t *testing.T) {
	assert.Equal(t, "nanoseconds", sampleUnitFromProfileType("process_cpu:cpu:nanoseconds:cpu:nanoseconds"))
	assert.Equal(t, "bytes", sampleUnitFromProfileType("memory:alloc_space:bytes:space:bytes"))
	assert.Equal(t, "", sampleUnitFromProfileType("garbage"))
}

func TestFormatSampleValue(t *testing.T) {
	assert.Equal(t, "615.26s", formatSampleValue(615_260_000_000, "nanoseconds"))
	assert.Equal(t, "1.50ms", formatSampleValue(1_500_000, "nanoseconds"))
	assert.Equal(t, "2.00us", formatSampleValue(2_000, "nanoseconds"))
	assert.Equal(t, "999ns", formatSampleValue(999, "nanoseconds"))
	assert.Equal(t, "1.42GB", formatSampleValue(1_525_000_000, "bytes"))
	assert.Equal(t, "1.45MB", formatSampleValue(1_525_000, "bytes"))
	assert.Equal(t, "1.49kB", formatSampleValue(1_525, "bytes"))
	assert.Equal(t, "42B", formatSampleValue(42, "bytes"))
	assert.Equal(t, "12345", formatSampleValue(12345, "count"))
}
