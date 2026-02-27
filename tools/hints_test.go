//go:build unit

package tools

import (
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateEmptyResultHints(t *testing.T) {
	t.Run("prometheus hints", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "prometheus",
			Query:          `up{job="test"}`,
			StartTime:      time.Date(2026, 2, 2, 19, 0, 0, 0, time.UTC),
			EndTime:        time.Date(2026, 2, 2, 20, 0, 0, 0, time.UTC),
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)
		assert.Contains(t, hints.Summary, "Prometheus")
		assert.NotEmpty(t, hints.PossibleCauses)
		assert.NotEmpty(t, hints.SuggestedActions)

		// Check for specific Prometheus-related hints
		foundMetricHint := false
		for _, cause := range hints.PossibleCauses {
			if contains(cause, "metric") {
				foundMetricHint = true
				break
			}
		}
		assert.True(t, foundMetricHint, "Should have a cause about metrics")

		// Check for suggested tool usage
		foundListMetricsAction := false
		for _, action := range hints.SuggestedActions {
			if contains(action, "list_prometheus_metric_names") {
				foundListMetricsAction = true
				break
			}
		}
		assert.True(t, foundListMetricsAction, "Should suggest using list_prometheus_metric_names")

		// Check debug info
		require.NotNil(t, hints.Debug)
		assert.Contains(t, hints.Debug.TimeRange, "2026-02-02T19:00:00Z")
		assert.Contains(t, hints.Debug.TimeRange, "2026-02-02T20:00:00Z")
	})

	t.Run("prometheus rate function hints", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "prometheus",
			Query:          `rate(http_requests_total[5m])`,
			StartTime:      time.Now().Add(-1 * time.Hour),
			EndTime:        time.Now(),
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)

		// Should have rate-specific cause
		foundRateCause := false
		for _, cause := range hints.PossibleCauses {
			if contains(cause, "Rate") || contains(cause, "rate") {
				foundRateCause = true
				break
			}
		}
		assert.True(t, foundRateCause, "Should have a rate-specific cause")
	})

	t.Run("prometheus histogram hints", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "prometheus",
			Query:          `histogram_quantile(0.99, rate(request_duration_bucket[5m]))`,
			StartTime:      time.Now().Add(-1 * time.Hour),
			EndTime:        time.Now(),
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)

		// Should have histogram-specific cause
		foundHistogramCause := false
		for _, cause := range hints.PossibleCauses {
			if contains(cause, "histogram") || contains(cause, "Histogram") {
				foundHistogramCause = true
				break
			}
		}
		assert.True(t, foundHistogramCause, "Should have a histogram-specific cause")
	})

	t.Run("loki hints", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "loki",
			Query:          `{job="myapp"} |= "error"`,
			StartTime:      time.Date(2026, 2, 2, 19, 0, 0, 0, time.UTC),
			EndTime:        time.Date(2026, 2, 2, 20, 0, 0, 0, time.UTC),
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)
		assert.Contains(t, hints.Summary, "Loki")
		assert.NotEmpty(t, hints.PossibleCauses)
		assert.NotEmpty(t, hints.SuggestedActions)

		// Check for specific Loki-related hints
		foundStreamHint := false
		for _, cause := range hints.PossibleCauses {
			if contains(cause, "stream") {
				foundStreamHint = true
				break
			}
		}
		assert.True(t, foundStreamHint, "Should have a cause about streams")

		// Check for suggested tool usage
		foundListLabelsAction := false
		for _, action := range hints.SuggestedActions {
			if contains(action, "list_loki_label_names") {
				foundListLabelsAction = true
				break
			}
		}
		assert.True(t, foundListLabelsAction, "Should suggest using list_loki_label_names")

		// Should have filter-specific cause since query has |=
		foundFilterCause := false
		for _, cause := range hints.PossibleCauses {
			if contains(cause, "filter") {
				foundFilterCause = true
				break
			}
		}
		assert.True(t, foundFilterCause, "Should have a line filter cause")
	})

	t.Run("loki json parser hints", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "loki",
			Query:          `{job="myapp"} | json`,
			StartTime:      time.Now().Add(-1 * time.Hour),
			EndTime:        time.Now(),
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)

		// Should have parsing-specific cause
		foundParsingCause := false
		for _, cause := range hints.PossibleCauses {
			if contains(cause, "pars") {
				foundParsingCause = true
				break
			}
		}
		assert.True(t, foundParsingCause, "Should have a parsing-specific cause")
	})

	t.Run("loki regex hints", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "loki",
			Query:          `{job=~"myapp.*"}`,
			StartTime:      time.Now().Add(-1 * time.Hour),
			EndTime:        time.Now(),
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)

		// Should have regex-specific action
		foundRegexAction := false
		for _, action := range hints.SuggestedActions {
			if contains(action, "regex") {
				foundRegexAction = true
				break
			}
		}
		assert.True(t, foundRegexAction, "Should have a regex-specific action")
	})

	t.Run("clickhouse hints", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "clickhouse",
			Query:          "SELECT * FROM logs WHERE timestamp > now() - INTERVAL 1 HOUR",
			StartTime:      time.Now().Add(-1 * time.Hour),
			EndTime:        time.Now(),
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)
		assert.Contains(t, hints.Summary, "ClickHouse")
		assert.NotEmpty(t, hints.PossibleCauses)
		assert.NotEmpty(t, hints.SuggestedActions)

		// Check for ClickHouse-specific tool suggestions
		foundListTablesAction := false
		for _, action := range hints.SuggestedActions {
			if contains(action, "list_clickhouse_tables") {
				foundListTablesAction = true
				break
			}
		}
		assert.True(t, foundListTablesAction, "Should suggest using list_clickhouse_tables")
	})

	t.Run("cloudwatch hints", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "cloudwatch",
			Query:          "AWS/EC2 CPUUtilization",
			StartTime:      time.Now().Add(-1 * time.Hour),
			EndTime:        time.Now(),
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)
		assert.Contains(t, hints.Summary, "CloudWatch")
		assert.NotEmpty(t, hints.PossibleCauses)
		assert.NotEmpty(t, hints.SuggestedActions)

		// Check for CloudWatch-specific hints
		foundNamespaceHint := false
		for _, cause := range hints.PossibleCauses {
			if contains(cause, "namespace") {
				foundNamespaceHint = true
				break
			}
		}
		assert.True(t, foundNamespaceHint, "Should have a cause about namespaces")
	})

	t.Run("unknown datasource hints", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "unknown",
			Query:          "some query",
			StartTime:      time.Now().Add(-1 * time.Hour),
			EndTime:        time.Now(),
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)
		assert.Contains(t, hints.Summary, "no data")
		assert.NotEmpty(t, hints.PossibleCauses)
		assert.NotEmpty(t, hints.SuggestedActions)
	})

	t.Run("processed query in debug info", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "prometheus",
			Query:          `up{job="$job"}`,
			ProcessedQuery: `up{job="myapp"}`,
			StartTime:      time.Now().Add(-1 * time.Hour),
			EndTime:        time.Now(),
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)
		require.NotNil(t, hints.Debug)
		assert.Equal(t, `up{job="myapp"}`, hints.Debug.ProcessedQuery)
	})

	t.Run("no debug info when query matches processed", func(t *testing.T) {
		ctx := HintContext{
			DatasourceType: "prometheus",
			Query:          `up{job="myapp"}`,
			ProcessedQuery: `up{job="myapp"}`,
			// No time range provided
		}

		hints := GenerateEmptyResultHints(ctx)

		require.NotNil(t, hints)
		// Debug should be nil since no useful debug info
		assert.Nil(t, hints.Debug)
	})

	t.Run("case insensitive datasource type", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected string
		}{
			{"PROMETHEUS", "Prometheus"},
			{"Prometheus", "Prometheus"},
			{"prometheus", "Prometheus"},
			{"LOKI", "Loki"},
			{"Loki", "Loki"},
			{"loki", "Loki"},
		}

		for _, tc := range testCases {
			t.Run(tc.input, func(t *testing.T) {
				ctx := HintContext{
					DatasourceType: tc.input,
					Query:          "test",
				}

				hints := GenerateEmptyResultHints(ctx)

				require.NotNil(t, hints)
				assert.Contains(t, hints.Summary, tc.expected)
			})
		}
	})
}

