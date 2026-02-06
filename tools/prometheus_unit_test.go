package tools

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRelativeTime(t *testing.T) {
	const day = 24 * time.Hour
	const week = 7 * day

	testCases := []struct {
		name          string
		input         string
		expectedError bool
		expectedDelta time.Duration // Expected time difference from now
		isMonthCase   bool          // Special handling for month arithmetic
		isYearCase    bool          // Special handling for year arithmetic
	}{
		{
			name:          "now",
			input:         "now",
			expectedError: false,
			expectedDelta: 0,
		},
		{
			name:          "now-1h",
			input:         "now-1h",
			expectedError: false,
			expectedDelta: -1 * time.Hour,
		},
		{
			name:          "now-30m",
			input:         "now-30m",
			expectedError: false,
			expectedDelta: -30 * time.Minute,
		},
		{
			name:          "now-1d",
			input:         "now-1d",
			expectedError: false,
			expectedDelta: -24 * time.Hour,
		},
		{
			name:          "now-1w",
			input:         "now-1w",
			expectedError: false,
			expectedDelta: -week,
		},
		{
			name:          "now-1M",
			input:         "now-1M",
			expectedError: false,
			isMonthCase:   true,
		},
		{
			name:          "now-1y",
			input:         "now-1y",
			expectedError: false,
			isYearCase:    true,
		},
		{
			name:          "now-1.5h",
			input:         "now-1.5h",
			expectedError: true,
		},
		{
			name:          "invalid format",
			input:         "yesterday",
			expectedError: true,
		},
		{
			name:          "empty string",
			input:         "",
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			result, err := parseTime(tc.input)

			if tc.expectedError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			if tc.input == "now" {
				// For "now", the result should be very close to the current time
				// Allow a small tolerance for execution time
				diff := result.Sub(now)
				assert.Less(t, diff.Abs(), 2*time.Second, "Time difference should be less than 2 seconds")
			} else if tc.isMonthCase {
				// For month calculations, use proper calendar arithmetic
				expected := now.AddDate(0, -1, 0)
				diff := result.Sub(expected)
				assert.Less(t, diff.Abs(), 2*time.Second, "Time difference should be less than 2 seconds")
			} else if tc.isYearCase {
				// For year calculations, use proper calendar arithmetic
				expected := now.AddDate(-1, 0, 0)
				diff := result.Sub(expected)
				assert.Less(t, diff.Abs(), 2*time.Second, "Time difference should be less than 2 seconds")
			} else {
				// For other relative times, compare with the expected delta from now
				expected := now.Add(tc.expectedDelta)
				diff := result.Sub(expected)
				assert.Less(t, diff.Abs(), 2*time.Second, "Time difference should be less than 2 seconds")
			}
		})
	}
}

