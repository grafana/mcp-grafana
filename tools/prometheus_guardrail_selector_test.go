package tools

import (
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func TestAssessPromQuerySelectors(t *testing.T) {
	tests := []struct {
		expr  string
		block bool
	}{
		{`up`, true},
		{`up{job=~".*"}`, true},
		{`up{job=~"(?:.*)"}`, true},
		{`up{job=~".*|.*"}`, true},
		{`up{job=~".{0,}"}`, true},
		{`up{job=~".+"}`, true},
		{`up{job=~"^.+$"}`, true},
		{`{__name__=~"up|http_requests_total"}`, true},
		{`{job=~".+"}`, true},
		{`up{job!=""}`, true},
		{`up{job!="api"}`, true},
		{`up{job!~".+"}`, true},
		{`up{job=""}`, true},
		{`up{job=~"api|"}`, true},
		{`up{job!~".*"}`, false},
		{`up{job!~"(?:.*)"}`, false},
		{`up{job="api"}`, false},
		{`{job="api", instance=~".*"}`, false},
		{`up{job="api", instance=~".+"}`, false},
		{`up{job="api", instance!~".+"}`, false},
		{`up{job=~"api|worker", instance=~".*"}`, false},
		{`up{job=~"api.*"}`, false},
		{`up{job =~ ".* "}`, false},
		{`up{job="api"} or up{instance=~".*"}`, true},
		{`up{instance=~".*"} or up{job="api"}`, true},
		{`up{job!~".*"} or up`, true},
		{`max_over_time(rate(x{job=~".*"}[5m])[1h:1m])`, true},
		{`max_over_time(rate(x{job="api"}[5m])[1h:1m])`, false},
		{`rate(x{job="api"}[$__rate_interval])`, false},
		{`vector(1)`, false},
		{`1 + 2`, false},
	}
	for _, queryType := range []string{"instant", "range"} {
		for _, tc := range tests {
			t.Run(queryType+"/"+tc.expr, func(t *testing.T) {
				// Both numeric budgets disabled: the selector check is independent.
				p := promGuardPolicy{RequireSelectiveLabels: true}
				d := assessPromQuery(tc.expr, queryType, promGuardEnd.Add(-time.Hour), promGuardEnd, 60, p)
				require.NoError(t, d.ParseErr)
				if tc.block {
					require.Equal(t, promGuardBlock, d.Verdict)
					assert.Equal(t, []promGuardReason{promGuardReasonSelector}, promGuardReasons(d))
				} else {
					assert.Equal(t, promGuardAllow, d.Verdict)
				}
				p.RequireSelectiveLabels = false
				assert.Equal(t, promGuardAllow, assessPromQuery(tc.expr, queryType, promGuardEnd, promGuardEnd, 60, p).Verdict)
			})
		}
	}
}

func TestPromSelectorGuidanceAndFailure(t *testing.T) {
	p := promGuardPolicy{RequireSelectiveLabels: true, MaxRange: time.Hour, MaxPoints: 10}
	d := assessPromQuery(`up`, "range", promGuardEnd.Add(-2*time.Hour), promGuardEnd, 1, p)
	assert.Equal(t, []promGuardReason{promGuardReasonSelector, promGuardReasonRange, promGuardReasonPoints}, promGuardReasons(d))
	for _, expr := range []string{`up{job="api"}`, `up{job=~"api|worker"}`} {
		require.Contains(t, d.Violations[0].Hint, expr)
		_, err := promGuardParser.ParseExpr(expr)
		require.NoError(t, err)
	}
	d = assessPromQuery(`rate(up[$window])`, "range", promGuardEnd.Add(-2*time.Hour), promGuardEnd, 1, p)
	assert.Equal(t, []promGuardReason{promGuardReasonRange, promGuardReasonPoints}, promGuardReasons(d))
	p.MaxRange, p.MaxPoints = 0, 0
	d = assessPromQuery(`rate(up[$window])`, "instant", promGuardEnd, promGuardEnd, 60, p)
	assert.Equal(t, promGuardUnknown, d.Verdict)
	require.Error(t, d.ParseErr)
}

func TestPromSelectorMetrics(t *testing.T) {
	for _, tc := range []struct {
		mode, expr, counter, attrs string
		wantError                  bool
	}{
		{"off", `up`, "", "", false},
		{"shadow", `up`, "would_block", "reason=selector", false},
		{"enforce", `up`, "blocked", "reason=selector", true},
		{"enforce", `up{job="api"}`, "admitted", "", false},
		{"enforce", `rate(up[$window])`, "fail_open", "reason=unparseable", false},
	} {
		t.Run(tc.mode+"/"+tc.expr, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			ctx := newPromGuardMetricsContext(t, reader, tc.mode, 0, 0)
			config := mcpgrafana.GrafanaConfigFromContext(ctx)
			config.PrometheusGuardrailRequireSelectiveLabels = true
			ctx = mcpgrafana.WithGrafanaConfig(ctx, config)
			err := guardPrometheusQuery(ctx, tc.expr, "instant", promGuardEnd, promGuardEnd, 60)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tc.counter == "" {
				assert.Empty(t, promGuardCounts(t, reader))
			} else {
				assert.Equal(t, map[string]map[string]int64{"mcp.prometheus_guardrail." + tc.counter: {tc.attrs: 1}}, promGuardCounts(t, reader))
			}
		})
	}
	t.Run("selector wins over both budgets", func(t *testing.T) {
		reader := sdkmetric.NewManualReader()
		ctx := newPromGuardMetricsContext(t, reader, "enforce", time.Hour, 10)
		config := mcpgrafana.GrafanaConfigFromContext(ctx)
		config.PrometheusGuardrailRequireSelectiveLabels = true
		ctx = mcpgrafana.WithGrafanaConfig(ctx, config)
		require.Error(t, guardPrometheusQuery(ctx, `up`, "range", promGuardEnd.Add(-2*time.Hour), promGuardEnd, 1))
		assert.Equal(t, map[string]map[string]int64{"mcp.prometheus_guardrail.blocked": {"reason=selector": 1}}, promGuardCounts(t, reader))
	})
}

