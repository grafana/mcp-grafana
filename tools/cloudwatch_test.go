//go:build unit

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

func TestParseCloudWatchStartTime(t *testing.T) {
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
			result, err := parseCloudWatchStartTime(tt.input)
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

func TestParseCloudWatchEndTime(t *testing.T) {
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
			result, err := parseCloudWatchEndTime(tt.input)
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

func TestListCloudWatchNamespacesParams_Structure(t *testing.T) {
	params := ListCloudWatchNamespacesParams{
		DatasourceUID: "test-uid",
		Region:        "us-west-2",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "us-west-2", params.Region)
}

func TestListCloudWatchMetricsParams_Structure(t *testing.T) {
	params := ListCloudWatchMetricsParams{
		DatasourceUID: "test-uid",
		Namespace:     "AWS/EC2",
		Region:        "eu-west-1",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "AWS/EC2", params.Namespace)
	assert.Equal(t, "eu-west-1", params.Region)
}

func TestListCloudWatchDimensionsParams_Structure(t *testing.T) {
	params := ListCloudWatchDimensionsParams{
		DatasourceUID: "test-uid",
		Namespace:     "AWS/RDS",
		MetricName:    "DatabaseConnections",
		Region:        "ap-southeast-1",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "AWS/RDS", params.Namespace)
	assert.Equal(t, "DatabaseConnections", params.MetricName)
	assert.Equal(t, "ap-southeast-1", params.Region)
}

func TestCloudWatchQueryResult_Hints(t *testing.T) {
	// Test that hints field can be populated
	result := CloudWatchQueryResult{
		Label:      "Test",
		Timestamps: []int64{},
		Values:     []float64{},
		Hints: []string{
			"Hint 1",
			"Hint 2",
		},
	}

	assert.Len(t, result.Hints, 2)
	assert.Equal(t, "Hint 1", result.Hints[0])
}

func TestParseCloudWatchResourceResponse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    []string
		expectError bool
	}{
		{
			name:     "valid response with multiple items",
			input:    `[{"text":"AWS/ECS","value":"AWS/ECS"},{"text":"AWS/EC2","value":"AWS/EC2"},{"text":"ECS/ContainerInsights","value":"ECS/ContainerInsights"}]`,
			expected: []string{"AWS/ECS", "AWS/EC2", "ECS/ContainerInsights"},
		},
		{
			name:     "empty response",
			input:    `[]`,
			expected: []string{},
		},
		{
			name:     "single item",
			input:    `[{"text":"CPUUtilization","value":"CPUUtilization"}]`,
			expected: []string{"CPUUtilization"},
		},
		{
			name:     "text and value differ",
			input:    `[{"text":"Display Name","value":"actual_value"}]`,
			expected: []string{"actual_value"},
		},
		{
			name:        "invalid JSON",
			input:       `not json`,
			expectError: true,
		},
		{
			name:        "wrong structure (plain strings)",
			input:       `["AWS/ECS","AWS/EC2"]`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseCloudWatchResourceResponse([]byte(tt.input))
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseCloudWatchMetricsResponse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    []string
		expectError bool
	}{
		{
			name:     "valid metrics response",
			input:    `[{"value":{"name":"CPUUtilization","namespace":"AWS/ECS"}},{"value":{"name":"MemoryUtilization","namespace":"AWS/ECS"}}]`,
			expected: []string{"CPUUtilization", "MemoryUtilization"},
		},
		{
			name:     "empty response",
			input:    `[]`,
			expected: []string{},
		},
		{
			name:     "single metric",
			input:    `[{"value":{"name":"CPUReservation","namespace":"AWS/ECS"}}]`,
			expected: []string{"CPUReservation"},
		},
		{
			name:        "invalid JSON",
			input:       `not json`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseCloudWatchMetricsResponse([]byte(tt.input))
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateCloudWatchEmptyResultHints(t *testing.T) {
	hints := generateCloudWatchEmptyResultHints()

	assert.NotEmpty(t, hints)
	assert.Equal(t, "No data found. Possible reasons:", hints[0])
	assert.GreaterOrEqual(t, len(hints), 5, "Should have at least 5 hints")

	// Verify hints mention the discovery tools
	hintsStr := ""
	for _, h := range hints {
		hintsStr += h + " "
	}
	assert.Contains(t, hintsStr, "list_cloudwatch_namespaces")
	assert.Contains(t, hintsStr, "list_cloudwatch_metrics")
	assert.Contains(t, hintsStr, "list_cloudwatch_dimensions")
}
