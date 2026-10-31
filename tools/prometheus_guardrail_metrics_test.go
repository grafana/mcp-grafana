package tools

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// newPromGuardMetricsContext points the guardrail's process-wide instruments
// at a fresh MeterProvider built from reader, resetting the once before and
// after so other tests neither leak into nor read from this one.
func newPromGuardMetricsContext(t *testing.T, reader sdkmetric.Reader, mode string, maxRange time.Duration, maxPoints int64) context.Context {
	t.Helper()
	reset := func() {
		promGuardrailMetricsOnce = sync.Once{}
		promGuardrailInstruments = promGuardrailMetrics{}
	}
	reset()
	t.Cleanup(reset)

	config, _ := promGuardConfig(mode, maxRange, maxPoints)
	config.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return mcpgrafana.WithGrafanaConfig(context.Background(), config)
}

// promGuardCounts returns, per counter name, the value recorded for each
// attribute set rendered as a sorted "k=v,k=v" key.
func promGuardCounts(t *testing.T, reader *sdkmetric.ManualReader) map[string]map[string]int64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))

	out := map[string]map[string]int64{}
	for _, sm := range data.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				var parts []string
				for _, kv := range dp.Attributes.ToSlice() {
					parts = append(parts, string(kv.Key)+"="+kv.Value.String())
				}
				sort.Strings(parts)
				if out[m.Name] == nil {
					out[m.Name] = map[string]int64{}
				}
				out[m.Name][strings.Join(parts, ",")] += dp.Value
			}
		}
	}
	return out
}

func TestPrometheusGuardrailMetricsVerdicts(t *testing.T) {
	start := promGuardEnd.Add(-time.Hour)
	monthAgo := promGuardEnd.Add(-30 * 24 * time.Hour)

	tests := []struct {
		name    string
		mode    string
		expr    string
		start   time.Time
		step    int
		counter string
		attrs   string
	}{
		{"admitted", mcpgrafana.PrometheusGuardrailEnforce, `up`, start, 60, "mcp.prometheus_guardrail.admitted", ""},
		{"shadow records would_block", mcpgrafana.PrometheusGuardrailShadow, `up`, monthAgo, 3600, "mcp.prometheus_guardrail.would_block", "reason=range"},
		{"enforce records blocked", mcpgrafana.PrometheusGuardrailEnforce, `up`, monthAgo, 3600, "mcp.prometheus_guardrail.blocked", "reason=range"},
		{"unknown mode records blocked", "enfore", `up`, monthAgo, 3600, "mcp.prometheus_guardrail.blocked", "reason=range"},
		{"points reason", mcpgrafana.PrometheusGuardrailShadow, `up`, promGuardEnd.Add(-4 * time.Hour), 1, "mcp.prometheus_guardrail.would_block", "reason=points"},
		{"range wins over points", mcpgrafana.PrometheusGuardrailShadow, `up`, monthAgo, 1, "mcp.prometheus_guardrail.would_block", "reason=range"},
		{"unparseable fails open", mcpgrafana.PrometheusGuardrailEnforce, `rate(x[$window])`, start, 60, "mcp.prometheus_guardrail.fail_open", "reason=unparseable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			ctx := newPromGuardMetricsContext(t, reader, tc.mode, 192*time.Hour, 11000)
			_ = guardPrometheusQuery(ctx, tc.expr, "range", tc.start, promGuardEnd, tc.step)

			// Exactly one counter moves, by one: the verdicts partition the
			// guarded queries.
			assert.Equal(t, map[string]map[string]int64{tc.counter: {tc.attrs: 1}}, promGuardCounts(t, reader))
		})
	}
}

// An unguarded query must not show up in any counter, including an
// unparseable one that would otherwise count as fail_open.
func TestPrometheusGuardrailMetricsRecordNothingWhenNotGuarded(t *testing.T) {
	start := promGuardEnd.Add(-30 * 24 * time.Hour)

	t.Run("mode off", func(t *testing.T) {
		reader := sdkmetric.NewManualReader()
		ctx := newPromGuardMetricsContext(t, reader, mcpgrafana.PrometheusGuardrailOff, 192*time.Hour, 11000)
		require.NoError(t, guardPrometheusQuery(ctx, `up`, "range", start, promGuardEnd, 1))
		require.NoError(t, guardPrometheusQuery(ctx, `rate(x[$window])`, "range", start, promGuardEnd, 1))
		assert.Empty(t, promGuardCounts(t, reader))
	})

	t.Run("mode unset", func(t *testing.T) {
		reader := sdkmetric.NewManualReader()
		ctx := newPromGuardMetricsContext(t, reader, "", 192*time.Hour, 11000)
		require.NoError(t, guardPrometheusQuery(ctx, `up`, "range", start, promGuardEnd, 1))
		assert.Empty(t, promGuardCounts(t, reader))
	})

	t.Run("unknown query type", func(t *testing.T) {
		reader := sdkmetric.NewManualReader()
		ctx := newPromGuardMetricsContext(t, reader, mcpgrafana.PrometheusGuardrailEnforce, 192*time.Hour, 11000)
		require.NoError(t, guardPrometheusQuery(ctx, `up`, "rnage", start, promGuardEnd, 1))
		assert.Empty(t, promGuardCounts(t, reader))
	})
}

// The counters must reach the injected provider under the shared meter name
// and the {query} unit, not the global provider.
func TestPrometheusGuardrailMetricsUseInjectedMeterProvider(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	ctx := newPromGuardMetricsContext(t, reader, mcpgrafana.PrometheusGuardrailEnforce, 192*time.Hour, 11000)
	require.Error(t, guardPrometheusQuery(ctx, `up`, "range", promGuardEnd.Add(-30*24*time.Hour), promGuardEnd, 3600))

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))
	require.Len(t, data.ScopeMetrics, 1)
	assert.Equal(t, "mcp-grafana", data.ScopeMetrics[0].Scope.Name)
	require.Len(t, data.ScopeMetrics[0].Metrics, 1)
	m := data.ScopeMetrics[0].Metrics[0]
	assert.Equal(t, "mcp.prometheus_guardrail.blocked", m.Name)
	assert.Equal(t, "{query}", m.Unit)
}

// The exported Prometheus name is what dashboards and alerts use.
func TestPrometheusGuardrailMetricsExportedName(t *testing.T) {
	registry := promclient.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	require.NoError(t, err)
	ctx := newPromGuardMetricsContext(t, exporter, mcpgrafana.PrometheusGuardrailEnforce, 192*time.Hour, 11000)
	require.Error(t, guardPrometheusQuery(ctx, `up`, "range", promGuardEnd.Add(-30*24*time.Hour), promGuardEnd, 3600))

	families, err := registry.Gather()
	require.NoError(t, err)
	var found bool
	for _, f := range families {
		if f.GetName() != "mcp_prometheus_guardrail_blocked_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "reason" && l.GetValue() == "range" {
					found = true
					assert.Equal(t, 1.0, m.GetCounter().GetValue())
				}
			}
		}
	}
	assert.True(t, found, "mcp_prometheus_guardrail_blocked_total{reason=\"range\"} not exported")
}
