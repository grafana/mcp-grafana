package auth

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetrics_SessionActiveIncrementsAndDecrements(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)

	m := NewMetrics()
	m.SessionCreated(ModeOAuthOIDC)
	m.SessionCreated(ModeOAuthOIDC)
	m.SessionRevoked(ModeOAuthOIDC)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, mtr := range sm.Metrics {
			if mtr.Name == "mcp_auth_session_active" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("mcp_auth_session_active not exported")
	}
}