func TestPromSelectorBlocksBeforeHTTP(t *testing.T) {
	for _, mode := range []string{"off", "shadow", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			config, _ := promGuardConfig(mode, 0, 0)
			config.PrometheusGuardrailRequireSelectiveLabels = true
			ctx, hits := promGuardGrafana(t, config)
			_, err := queryPrometheusWithHints(ctx, QueryPrometheusParams{
				DatasourceUID: "prometheus", Expr: `up{job=~".*"}`, QueryType: "instant", StartTime: "now", EndTime: "now",
			})
			if mode == "enforce" {
				require.ErrorContains(t, err, "no selective positive label matcher")
				assert.Zero(t, *hits)
			} else {
				assert.NotZero(t, *hits)
			}
		})
	}
}

func TestPromSelectorOtherQueryPaths(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(map[bool]string{false: "unscoped", true: "scoped"}[scoped], func(t *testing.T) {
			t.Run("panel", func(t *testing.T) {
				config, _ := promGuardConfig("enforce", 0, 0)
				config.PrometheusGuardrailRequireSelectiveLabels = true
				ctx, hits := promGuardGrafana(t, config)
				expr := "up"
				if scoped {
					expr = `up{job="api"}`
				}
				_, err := executePrometheusQuery(ctx, "prometheus", expr, "now-1h", "now")
				if scoped {
					assert.NotZero(t, *hits)
				} else {
					require.ErrorContains(t, err, "no selective positive label matcher")
					assert.Zero(t, *hits)
				}
			})
			t.Run("histogram", func(t *testing.T) {
				config, _ := promGuardConfig("enforce", 0, 0)
				config.PrometheusGuardrailRequireSelectiveLabels = true
				ctx, hits := promGuardGrafana(t, config)
				args := QueryPrometheusHistogramParams{DatasourceUID: "prometheus", Metric: "http_request_duration_seconds", Percentile: 95}
				if scoped {
					args.Labels = `job="api"`
				}
				_, err := queryPrometheusHistogram(ctx, args)
				if scoped {
					assert.NotZero(t, *hits)
				} else {
					require.ErrorContains(t, err, "no selective positive label matcher")
					assert.Zero(t, *hits)
				}
			})
		})
	}
}

func BenchmarkPromSelectorAssessment(b *testing.B) {
	for _, expr := range []string{`up{job="api",instance=~".*"}`, `up{job=~"(?:.+)"}`} {
		b.Run(expr, func(b *testing.B) {
			p := promGuardPolicy{RequireSelectiveLabels: true}
			b.ReportAllocs()
			for b.Loop() {
				assessPromQuery(expr, "instant", promGuardEnd, promGuardEnd, 60, p)
			}
		})
	}
}