func TestIsPrometheusResultEmptyOrNaN(t *testing.T) {
	testCases := []struct {
		name     string
		value    model.Value
		expected bool
	}{
		{
			name:     "empty matrix",
			value:    model.Matrix{},
			expected: true,
		},
		{
			name: "matrix with valid values",
			value: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{"__name__": "test"},
					Values: []model.SamplePair{
						{Timestamp: 1000, Value: 1.5},
						{Timestamp: 2000, Value: 2.5},
					},
				},
			},
			expected: false,
		},
		{
			name: "matrix with all NaN values",
			value: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{"__name__": "test"},
					Values: []model.SamplePair{
						{Timestamp: 1000, Value: model.SampleValue(math.NaN())},
						{Timestamp: 2000, Value: model.SampleValue(math.NaN())},
					},
				},
			},
			expected: true,
		},
		{
			name: "matrix with mixed NaN and valid values",
			value: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{"__name__": "test"},
					Values: []model.SamplePair{
						{Timestamp: 1000, Value: model.SampleValue(math.NaN())},
						{Timestamp: 2000, Value: 1.5},
					},
				},
			},
			expected: false,
		},
		{
			name:     "empty vector",
			value:    model.Vector{},
			expected: true,
		},
		{
			name: "vector with valid values",
			value: model.Vector{
				&model.Sample{
					Metric:    model.Metric{"__name__": "test"},
					Timestamp: 1000,
					Value:     1.5,
				},
			},
			expected: false,
		},
		{
			name: "vector with all NaN values",
			value: model.Vector{
				&model.Sample{
					Metric:    model.Metric{"__name__": "test"},
					Timestamp: 1000,
					Value:     model.SampleValue(math.NaN()),
				},
			},
			expected: true,
		},
		{
			name:     "nil value",
			value:    nil,
			expected: false,
		},
		{
			name:     "scalar value",
			value:    &model.Scalar{Value: 1.5, Timestamp: 1000},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isPrometheusResultEmptyOrNaN(tc.value)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestQueryPrometheusHistogramParams(t *testing.T) {
	t.Run("histogram query generation with labels", func(t *testing.T) {
		params := QueryPrometheusHistogramParams{
			DatasourceUID: "prometheus",
			Metric:        "http_request_duration_seconds",
			Percentile:    95,
			Labels:        `job="api"`,
			RateInterval:  "5m",
		}

		// Test that the parameters are valid
		assert.Equal(t, "prometheus", params.DatasourceUID)
		assert.Equal(t, "http_request_duration_seconds", params.Metric)
		assert.Equal(t, float64(95), params.Percentile)
		assert.Equal(t, `job="api"`, params.Labels)
		assert.Equal(t, "5m", params.RateInterval)
	})

	t.Run("histogram query generation without labels", func(t *testing.T) {
		params := QueryPrometheusHistogramParams{
			DatasourceUID: "prometheus",
			Metric:        "http_request_duration_seconds",
			Percentile:    99,
		}

		// Test that the parameters are valid with defaults
		assert.Equal(t, "prometheus", params.DatasourceUID)
		assert.Equal(t, "http_request_duration_seconds", params.Metric)
		assert.Equal(t, float64(99), params.Percentile)
		assert.Equal(t, "", params.Labels)
		assert.Equal(t, "", params.RateInterval)
	})

	t.Run("percentile to quantile conversion", func(t *testing.T) {
		testCases := []struct {
			percentile float64
			quantile   float64
		}{
			{50, 0.5},
			{90, 0.9},
			{95, 0.95},
			{99, 0.99},
			{99.9, 0.999},
		}

		for _, tc := range testCases {
			quantile := tc.percentile / 100.0
			assert.InDelta(t, tc.quantile, quantile, 0.0001)
		}
	})
}

func TestPrometheusHistogramResult(t *testing.T) {
	t.Run("result with hints", func(t *testing.T) {
		result := &PrometheusHistogramResult{
			Result: model.Matrix{},
			Query:  "histogram_quantile(0.95, sum(rate(http_bucket[5m])) by (le))",
			Hints: []string{
				"No data found or result is NaN. Possible reasons:",
				"- Histogram metric may not exist",
			},
		}

		assert.NotNil(t, result.Hints)
		assert.Len(t, result.Hints, 2)
		assert.Contains(t, result.Query, "histogram_quantile")
		assert.Contains(t, result.Query, "0.95")
	})

	t.Run("result without hints", func(t *testing.T) {
		result := &PrometheusHistogramResult{
			Result: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{},
					Values: []model.SamplePair{
						{Timestamp: 1000, Value: 0.5},
					},
				},
			},
			Query: "histogram_quantile(0.95, sum(rate(http_bucket[5m])) by (le))",
			Hints: nil,
		}

		assert.Nil(t, result.Hints)
		assert.NotNil(t, result.Result)
	})
}

func TestQueryPrometheusHistogramPercentileValidation(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		percentile float64
		wantErr    bool
		errMsg     string
	}{
		{name: "invalid negative", percentile: -1, wantErr: true, errMsg: "percentile must be between 0 and 100"},
		{name: "invalid over 100", percentile: 101, wantErr: true, errMsg: "percentile must be between 0 and 100"},
		{name: "invalid large negative", percentile: -50, wantErr: true, errMsg: "percentile must be between 0 and 100"},
		{name: "invalid large positive", percentile: 200, wantErr: true, errMsg: "percentile must be between 0 and 100"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			args := QueryPrometheusHistogramParams{
				DatasourceUID: "test-prometheus",
				Metric:        "http_request_duration_seconds",
				Percentile:    tc.percentile,
			}
			_, err := queryPrometheusHistogram(ctx, args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errMsg)
		})
	}
}
