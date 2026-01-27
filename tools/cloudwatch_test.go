package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudWatchQueryParams_Validation(t *testing.T) {
	// Test that the struct has the expected fields
	params := CloudWatchQueryParams{
		DatasourceUID: "test-uid",
		Namespace:     "AWS/ECS",
		MetricName:    "CPUUtilization",
		Dimensions: map[string]string{
			"ClusterName": "my-cluster",
			"ServiceName": "my-service",
		},
		Statistic: "Average",
		Period:    300,
		Start:     "now-1h",
		End:       "now",
		Region:    "us-east-1",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "AWS/ECS", params.Namespace)
	assert.Equal(t, "CPUUtilization", params.MetricName)
	assert.Equal(t, "my-cluster", params.Dimensions["ClusterName"])
	assert.Equal(t, "my-service", params.Dimensions["ServiceName"])
	assert.Equal(t, "Average", params.Statistic)
	assert.Equal(t, 300, params.Period)
	assert.Equal(t, "now-1h", params.Start)
	assert.Equal(t, "now", params.End)
	assert.Equal(t, "us-east-1", params.Region)
}

func TestCloudWatchQueryResult_Structure(t *testing.T) {
	result := CloudWatchQueryResult{
		Label:      "AWS/ECS - CPUUtilization",
		Timestamps: []int64{1705312800000, 1705313100000, 1705313400000},
		Values:     []float64{25.5, 30.2, 28.7},
		Statistics: map[string]float64{
			"avg":   28.13,
			"min":   25.5,
			"max":   30.2,
			"sum":   84.4,
			"count": 3,
		},
	}

	assert.Equal(t, "AWS/ECS - CPUUtilization", result.Label)
	assert.Len(t, result.Timestamps, 3)
	assert.Len(t, result.Values, 3)
	assert.Equal(t, 25.5, result.Values[0])
	assert.InDelta(t, 28.13, result.Statistics["avg"], 0.01)
	assert.Equal(t, 25.5, result.Statistics["min"])
	assert.Equal(t, 30.2, result.Statistics["max"])
}

func TestParseCloudWatchTime(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		checkFunc   func(t *testing.T, result time.Time)
	}{
		{
			name:  "empty string returns zero time",
			input: "",
			checkFunc: func(t *testing.T, result time.Time) {
				assert.True(t, result.IsZero())
			},
		},
		{
			name:  "now returns current time",
			input: "now",
			checkFunc: func(t *testing.T, result time.Time) {
				assert.WithinDuration(t, time.Now(), result, 5*time.Second)
			},
		},
		{
			name:  "now-1h returns time 1 hour ago",
			input: "now-1h",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Now().Add(-1 * time.Hour)
				assert.WithinDuration(t, expected, result, 5*time.Second)
			},
		},
		{
			name:  "now-6h returns time 6 hours ago",
			input: "now-6h",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Now().Add(-6 * time.Hour)
				assert.WithinDuration(t, expected, result, 5*time.Second)
			},
		},
		{
			name:  "RFC3339 format",
			input: "2024-01-15T10:00:00Z",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
				assert.Equal(t, expected, result)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseCloudWatchTime(tt.input)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}

func TestDefaultCloudWatchValues(t *testing.T) {
	// Test that constants are defined with expected values
	assert.Equal(t, 300, DefaultCloudWatchPeriod)
	assert.Equal(t, "cloudwatch", CloudWatchDatasourceType)
}

func TestApplyDatasourcePagination(t *testing.T) {
	// Create test data
	items := make([]dataSourceSummary, 25)
	for i := 0; i < 25; i++ {
		items[i] = dataSourceSummary{
			ID:   int64(i + 1),
			UID:  "uid-" + string(rune('a'+i)),
			Name: "ds-" + string(rune('a'+i)),
			Type: "prometheus",
		}
	}

	tests := []struct {
		name          string
		limit         int
		page          int
		expectedLen   int
		expectedFirst int64
		expectedLast  int64
	}{
		{
			name:          "default pagination (page 1, limit 100)",
			limit:         0,
			page:          0,
			expectedLen:   25, // All items since there are only 25
			expectedFirst: 1,
			expectedLast:  25,
		},
		{
			name:          "first page with limit 10",
			limit:         10,
			page:          1,
			expectedLen:   10,
			expectedFirst: 1,
			expectedLast:  10,
		},
		{
			name:          "second page with limit 10",
			limit:         10,
			page:          2,
			expectedLen:   10,
			expectedFirst: 11,
			expectedLast:  20,
		},
		{
			name:          "third page with limit 10 (partial)",
			limit:         10,
			page:          3,
			expectedLen:   5, // Only 5 remaining items
			expectedFirst: 21,
			expectedLast:  25,
		},
		{
			name:        "page beyond data",
			limit:       10,
			page:        10,
			expectedLen: 0,
		},
		{
			name:          "negative limit uses default",
			limit:         -1,
			page:          1,
			expectedLen:   25,
			expectedFirst: 1,
			expectedLast:  25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyDatasourcePagination(items, tt.limit, tt.page)
			assert.Len(t, result, tt.expectedLen)
			if tt.expectedLen > 0 {
				assert.Equal(t, tt.expectedFirst, result[0].ID)
				assert.Equal(t, tt.expectedLast, result[len(result)-1].ID)
			}
		})
	}
}
