//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLokiLabelAnalyzerTools(t *testing.T) {
	t.Run("get loki label cardinality (auto-discovered labels)", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getLokiLabelCardinality(ctx, GetLokiLabelCardinalityParams{
			DatasourceUID: "loki",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result, "Should measure at least one label")

		for _, r := range result {
			assert.NotEmpty(t, r.Label, "Label name should be populated")
			assert.NotEmpty(t, r.Band, "Band should be populated")
			assert.GreaterOrEqual(t, r.UniqueValues, 0, "Unique value count should be non-negative")
		}

		// Results must be sorted by cardinality descending.
		for i := 1; i < len(result); i++ {
			assert.GreaterOrEqual(t, result[i-1].UniqueValues, result[i].UniqueValues,
				"Expected results sorted by cardinality descending")
		}
	})

	t.Run("get loki label cardinality (explicit labels)", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getLokiLabelCardinality(ctx, GetLokiLabelCardinalityParams{
			DatasourceUID: "loki",
			LabelNames:    []string{"container"},
		})
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "container", result[0].Label)
	})

	t.Run("audit loki label strategy (live mode)", func(t *testing.T) {
		ctx := newTestContext()
		audit, err := auditLokiLabelStrategy(ctx, AuditLokiLabelStrategyParams{
			DatasourceUID: "loki",
		})
		require.NoError(t, err)
		require.NotNil(t, audit)
		assert.Equal(t, "live", audit.Mode)
		assert.NotEmpty(t, audit.Verdicts, "Live audit should produce at least one verdict")
		assert.NotEmpty(t, audit.Summary)
	})

	t.Run("diagnose loki query performance (live stats)", func(t *testing.T) {
		ctx := newTestContext()
		diag, err := diagnoseLokiQueryPerformance(ctx, DiagnoseLokiQueryPerformanceParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Metrics: QueryPerfMetrics{
				QueueTimeSec: 2.0,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, diag)
		require.NotEmpty(t, diag.Findings, "Should at minimum surface the queue_time finding")

		found := false
		for _, f := range diag.Findings {
			if f.Bottleneck == "queue_time" {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected queue_time finding from metric input")
	})
}
