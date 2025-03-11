// Requires a Grafana instance running on localhost:3000,
// with a Prometheus datasource provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusTools(t *testing.T) {
	t.Run("list prometheus metric metadata", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listPrometheusMetricMetadata(ctx, ListPrometheusMetricMetadataParams{
			DatasourceUID: "prometheus",
		})
		require.NoError(t, err)
		assert.Len(t, result, 10)
	})

	t.Run("list prometheus metric names", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listPrometheusMetricNames(ctx, ListPrometheusMetricNamesParams{
			DatasourceUID: "prometheus",
			Regex:         ".*",
			Limit:         10,
		})
		require.NoError(t, err)
		assert.Len(t, result, 10)
	})

	t.Run("list prometheus label names", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listPrometheusLabelNames(ctx, ListPrometheusLabelNamesParams{
			DatasourceUID: "prometheus",
			Matches: []Selector{
				{
					Filters: []LabelMatcher{
						{Name: "job", Value: "prometheus"},
					},
				},
			},
			Limit: 10,
		})
		require.NoError(t, err)
		assert.Len(t, result, 10)
	})

	t.Run("list prometheus label values", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listPrometheusLabelValues(ctx, ListPrometheusLabelValuesParams{
			DatasourceUID: "prometheus",
			LabelName:     "job",
			Matches: []Selector{
				{
					Filters: []LabelMatcher{
						{Name: "job", Value: "prometheus"},
					},
				},
			},
		})
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

func TestPrometheusQueries(t *testing.T) {
	t.Run("query prometheus range", func(t *testing.T) {
		end := time.Now()
		start := end.Add(-10 * time.Minute)
		for _, step := range []int{15, 60, 300} {
			t.Run(fmt.Sprintf("step=%d", step), func(t *testing.T) {
				ctx := newTestContext()
				result, err := queryPrometheus(ctx, QueryPrometheusParams{
					DatasourceUID: "prometheus",
					Expr:          "up",
					StartRFC3339:  start.Format(time.RFC3339),
					EndRFC3339:    end.Format(time.RFC3339),
					StepSeconds:   step,
					QueryType:     "range",
				})
				require.NoError(t, err)
				matrix := result.(model.Matrix)
				require.Len(t, matrix, 1)
				expectedLen := int(end.Sub(start).Seconds()/float64(step)) + 1
				assert.Len(t, matrix[0].Values, expectedLen)
				assert.Less(t, matrix[0].Values[0].Timestamp.Sub(model.TimeFromUnix(start.Unix())), time.Duration(step)*time.Second)
				assert.Equal(t, matrix[0].Metric["__name__"], model.LabelValue("up"))
			})
		}
	})

	t.Run("query prometheus instant", func(t *testing.T) {
		ctx := newTestContext()
		result, err := queryPrometheus(ctx, QueryPrometheusParams{
			DatasourceUID: "prometheus",
			Expr:          "up",
			StartRFC3339:  time.Now().Format(time.RFC3339),
			QueryType:     "instant",
		})
		require.NoError(t, err)
		scalar := result.(model.Vector)
		assert.Equal(t, scalar[0].Value, model.SampleValue(1))
		assert.Equal(t, scalar[0].Timestamp, model.TimeFromUnix(time.Now().Unix()))
		assert.Equal(t, scalar[0].Metric["__name__"], model.LabelValue("up"))
	})
}
