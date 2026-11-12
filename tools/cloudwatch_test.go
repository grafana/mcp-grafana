//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
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
		AccountId: "123456789012",
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
	assert.Equal(t, "123456789012", params.AccountId)
}

func TestCloudWatchQueryParams_AccountIdAll(t *testing.T) {
	// Test that AccountId supports the "all" wildcard value
	params := CloudWatchQueryParams{
		DatasourceUID: "test-uid",
		Namespace:     "AWS/EC2",
		MetricName:    "CPUUtilization",
		Region:        "us-east-1",
		AccountId:     "all",
	}

	assert.Equal(t, "all", params.AccountId)
}

func TestCloudWatchQueryParams_AccountIdEmpty(t *testing.T) {
	// Test that AccountId is optional and defaults to empty
	params := CloudWatchQueryParams{
		DatasourceUID: "test-uid",
		Namespace:     "AWS/EC2",
		MetricName:    "CPUUtilization",
		Region:        "us-east-1",
	}

	assert.Empty(t, params.AccountId)
}

func TestCloudWatchSeries_Structure(t *testing.T) {
	result := CloudWatchSeries{
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
		AccountId:     "123456789012",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "us-west-2", params.Region)
	assert.Equal(t, "123456789012", params.AccountId)
}

func TestListCloudWatchMetricsParams_Structure(t *testing.T) {
	params := ListCloudWatchMetricsParams{
		DatasourceUID: "test-uid",
		Namespace:     "AWS/EC2",
		Region:        "eu-west-1",
		AccountId:     "all",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "AWS/EC2", params.Namespace)
	assert.Equal(t, "eu-west-1", params.Region)
	assert.Equal(t, "all", params.AccountId)
}

func TestListCloudWatchDimensionsParams_Structure(t *testing.T) {
	params := ListCloudWatchDimensionsParams{
		DatasourceUID: "test-uid",
		Namespace:     "AWS/RDS",
		MetricName:    "DatabaseConnections",
		Region:        "ap-southeast-1",
		AccountId:     "987654321098",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "AWS/RDS", params.Namespace)
	assert.Equal(t, "DatabaseConnections", params.MetricName)
	assert.Equal(t, "ap-southeast-1", params.Region)
	assert.Equal(t, "987654321098", params.AccountId)
}

func TestListCloudWatchDimensionValuesParams_Structure(t *testing.T) {
	params := ListCloudWatchDimensionValuesParams{
		DatasourceUID: "test-uid",
		Namespace:     "AWS/RDS",
		MetricName:    "DatabaseConnections",
		DimensionKey:  "DBInstanceIdentifier",
		Region:        "ap-southeast-1",
		AccountId:     "987654321098",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "AWS/RDS", params.Namespace)
	assert.Equal(t, "DatabaseConnections", params.MetricName)
	assert.Equal(t, "DBInstanceIdentifier", params.DimensionKey)
	assert.Equal(t, "ap-southeast-1", params.Region)
	assert.Equal(t, "987654321098", params.AccountId)
}

