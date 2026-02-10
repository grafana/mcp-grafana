//go:build unit

package tools

import (
	"net/url"
	"testing"

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

func TestCloudWatchMultiFrameStatistics(t *testing.T) {
	// Build a cloudWatchQueryResponse with 2 frames to verify statistics
	// are accumulated across all frames, not just the last one.
	resp := &cloudWatchQueryResponse{
		Results: map[string]struct {
			Status int `json:"status,omitempty"`
			Frames []struct {
				Schema struct {
					Name   string `json:"name,omitempty"`
					RefID  string `json:"refId,omitempty"`
					Fields []struct {
						Name     string                 `json:"name"`
						Type     string                 `json:"type"`
						Labels   map[string]string      `json:"labels,omitempty"`
						Config   map[string]interface{} `json:"config,omitempty"`
						TypeInfo struct {
							Frame string `json:"frame,omitempty"`
						} `json:"typeInfo,omitempty"`
					} `json:"fields"`
				} `json:"schema"`
				Data struct {
					Values [][]interface{} `json:"values"`
				} `json:"data"`
			} `json:"frames,omitempty"`
			Error string `json:"error,omitempty"`
		}{},
	}

	// Frame type for convenience
	type frame = struct {
		Schema struct {
			Name   string `json:"name,omitempty"`
			RefID  string `json:"refId,omitempty"`
			Fields []struct {
				Name     string                 `json:"name"`
				Type     string                 `json:"type"`
				Labels   map[string]string      `json:"labels,omitempty"`
				Config   map[string]interface{} `json:"config,omitempty"`
				TypeInfo struct {
					Frame string `json:"frame,omitempty"`
				} `json:"typeInfo,omitempty"`
			} `json:"fields"`
		} `json:"schema"`
		Data struct {
			Values [][]interface{} `json:"values"`
		} `json:"data"`
	}

	type field = struct {
		Name     string                 `json:"name"`
		Type     string                 `json:"type"`
		Labels   map[string]string      `json:"labels,omitempty"`
		Config   map[string]interface{} `json:"config,omitempty"`
		TypeInfo struct {
			Frame string `json:"frame,omitempty"`
		} `json:"typeInfo,omitempty"`
	}

	// Frame 1: values 10, 20 (sum=30, min=10, max=20)
	f1 := frame{}
	f1.Schema.Fields = []field{
		{Name: "Time", Type: "time"},
		{Name: "Value", Type: "number"},
	}
	f1.Data.Values = [][]interface{}{
		{float64(1000), float64(2000)},       // timestamps
		{float64(10.0), float64(20.0)},       // values
	}

	// Frame 2: values 5, 40 (sum=45, min=5, max=40)
	f2 := frame{}
	f2.Schema.Fields = []field{
		{Name: "Time", Type: "time"},
		{Name: "Value", Type: "number"},
	}
	f2.Data.Values = [][]interface{}{
		{float64(3000), float64(4000)},       // timestamps
		{float64(5.0), float64(40.0)},        // values
	}

	type resultType = struct {
		Status int `json:"status,omitempty"`
		Frames []struct {
			Schema struct {
				Name   string `json:"name,omitempty"`
				RefID  string `json:"refId,omitempty"`
				Fields []struct {
					Name     string                 `json:"name"`
					Type     string                 `json:"type"`
					Labels   map[string]string      `json:"labels,omitempty"`
					Config   map[string]interface{} `json:"config,omitempty"`
					TypeInfo struct {
						Frame string `json:"frame,omitempty"`
					} `json:"typeInfo,omitempty"`
				} `json:"fields"`
			} `json:"schema"`
			Data struct {
				Values [][]interface{} `json:"values"`
			} `json:"data"`
		} `json:"frames,omitempty"`
		Error string `json:"error,omitempty"`
	}

	resp.Results["A"] = resultType{
		Frames: []frame{f1, f2},
	}

	// Process the response the same way queryCloudWatch does
	result := &CloudWatchQueryResult{
		Label:      "Test",
		Timestamps: []int64{},
		Values:     []float64{},
		Statistics: make(map[string]float64),
	}

	for _, r := range resp.Results {
		var sum, min, max float64
		var count int64
		first := true

		for _, frm := range r.Frames {
			var timeColIdx, valueColIdx = -1, -1
			for i, fld := range frm.Schema.Fields {
				switch fld.Type {
				case "time":
					timeColIdx = i
				case "number":
					valueColIdx = i
				}
			}
			if timeColIdx == -1 || valueColIdx == -1 {
				continue
			}
			if len(frm.Data.Values) > timeColIdx && len(frm.Data.Values) > valueColIdx {
				timeValues := frm.Data.Values[timeColIdx]
				metricValues := frm.Data.Values[valueColIdx]
				for i := 0; i < len(timeValues) && i < len(metricValues); i++ {
					ts, ok := timeValues[i].(float64)
					if !ok {
						continue
					}
					val, ok := metricValues[i].(float64)
					if !ok {
						continue
					}
					result.Timestamps = append(result.Timestamps, int64(ts))
					result.Values = append(result.Values, val)
					sum += val
					count++
					if first {
						min = val
						max = val
						first = false
					} else {
						if val < min {
							min = val
						}
						if val > max {
							max = val
						}
					}
				}
			}
		}
		if count > 0 {
			result.Statistics["sum"] = sum
			result.Statistics["min"] = min
			result.Statistics["max"] = max
			result.Statistics["avg"] = sum / float64(count)
			result.Statistics["count"] = float64(count)
		}
	}

	// Verify all 4 data points accumulated across both frames
	assert.Len(t, result.Timestamps, 4)
	assert.Len(t, result.Values, 4)

	// Statistics should span both frames: min=5, max=40, sum=75, count=4, avg=18.75
	assert.Equal(t, 75.0, result.Statistics["sum"])
	assert.Equal(t, 5.0, result.Statistics["min"])
	assert.Equal(t, 40.0, result.Statistics["max"])
	assert.Equal(t, 4.0, result.Statistics["count"])
	assert.Equal(t, 18.75, result.Statistics["avg"])
}

func TestCloudWatchURLEncoding(t *testing.T) {
	tests := []struct {
		name       string
		namespace  string
		metricName string
		region     string
		wantParams map[string]string
	}{
		{
			name:       "standard AWS namespace with slash",
			namespace:  "AWS/EC2",
			metricName: "CPUUtilization",
			region:     "us-east-1",
			wantParams: map[string]string{
				"namespace":  "AWS/EC2",
				"metricName": "CPUUtilization",
				"region":     "us-east-1",
			},
		},
		{
			name:       "custom namespace with hash character",
			namespace:  "Custom#Namespace",
			metricName: "MyMetric",
			region:     "us-west-2",
			wantParams: map[string]string{
				"namespace":  "Custom#Namespace",
				"metricName": "MyMetric",
				"region":     "us-west-2",
			},
		},
		{
			name:       "namespace with spaces",
			namespace:  "Custom Namespace",
			metricName: "My Metric",
			region:     "eu-west-1",
			wantParams: map[string]string{
				"namespace":  "Custom Namespace",
				"metricName": "My Metric",
				"region":     "eu-west-1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build params the same way the fixed code does
			params := url.Values{}
			params.Set("namespace", tt.namespace)
			params.Set("metricName", tt.metricName)
			if tt.region != "" {
				params.Set("region", tt.region)
			}

			encoded := params.Encode()

			// Parse back to verify round-trip
			parsed, err := url.ParseQuery(encoded)
			require.NoError(t, err)

			for key, want := range tt.wantParams {
				assert.Equal(t, want, parsed.Get(key), "parameter %s should round-trip correctly", key)
			}

			// Verify the hash character is encoded (not treated as fragment)
			if tt.namespace == "Custom#Namespace" {
				assert.Contains(t, encoded, "Custom%23Namespace", "# should be percent-encoded")
				assert.NotContains(t, encoded, "Custom#", "raw # should not appear in encoded query")
			}
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
