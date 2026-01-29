package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateEmptyResultHints(t *testing.T) {
	tests := []struct {
		name           string
		datasourceType string
		expectedMinLen int
		expectedFirst  string
	}{
		{
			name:           "prometheus hints",
			datasourceType: "prometheus",
			expectedMinLen: 5,
			expectedFirst:  "No data found. Possible reasons:",
		},
		{
			name:           "loki hints",
			datasourceType: "loki",
			expectedMinLen: 5,
			expectedFirst:  "No data found. Possible reasons:",
		},
		{
			name:           "cloudwatch hints",
			datasourceType: "cloudwatch",
			expectedMinLen: 6,
			expectedFirst:  "No data found. Possible reasons:",
		},
		{
			name:           "clickhouse hints",
			datasourceType: "clickhouse",
			expectedMinLen: 6,
			expectedFirst:  "No data found. Possible reasons:",
		},
		{
			name:           "unknown datasource type",
			datasourceType: "unknown",
			expectedMinLen: 4,
			expectedFirst:  "No data found. Possible reasons:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hints := GenerateEmptyResultHints(tt.datasourceType)
			assert.GreaterOrEqual(t, len(hints), tt.expectedMinLen)
			assert.Equal(t, tt.expectedFirst, hints[0])
		})
	}
}

func TestGenerateEmptyResultHints_ContainsDiscoveryToolSuggestions(t *testing.T) {
	t.Run("prometheus suggests discovery tools", func(t *testing.T) {
		hints := GenerateEmptyResultHints("prometheus")
		hintsStr := joinHints(hints)
		assert.Contains(t, hintsStr, "list_prometheus_metric_names")
		assert.Contains(t, hintsStr, "list_prometheus_label_values")
	})

	t.Run("loki suggests discovery tools", func(t *testing.T) {
		hints := GenerateEmptyResultHints("loki")
		hintsStr := joinHints(hints)
		assert.Contains(t, hintsStr, "list_loki_label_names")
		assert.Contains(t, hintsStr, "list_loki_label_values")
	})

	t.Run("cloudwatch suggests discovery tools", func(t *testing.T) {
		hints := GenerateEmptyResultHints("cloudwatch")
		hintsStr := joinHints(hints)
		assert.Contains(t, hintsStr, "list_cloudwatch_namespaces")
		assert.Contains(t, hintsStr, "list_cloudwatch_metrics")
		assert.Contains(t, hintsStr, "list_cloudwatch_dimensions")
	})

	t.Run("clickhouse suggests discovery tools", func(t *testing.T) {
		hints := GenerateEmptyResultHints("clickhouse")
		hintsStr := joinHints(hints)
		assert.Contains(t, hintsStr, "list_clickhouse_tables")
		assert.Contains(t, hintsStr, "describe_clickhouse_table")
	})
}

// joinHints joins hints into a single string for easier searching
func joinHints(hints []string) string {
	result := ""
	for _, h := range hints {
		result += h + " "
	}
	return result
}

func TestQueryPrometheusHistogramParams_Structure(t *testing.T) {
	params := QueryPrometheusHistogramParams{
		DatasourceUID: "test-uid",
		Metric:        "http_request_duration_seconds",
		Percentile:    95,
		Labels:        "job=\"api\"",
		RateInterval:  "5m",
		StartTime:     "now-1h",
		EndTime:       "now",
		StepSeconds:   60,
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "http_request_duration_seconds", params.Metric)
	assert.Equal(t, float64(95), params.Percentile)
	assert.Equal(t, "job=\"api\"", params.Labels)
	assert.Equal(t, "5m", params.RateInterval)
	assert.Equal(t, "now-1h", params.StartTime)
	assert.Equal(t, "now", params.EndTime)
	assert.Equal(t, 60, params.StepSeconds)
}

func TestGetQueryExamplesParams_Structure(t *testing.T) {
	params := GetQueryExamplesParams{
		DatasourceUID:  "test-uid",
		DatasourceType: "prometheus",
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "prometheus", params.DatasourceType)
}

func TestQueryExample_Structure(t *testing.T) {
	example := QueryExample{
		Name:        "CPU Usage",
		Description: "Average CPU usage per instance",
		Query:       `avg(rate(node_cpu_seconds_total{mode!="idle"}[5m])) by (instance)`,
	}

	assert.Equal(t, "CPU Usage", example.Name)
	assert.Equal(t, "Average CPU usage per instance", example.Description)
	assert.Contains(t, example.Query, "node_cpu_seconds_total")
}

func TestQueryExamplesResult_Structure(t *testing.T) {
	result := QueryExamplesResult{
		DatasourceType: "prometheus",
		Examples: []QueryExample{
			{Name: "Example 1", Description: "Desc 1", Query: "query1"},
			{Name: "Example 2", Description: "Desc 2", Query: "query2"},
		},
	}

	assert.Equal(t, "prometheus", result.DatasourceType)
	assert.Len(t, result.Examples, 2)
	assert.Equal(t, "Example 1", result.Examples[0].Name)
}
