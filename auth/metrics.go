package auth

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "mcp-grafana-auth"

// Metrics owns the OTel instruments used by the auth package.
type Metrics struct {
	active metric.Int64UpDownCounter
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
	return &Metrics{active: active}
}

func (m *Metrics) SessionCreated(ctx context.Context, mode Mode) {
	if m == nil {
		return
	}
	m.active.Add(ctx, 1, metric.WithAttributes(attribute.String("mode", string(mode))))
}

func (m *Metrics) SessionRevoked(ctx context.Context, mode Mode) {
	if m == nil {
		return
	}
	m.active.Add(ctx, -1, metric.WithAttributes(attribute.String("mode", string(mode))))
}
