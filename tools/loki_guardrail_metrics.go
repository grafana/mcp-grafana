package tools

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// lokiGuardrailMeterName is the OTel meter name for the Loki cost guardrail,
// matching the convention used by clientCacheMeterName/proxiedToolsMeterName.
const lokiGuardrailMeterName = "mcp-grafana"

// Guardrail block reason kinds, used as the `reason` attribute on the
// would_block/blocked counters. Deliberately a closed set: these become
// Prometheus labels downstream.
const (
	guardrailReasonSelector = "selector"
	guardrailReasonRange    = "range"
	guardrailReasonBytes    = "bytes"
)

// Guardrail fail-open causes, used as the `cause` attribute on the fail_open
// counter. Worth distinguishing: unparseable is a gap in the hand-rolled
// LogQL scanner (fix the scanner), estimate_failed is a Loki index/stats
// availability blip (fix nothing, watch the rate).
const (
	guardrailCauseUnparseable    = "unparseable"
	guardrailCauseEstimateFailed = "estimate_failed"
)

// Backend attribute values. The byte-budget check only runs on native Loki,
// so lumping the backends together would make the admitted count misleading.
const (
	guardrailBackendLoki         = "loki"
	guardrailBackendVictoriaLogs = "victorialogs"
	guardrailBackendUnknown      = "unknown"
)

// lokiGuardrailMetrics holds the OTel instruments for guardrail decisions.
// The four counters partition every guarded query exactly once, so
// admitted + would_block + blocked + fail_open is the total number of
// query_loki_logs calls the guardrail evaluated.
type lokiGuardrailMetrics struct {
	admitted   metric.Int64Counter // Passed every enabled check
	wouldBlock metric.Int64Counter // Failed a check in shadow mode; ran anyway
	blocked    metric.Int64Counter // Failed a check in enforce mode; rejected
	failOpen   metric.Int64Counter // Could not be evaluated; admitted
}

var (
	// lokiGuardrailMetricsOnce gates one-time construction of the guardrail
	// instruments. The guardrail has no constructor — it runs inside a tool
	// handler and reads its settings from the per-request GrafanaConfig — so
	// the instruments are built from the first request that reaches it and
	// reused for the life of the process. A process that hands different
	// requests different MeterProviders would therefore record everything
	// against the first one; no embedder does that today, and the
	// alternative (rebuilding instruments per request) would allocate on
	// every query. Vars rather than literals so tests can reset them.
	lokiGuardrailMetricsOnce sync.Once
	lokiGuardrailInstruments lokiGuardrailMetrics
)

// lokiGuardrailInstrumentsFor returns the process-wide guardrail instruments,
// building them against mp on first use. See lokiGuardrailMetricsOnce.
func lokiGuardrailInstrumentsFor(mp metric.MeterProvider) lokiGuardrailMetrics {
	lokiGuardrailMetricsOnce.Do(func() {
		lokiGuardrailInstruments = newLokiGuardrailMetrics(mp)
	})
	return lokiGuardrailInstruments
}

func newLokiGuardrailMetrics(mp metric.MeterProvider) lokiGuardrailMetrics {
	meter := mp.Meter(lokiGuardrailMeterName)

	admitted, _ := meter.Int64Counter("mcp.loki_guardrail.admitted",
		metric.WithDescription("Number of Loki queries that passed every enabled guardrail check"),
		metric.WithUnit("{query}"),
	)
	wouldBlock, _ := meter.Int64Counter("mcp.loki_guardrail.would_block",
		metric.WithDescription("Number of Loki queries that failed a guardrail check in shadow mode and ran anyway"),
		metric.WithUnit("{query}"),
	)
	blocked, _ := meter.Int64Counter("mcp.loki_guardrail.blocked",
		metric.WithDescription("Number of Loki queries rejected by the guardrail in enforce mode"),
		metric.WithUnit("{query}"),
	)
	failOpen, _ := meter.Int64Counter("mcp.loki_guardrail.fail_open",
		metric.WithDescription("Number of Loki queries the guardrail could not evaluate and admitted"),
		metric.WithUnit("{query}"),
	)

	return lokiGuardrailMetrics{
		admitted:   admitted,
		wouldBlock: wouldBlock,
		blocked:    blocked,
		failOpen:   failOpen,
	}
}

// recordAdmitted counts a query that passed every enabled check.
func (m lokiGuardrailMetrics) recordAdmitted(ctx context.Context, backend string) {
	m.admitted.Add(ctx, 1, metric.WithAttributes(attribute.String("backend", backend)))
}

// recordBlocked counts a query that failed a check. reason is the first
// check that tripped, so the counter counts queries rather than checks: a
// query failing both the selector and range checks is attributed to
// `selector`, which is the right answer for rollout purposes because raising
// the range budget would not admit it.
func (m lokiGuardrailMetrics) recordBlocked(ctx context.Context, enforced bool, backend, reason string) {
	counter := m.wouldBlock
	if enforced {
		counter = m.blocked
	}
	counter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("backend", backend),
		attribute.String("reason", reason),
	))
}

// recordFailOpen counts a query the guardrail admitted without reaching a
// verdict.
func (m lokiGuardrailMetrics) recordFailOpen(ctx context.Context, backend, cause string) {
	m.failOpen.Add(ctx, 1, metric.WithAttributes(
		attribute.String("backend", backend),
		attribute.String("cause", cause),
	))
}

// guardrailBackendLabel maps a backend to its bounded `backend` attribute
// value. A nil or unrecognized backend reports "unknown" rather than
// guessing, keeping the label set closed.
func guardrailBackendLabel(backend lokiBackend) string {
	switch backend.(type) {
	case *lokiNativeBackend:
		return guardrailBackendLoki
	case *victoriaLogsBackend:
		return guardrailBackendVictoriaLogs
	default:
		return guardrailBackendUnknown
	}
}
