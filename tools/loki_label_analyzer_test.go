//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLokiLabelAnalyzerTools(t *testing.T) {
	t.Run("analyze loki labels (live audit)", func(t *testing.T) {
		ctx := newTestContext()
		res, err := analyzeLokiLabels(ctx, AnalyzeLokiLabelsParams{
			DatasourceUID: "loki",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Audit)
		assert.Equal(t, "live", res.Audit.Mode)
		assert.NotEmpty(t, res.Audit.Verdicts, "Live audit should produce at least one verdict")
		assert.NotEmpty(t, res.Audit.Summary)
		assert.Nil(t, res.QueryPerformance, "Perf diagnosis should not run without selector or metrics")
	})

	t.Run("analyze loki labels (live audit + perf diagnosis)", func(t *testing.T) {
		ctx := newTestContext()
		res, err := analyzeLokiLabels(ctx, AnalyzeLokiLabelsParams{
			DatasourceUID: "loki",
			Selector:      `{container=~".+"}`,
			PerfMetrics: &QueryPerfMetrics{
				QueueTimeSec: 2.0,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, res.Audit)
		require.NotNil(t, res.QueryPerformance)

		found := false
		for _, f := range res.QueryPerformance.Findings {
			if f.Bottleneck == "queue_time" {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected queue_time finding from metric input")
	})
}
