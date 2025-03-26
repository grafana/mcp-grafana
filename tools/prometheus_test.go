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
	"github.com/prometheus/prometheus/model/labels"
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

func TestSelectorMatches(t *testing.T) {
	testCases := []struct {
		name      string
		selector  Selector
		labels    map[string]string
		expected  bool
		expectErr bool
	}{
		{
			name: "Equal match",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "=", Value: "prometheus"},
				},
			},
			labels:   map[string]string{"job": "prometheus"},
			expected: true,
		},
		{
			name: "Equal no match",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "=", Value: "prometheus"},
				},
			},
			labels:   map[string]string{"job": "node-exporter"},
			expected: false,
		},
		{
			name: "Not equal match",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "!=", Value: "prometheus"},
				},
			},
			labels:   map[string]string{"job": "node-exporter"},
			expected: true,
		},
		{
			name: "Not equal no match",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "!=", Value: "prometheus"},
				},
			},
			labels:   map[string]string{"job": "prometheus"},
			expected: false,
		},
		{
			name: "Regex match",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "=~", Value: "prom.*"},
				},
			},
			labels:   map[string]string{"job": "prometheus"},
			expected: true,
		},
		{
			name: "Regex no match",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "=~", Value: "node.*"},
				},
			},
			labels:   map[string]string{"job": "prometheus"},
			expected: false,
		},
		{
			name: "Not regex match",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "!~", Value: "node.*"},
				},
			},
			labels:   map[string]string{"job": "prometheus"},
			expected: true,
		},
		{
			name: "Not regex no match",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "!~", Value: "prom.*"},
				},
			},
			labels:   map[string]string{"job": "prometheus"},
			expected: false,
		},
		{
			name: "Multiple filters all match",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "=", Value: "prometheus"},
					{Name: "instance", Type: "=~", Value: "localhost.*"},
				},
			},
			labels:   map[string]string{"job": "prometheus", "instance": "localhost:9090"},
			expected: true,
		},
		{
			name: "Multiple filters one doesn't match",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "=", Value: "prometheus"},
					{Name: "instance", Type: "=~", Value: "remote.*"},
				},
			},
			labels:   map[string]string{"job": "prometheus", "instance": "localhost:9090"},
			expected: false,
		},
		{
			name: "Label doesn't exist with = operator",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "missing", Type: "=", Value: "value"},
				},
			},
			labels:   map[string]string{"job": "prometheus"},
			expected: false,
		},
		{
			name: "Label doesn't exist with != operator",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "missing", Type: "!=", Value: "value"},
				},
			},
			labels:   map[string]string{"job": "prometheus"},
			expected: true,
		},
		{
			name: "Invalid matcher type",
			selector: Selector{
				Filters: []LabelMatcher{
					{Name: "job", Type: "<>", Value: "prometheus"},
				},
			},
			labels:    map[string]string{"job": "prometheus"},
			expected:  false,
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lbls := labels.FromMap(tc.labels)
			result, err := tc.selector.Matches(lbls)

			if tc.expectErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestPrometheusQueries(t *testing.T) {
	t.Run("query prometheus range", func(t *testing.T) {
		t.Skip("Skipping because we don't have a Prometheus instance with enough data")
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
