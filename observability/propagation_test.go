//go:build unit
// +build unit

package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// inboundTraceparent is a well-formed W3C traceparent used to stand in for a
// header injected by an upstream caller or proxy.
const (
	inboundTraceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	inboundTraceID     = "0af7651916cd43dd8448eb211c80319c"
	inboundSpanID      = "b7ad6b7169203331"
)

// restoreGlobalPropagator resets the global TextMapPropagator after a test, so
// that installing one here does not leak into unrelated tests.
func restoreGlobalPropagator(t *testing.T) {
	t.Helper()
	prev := otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
}

func TestSetupInstallsTextMapPropagator(t *testing.T) {
	restoreGlobalPropagator(t)
	// Start from the OTel default (no-op) so the assertions below can only
	// pass if Setup installed a propagator itself.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())

	_, err := Setup(Config{})
	require.NoError(t, err)

	prop := otel.GetTextMapPropagator()
	assert.Contains(t, prop.Fields(), "traceparent", "W3C trace context must be propagated by default")
	assert.Contains(t, prop.Fields(), "baggage", "baggage must be propagated by default")

	// Inbound: a traceparent header is extracted into the context.
	carrier := http.Header{}
	carrier.Set("traceparent", inboundTraceparent)
	ctx := prop.Extract(context.Background(), propagation.HeaderCarrier(carrier))
	sc := trace.SpanContextFromContext(ctx)
	require.True(t, sc.IsValid(), "inbound traceparent should yield a valid span context")
	assert.Equal(t, inboundTraceID, sc.TraceID().String())
	assert.Equal(t, inboundSpanID, sc.SpanID().String())
	assert.True(t, sc.IsRemote())

	// Outbound: the same context injects a traceparent for the next hop.
	out := http.Header{}
	prop.Inject(ctx, propagation.HeaderCarrier(out))
	assert.Equal(t, inboundTraceparent, out.Get("traceparent"))
}

func TestSetupHonoursOTELPropagatorsEnv(t *testing.T) {
	restoreGlobalPropagator(t)

	t.Run("b3", func(t *testing.T) {
		t.Setenv("OTEL_PROPAGATORS", "b3")

		_, err := Setup(Config{})
		require.NoError(t, err)

		fields := otel.GetTextMapPropagator().Fields()
		assert.Contains(t, fields, "x-b3-traceid")
		assert.NotContains(t, fields, "traceparent", "OTEL_PROPAGATORS should replace the default, not add to it")
	})

	t.Run("none", func(t *testing.T) {
		t.Setenv("OTEL_PROPAGATORS", "none")

		_, err := Setup(Config{})
		require.NoError(t, err)

		assert.Empty(t, otel.GetTextMapPropagator().Fields(), "propagation must be disabled by OTEL_PROPAGATORS=none")
	})
}

func TestWrapHandlerContinuesInboundTrace(t *testing.T) {
	restoreGlobalPropagator(t)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	var handlerSpanContext trace.SpanContext
	wrapped := WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerSpanContext = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}), "test-operation")

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("traceparent", inboundTraceparent)
	wrapped.ServeHTTP(httptest.NewRecorder(), req)

	require.True(t, handlerSpanContext.IsValid(), "handler should run under a span")
	assert.Equal(t, inboundTraceID, handlerSpanContext.TraceID().String(),
		"the server span must continue the caller's trace rather than start a new one")
}

func TestWrapHandlerWithoutPropagatorIgnoresInboundTrace(t *testing.T) {
	restoreGlobalPropagator(t)
	// The OTel default: propagation disabled. This is the pre-fix behaviour,
	// pinned here to document what the propagator installed by Setup buys us.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())

	var handlerSpanContext trace.SpanContext
	wrapped := WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerSpanContext = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}), "test-operation")

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("traceparent", inboundTraceparent)
	wrapped.ServeHTTP(httptest.NewRecorder(), req)

	assert.NotEqual(t, inboundTraceID, handlerSpanContext.TraceID().String())
}
