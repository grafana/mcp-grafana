package rbac

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "mcp-grafana-rbac"

// Metrics holds the OTel instruments used by the RBAC subsystem.
type Metrics struct {
	filterDuration metric.Float64Histogram
	cacheHits      metric.Int64Counter
	cacheMisses    metric.Int64Counter
}

// NewMetrics builds the instruments. Errors are swallowed (library convention).
func NewMetrics() *Metrics {
	m := otel.GetMeterProvider().Meter(meterName)
	dur, _ := m.Float64Histogram("mcp_auth_rbac_filter_duration_seconds",
		metric.WithDescription("Time spent filtering tools/list per user"),
		metric.WithUnit("s"),
	)
	hits, _ := m.Int64Counter("mcp_auth_permission_cache_hit_total",
		metric.WithDescription("Per-session permission cache hits"),
	)
	misses, _ := m.Int64Counter("mcp_auth_permission_cache_miss_total",
		metric.WithDescription("Per-session permission cache misses"),
	)
	return &Metrics{filterDuration: dur, cacheHits: hits, cacheMisses: misses}
}

// FilterObserved records the duration of a tools/list filter call.
func (m *Metrics) FilterObserved(ctx context.Context, mode Mode, durationSec float64) {
	if m == nil {
		return
	}
	m.filterDuration.Record(ctx, durationSec, metric.WithAttributes(
		attribute.String("mode", string(mode)),
	))
}

// CacheHit increments the cache-hit counter.
func (m *Metrics) CacheHit(ctx context.Context) {
	if m != nil {
		m.cacheHits.Add(ctx, 1)
	}
}

// CacheMiss increments the cache-miss counter.
func (m *Metrics) CacheMiss(ctx context.Context) {
	if m != nil {
		m.cacheMisses.Add(ctx, 1)
	}
}

// Stopwatch returns a closure that records the filter duration when called.
// Used via defer stop() in the hook.
func (m *Metrics) Stopwatch(mode Mode) func() {
	if m == nil {
		return func() {}
	}
	start := time.Now()
	return func() { m.FilterObserved(context.Background(), mode, time.Since(start).Seconds()) }
}