func TestCloudWatchQueryResult_Hints(t *testing.T) {
	// Test that hints field can be populated
	result := CloudWatchQueryResult{
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

func cloudWatchTestFrame(label string, ms []int64, vals []float64) *data.Frame {
	times := make([]time.Time, len(ms))
	for i, m := range ms {
		times[i] = time.UnixMilli(m)
	}
	value := data.NewField("Value", nil, vals)
	if label != "" {
		value.Config = &data.FieldConfig{DisplayNameFromDS: label}
	}
	return data.NewFrame("", data.NewField("Time", nil, times), value)
}

func cloudWatchResultJSON(t *testing.T, frames ...*data.Frame) string {
	t.Helper()
	resp := &backend.QueryDataResponse{
		Responses: backend.Responses{"A": backend.DataResponse{Frames: frames}},
	}
	result, err := cloudWatchResultFromResponse(resp, "AWS/EC2 - CPUUtilization")
	require.NoError(t, err)
	b, err := json.Marshal(result)
	require.NoError(t, err)
	return string(b)
}

func TestCloudWatchResultFromResponse_MultipleFramesStaySeparate(t *testing.T) {
	got := cloudWatchResultJSON(t,
		cloudWatchTestFrame("i-aaa", []int64{1000, 2000}, []float64{10, 20}),
		cloudWatchTestFrame("i-bbb", []int64{1000, 2000}, []float64{5, 40}),
	)

	assert.JSONEq(t, `{
		"series": [
			{
				"label": "i-aaa",
				"timestamps": [1000, 2000],
				"values": [10, 20],
				"statistics": {"sum": 30, "min": 10, "max": 20, "avg": 15, "count": 2}
			},
			{
				"label": "i-bbb",
				"timestamps": [1000, 2000],
				"values": [5, 40],
				"statistics": {"sum": 45, "min": 5, "max": 40, "avg": 22.5, "count": 2}
			}
		]
	}`, got)
}

func TestCloudWatchResultFromResponse_SingleFrameUsesSeries(t *testing.T) {
	got := cloudWatchResultJSON(t,
		cloudWatchTestFrame("i-aaa", []int64{1000, 2000}, []float64{10, 20}),
	)

	assert.JSONEq(t, `{
		"series": [
			{
				"label": "i-aaa",
				"timestamps": [1000, 2000],
				"values": [10, 20],
				"statistics": {"sum": 30, "min": 10, "max": 20, "avg": 15, "count": 2}
			}
		]
	}`, got)
}

func TestCloudWatchResultFromResponse_NoFramesReturnsEmptySeries(t *testing.T) {
	got := cloudWatchResultJSON(t)

	hints, err := json.Marshal(generateCloudWatchEmptyResultHints())
	require.NoError(t, err)
	assert.JSONEq(t, `{"series": [], "hints": `+string(hints)+`}`, got)
}

func TestCloudWatchSeriesLabel(t *testing.T) {
	const def = "AWS/EC2 - CPUUtilization"
	named := cloudWatchTestFrame("", []int64{1000}, []float64{1})
	named.Name = "i-named"

	labeled := cloudWatchTestFrame("", []int64{1000}, []float64{1})
	labeled.Fields[1].Labels = data.Labels{"InstanceId": "i-labeled"}

	for name, tc := range map[string]struct {
		frame *data.Frame
		want  string
	}{
		"display name wins": {cloudWatchTestFrame("i-display", []int64{1000}, []float64{1}), "i-display"},
		"frame name":        {named, "i-named"},
		"field labels":      {labeled, "InstanceId=i-labeled"},
		"default":           {cloudWatchTestFrame("", []int64{1000}, []float64{1}), def},
	} {
		t.Run(name, func(t *testing.T) {
			got := cloudWatchSeriesLabel(tc.frame, tc.frame.Fields[1], def)
			assert.Contains(t, got, tc.want)
		})
	}
}

func TestCloudWatchURLEncoding(t *testing.T) {
	tests := []struct {
		name         string
		namespace    string
		metricName   string
		region       string
		dimensionKey string
		wantParams   map[string]string
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
			name:         "dimension key with spaces for dimension-values lookup",
			namespace:    "Custom Namespace",
			metricName:   "My Metric",
			region:       "eu-west-1",
			dimensionKey: "Instance Name",
			wantParams: map[string]string{
				"namespace":    "Custom Namespace",
				"metricName":   "My Metric",
				"region":       "eu-west-1",
				"dimensionKey": "Instance Name",
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
			if tt.dimensionKey != "" {
				params.Set("dimensionKey", tt.dimensionKey)
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

func TestCloudWatchQueryParams_JSONSerialization(t *testing.T) {
	t.Run("accountId included when set", func(t *testing.T) {
		params := CloudWatchQueryParams{
			DatasourceUID: "test-uid",
			Namespace:     "AWS/EC2",
			MetricName:    "CPUUtilization",
			Region:        "us-east-1",
			AccountId:     "123456789012",
		}

		data, err := json.Marshal(params)
		require.NoError(t, err)

		var raw map[string]interface{}
		err = json.Unmarshal(data, &raw)
		require.NoError(t, err)

		assert.Equal(t, "123456789012", raw["accountId"])
	})

	t.Run("accountId omitted when empty", func(t *testing.T) {
		params := CloudWatchQueryParams{
			DatasourceUID: "test-uid",
			Namespace:     "AWS/EC2",
			MetricName:    "CPUUtilization",
			Region:        "us-east-1",
		}

		data, err := json.Marshal(params)
		require.NoError(t, err)

		var raw map[string]interface{}
		err = json.Unmarshal(data, &raw)
		require.NoError(t, err)

		_, exists := raw["accountId"]
		assert.False(t, exists, "accountId should be omitted from JSON when empty")
	})
}

func TestCloudWatchAccountIdURLEncoding(t *testing.T) {
	tests := []struct {
		name      string
		accountId string
		wantParam string
	}{
		{
			name:      "specific account ID",
			accountId: "123456789012",
			wantParam: "123456789012",
		},
		{
			name:      "all accounts wildcard",
			accountId: "all",
			wantParam: "all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := url.Values{}
			params.Set("region", "us-east-1")
			if tt.accountId != "" {
				params.Set("accountId", tt.accountId)
			}

			encoded := params.Encode()
			parsed, err := url.ParseQuery(encoded)
			require.NoError(t, err)
			assert.Equal(t, tt.wantParam, parsed.Get("accountId"))
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

func TestCloudWatchClient_QuerySendsPercentileStatistic(t *testing.T) {
	var gotQuery map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Queries []map[string]any `json:"queries"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Len(t, payload.Queries, 1)
		gotQuery = payload.Queries[0]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{}}`))
	}))
	defer server.Close()

	client := &cloudWatchClient{httpClient: server.Client(), baseURL: server.URL}
	now := time.Now()
	_, err := client.query(context.Background(), CloudWatchQueryParams{
		DatasourceUID: "cw",
		Namespace:     "AWS/ApplicationELB",
		MetricName:    "TargetResponseTime",
		Statistic:     "p99.9",
		Region:        "us-east-1",
	}, now.Add(-time.Hour), now)
	require.NoError(t, err)

	assert.Equal(t, "p99.9", gotQuery["statistic"])
}
