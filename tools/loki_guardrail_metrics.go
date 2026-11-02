package tools

import (
	"context"
	"sync"

	"github.com/grafana/mcp-grafana/lokiguard"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// lokiGuardrailMeterName is the OTel meter name for the Loki cost guardrail,
// matching the convention used by clientCacheMeterName/proxiedToolsMeterName.
const lokiGuardrailMeterName = "mcp-grafana"

// The `reason` attribute on the would_block/blocked counters is a
// lokiguard.Kind (selector, range, bytes) and the `cause` attribute on the
// fail_open counter is a lokiguard.Cause (unparseable, estimate_failed). Both
// are closed sets because they become Prometheus labels downstream. With
// --loki-guardrail-on-estimate-failure=block, a Cause is also used as the
// `reason` of the blocked/would_block counters.

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

	// estimatedBytes records the index/stats estimate for every query that
	// has one, including blocked queries, so the distribution can drive the
	// default budget.
	estimatedBytes metric.Int64Histogram
}

// estimatedBytesBuckets are the explicit histogram bucket bounds for
// mcp.loki_guardrail.estimated_bytes: powers of four from 1 MiB to 4 TiB plus
// 10 TiB, covering a cheap query to one that would scan a whole large tenant.
var estimatedBytesBuckets = []float64{
	1 << 20, 4 << 20, 16 << 20, 64 << 20, 256 << 20,
	1 << 30, 4 << 30, 16 << 30, 64 << 30, 256 << 30,
	1 << 40, 4 << 40, 10 << 40,
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

	estimatedBytes, _ := meter.Int64Histogram("mcp.loki_guardrail.estimated_bytes",
		metric.WithDescription("Loki index/stats byte estimate for queries the guardrail evaluated, by verdict"),
		metric.WithUnit("By"),
		metric.WithExplicitBucketBoundaries(estimatedBytesBuckets...),
	)

	return lokiGuardrailMetrics{
		admitted:       admitted,
		wouldBlock:     wouldBlock,
		blocked:        blocked,
		failOpen:       failOpen,
		estimatedBytes: estimatedBytes,
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

// recordEstimatedBytes records a query's index/stats byte estimate, labelled
// with the verdict (allow, block, unknown) so the size distribution of blocked
// queries is visible next to the admitted ones.
func (m lokiGuardrailMetrics) recordEstimatedBytes(ctx context.Context, backend string, verdict lokiguard.Verdict, bytes int64) {
	m.estimatedBytes.Record(ctx, bytes, metric.WithAttributes(
		attribute.String("backend", backend),
		attribute.String("verdict", verdict.String()),
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
