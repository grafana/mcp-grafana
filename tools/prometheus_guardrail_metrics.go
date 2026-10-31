package tools

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const promGuardrailMeterName = "mcp-grafana"

// promGuardrailMetrics holds the OTel instruments for Prometheus guardrail
// decisions. The four counters partition every guarded query exactly once.
// There is no backend attribute: unlike Loki, every Prometheus backend gets
// the same checks.
type promGuardrailMetrics struct {
	admitted   metric.Int64Counter // Passed every enabled check
	wouldBlock metric.Int64Counter // Failed a check in shadow mode; ran anyway
	blocked    metric.Int64Counter // Failed a check in enforce mode; rejected
	failOpen   metric.Int64Counter // Expression could not be parsed; admitted
}

var (
	// promGuardrailMetricsOnce builds the instruments from the first request
	// that reaches the guardrail, for the same reason as
	// lokiGuardrailMetricsOnce. Vars so tests can reset them.
	promGuardrailMetricsOnce sync.Once
	promGuardrailInstruments promGuardrailMetrics
)

func promGuardrailInstrumentsFor(mp metric.MeterProvider) promGuardrailMetrics {
	promGuardrailMetricsOnce.Do(func() {
		promGuardrailInstruments = newPromGuardrailMetrics(mp)
	})
	return promGuardrailInstruments
}

func newPromGuardrailMetrics(mp metric.MeterProvider) promGuardrailMetrics {
	meter := mp.Meter(promGuardrailMeterName)

	admitted, _ := meter.Int64Counter("mcp.prometheus_guardrail.admitted",
		metric.WithDescription("Number of Prometheus queries that passed every enabled guardrail check"),
		metric.WithUnit("{query}"),
	)
	wouldBlock, _ := meter.Int64Counter("mcp.prometheus_guardrail.would_block",
		metric.WithDescription("Number of Prometheus queries that failed a guardrail check in shadow mode and ran anyway"),
		metric.WithUnit("{query}"),
	)
	blocked, _ := meter.Int64Counter("mcp.prometheus_guardrail.blocked",
		metric.WithDescription("Number of Prometheus queries rejected by the guardrail in enforce mode"),
		metric.WithUnit("{query}"),
	)
	failOpen, _ := meter.Int64Counter("mcp.prometheus_guardrail.fail_open",
		metric.WithDescription("Number of Prometheus queries whose expression the guardrail could not parse and admitted"),
		metric.WithUnit("{query}"),
	)

	return promGuardrailMetrics{
		admitted:   admitted,
		wouldBlock: wouldBlock,
		blocked:    blocked,
		failOpen:   failOpen,
	}
}

func (m promGuardrailMetrics) recordAdmitted(ctx context.Context) {
	m.admitted.Add(ctx, 1)
}

// recordBlocked counts a query once, under the first check it failed.
func (m promGuardrailMetrics) recordBlocked(ctx context.Context, enforced bool, reason promGuardReason) {
	counter := m.wouldBlock
	if enforced {
		counter = m.blocked
	}
	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", string(reason))))
}

func (m promGuardrailMetrics) recordFailOpen(ctx context.Context, reason promGuardReason) {
	m.failOpen.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", string(reason))))
}
