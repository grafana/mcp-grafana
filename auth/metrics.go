package auth

import (
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "mcp-grafana-auth"

// Metrics owns the OTel instruments used by the auth package.
type Metrics struct {
	active metric.Int64UpDownCounter
	login  metric.Float64Histogram

	mu     sync.Mutex
	counts map[Mode]int64
}

// NewMetrics constructs the instruments, swallowing errors (per OTel conventions
// when used in libraries — the alternative is a panic at process start, which
// is worse for an optional feature).
func NewMetrics() *Metrics {
	m := otel.GetMeterProvider().Meter(meterName)
	active, _ := m.Int64UpDownCounter("mcp_auth_session_active",
		metric.WithDescription("Active per-user auth sessions"),
		metric.WithUnit("{session}"),
	)
	login, _ := m.Float64Histogram("mcp_auth_login_duration_seconds",
		metric.WithDescription("End-to-end auth login flow duration"),
		metric.WithUnit("s"),
	)
	return &Metrics{active: active, login: login, counts: make(map[Mode]int64)}
}

func (m *Metrics) SessionCreated(mode Mode) {
	if m == nil {
		return
	}
	m.active.Add(nil, 1, metric.WithAttributes(attribute.String("mode", string(mode))))
}

func (m *Metrics) SessionRevoked(mode Mode) {
	if m == nil {
		return
	}
	m.active.Add(nil, -1, metric.WithAttributes(attribute.String("mode", string(mode))))
}

func (m *Metrics) LoginObserved(mode Mode, result string, durationSec float64) {
	if m == nil {
		return
	}
	m.login.Record(nil, durationSec, metric.WithAttributes(
		attribute.String("mode", string(mode)),
		attribute.String("result", result),
	))
}