// contains is a helper function to check if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(substr) == 0 ||
		(len(s) > 0 && containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if equalFold(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}

func equalFold(s, t string) bool {
	if len(s) != len(t) {
		return false
	}
	for i := 0; i < len(s); i++ {
		sr := s[i]
		tr := t[i]
		if sr == tr {
			continue
		}
		// Convert to lowercase
		if 'A' <= sr && sr <= 'Z' {
			sr += 'a' - 'A'
		}
		if 'A' <= tr && tr <= 'Z' {
			tr += 'a' - 'A'
		}
		if sr != tr {
			return false
		}
	}
	return true
}

func TestIsPrometheusResultEmpty(t *testing.T) {
	t.Run("nil result is empty", func(t *testing.T) {
		assert.True(t, isPrometheusResultEmpty(nil))
	})

	t.Run("empty vector is empty", func(t *testing.T) {
		emptyVector := model.Vector{}
		assert.True(t, isPrometheusResultEmpty(emptyVector))
	})

	t.Run("non-empty vector is not empty", func(t *testing.T) {
		nonEmptyVector := model.Vector{
			&model.Sample{
				Metric:    model.Metric{"__name__": "test"},
				Value:     42,
				Timestamp: model.Time(1234567890000),
			},
		}
		assert.False(t, isPrometheusResultEmpty(nonEmptyVector))
	})

	t.Run("empty matrix is empty", func(t *testing.T) {
		emptyMatrix := model.Matrix{}
		assert.True(t, isPrometheusResultEmpty(emptyMatrix))
	})

	t.Run("non-empty matrix is not empty", func(t *testing.T) {
		nonEmptyMatrix := model.Matrix{
			&model.SampleStream{
				Metric: model.Metric{"__name__": "test"},
				Values: []model.SamplePair{
					{Timestamp: 1234567890000, Value: 42},
				},
			},
		}
		assert.False(t, isPrometheusResultEmpty(nonEmptyMatrix))
	})

	t.Run("scalar is not empty", func(t *testing.T) {
		scalar := &model.Scalar{
			Timestamp: model.Time(1234567890000),
			Value:     42,
		}
		assert.False(t, isPrometheusResultEmpty(scalar))
	})

	t.Run("nil scalar is empty", func(t *testing.T) {
		var nilScalar *model.Scalar
		assert.True(t, isPrometheusResultEmpty(nilScalar))
	})

	t.Run("non-empty string is not empty", func(t *testing.T) {
		str := &model.String{
			Timestamp: model.Time(1234567890000),
			Value:     "hello",
		}
		assert.False(t, isPrometheusResultEmpty(str))
	})

	t.Run("empty string is empty", func(t *testing.T) {
		str := &model.String{
			Timestamp: model.Time(1234567890000),
			Value:     "",
		}
		assert.True(t, isPrometheusResultEmpty(str))
	})

	t.Run("nil string is empty", func(t *testing.T) {
		var nilStr *model.String
		assert.True(t, isPrometheusResultEmpty(nilStr))
	})
}
