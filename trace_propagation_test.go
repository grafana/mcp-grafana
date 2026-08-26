package mcpgrafana

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

const (
	callerTraceID     = "0af7651916cd43dd8448eb211c80319c"
	callerSpanID      = "b7ad6b7169203331"
	callerTraceparent = "00-" + callerTraceID + "-" + callerSpanID + "-01"
)

// withTraceContextPropagator installs the W3C propagator for the duration of a
// test, restoring whatever was configured before. Production code installs it
// in observability.Setup, which this package must not depend on.
func withTraceContextPropagator(t *testing.T) {
	t.Helper()
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
}

// callerContext returns a context carrying the caller's remote span context, as
// the otelhttp server handler would produce for an inbound traceparent.
func callerContext(t *testing.T) context.Context {
	t.Helper()
	traceID, err := trace.TraceIDFromHex(callerTraceID)
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex(callerSpanID)
	require.NoError(t, err)
	return trace.ContextWithRemoteSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	}))
}

// serveCapturingHeaders starts a test server that records the headers of the
// single request it receives.
func serveCapturingHeaders(t *testing.T) (*httptest.Server, *http.Header) {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func doRequest(t *testing.T, transport http.RoundTripper, ctx context.Context, url string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: transport}).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

func TestBuildTransportInjectsTraceContext(t *testing.T) {
	withTraceContextPropagator(t)

	srv, got := serveCapturingHeaders(t)
	transport, err := BuildTransport(&GrafanaConfig{}, nil)
	require.NoError(t, err)

	doRequest(t, transport, callerContext(t), srv.URL)

	traceparent := got.Get("traceparent")
	require.NotEmpty(t, traceparent, "outbound Grafana requests must carry a traceparent")
	assert.Contains(t, traceparent, callerTraceID, "the outbound request must continue the caller's trace")
}

func TestBuildTransportWithoutOtelDoesNotInjectTraceContext(t *testing.T) {
	withTraceContextPropagator(t)

	srv, got := serveCapturingHeaders(t)
	transport, err := BuildTransport(&GrafanaConfig{}, nil, WithoutOtel())
	require.NoError(t, err)

	doRequest(t, transport, callerContext(t), srv.URL)

	assert.Empty(t, got.Get("traceparent"))
}

// A traceparent copied from the incoming request via GRAFANA_FORWARD_HEADERS
// (surfaced as an extra header) names the caller's span. Letting it overwrite
// the injected value would re-parent Grafana's spans onto the caller and cut
// mcp-grafana out of the middle of the trace.
func TestExtraHeadersDoNotOverrideInjectedTraceContext(t *testing.T) {
	withTraceContextPropagator(t)

	forwarded := "00-11111111111111111111111111111111-2222222222222222-01"
	srv, got := serveCapturingHeaders(t)
	transport, err := BuildTransport(&GrafanaConfig{
		ExtraHeaders: map[string]string{
			"traceparent": forwarded,
			"X-Custom":    "kept",
		},
	}, nil)
	require.NoError(t, err)

	doRequest(t, transport, callerContext(t), srv.URL)

	assert.NotEqual(t, forwarded, got.Get("traceparent"))
	assert.Contains(t, got.Get("traceparent"), callerTraceID)
	assert.Equal(t, "kept", got.Get("X-Custom"), "non-propagation extra headers are unaffected")
}

// Without OTel injection there is nothing to preserve, so a forwarded
// traceparent is still applied — the behaviour operators relied on before the
// propagator existed.
func TestExtraHeadersSetTraceContextWhenNothingInjected(t *testing.T) {
	withTraceContextPropagator(t)

	forwarded := "00-11111111111111111111111111111111-2222222222222222-01"
	srv, got := serveCapturingHeaders(t)
	transport, err := BuildTransport(&GrafanaConfig{
		ExtraHeaders: map[string]string{"traceparent": forwarded},
	}, nil, WithoutOtel())
	require.NoError(t, err)

	doRequest(t, transport, callerContext(t), srv.URL)

	assert.Equal(t, forwarded, got.Get("traceparent"))
}

// Per-request extra headers from the context take the same path as the
// configured ones, so they must respect the injected trace context too.
func TestContextExtraHeadersDoNotOverrideInjectedTraceContext(t *testing.T) {
	withTraceContextPropagator(t)

	forwarded := "00-11111111111111111111111111111111-2222222222222222-01"
	srv, got := serveCapturingHeaders(t)
	transport, err := BuildTransport(&GrafanaConfig{}, nil)
	require.NoError(t, err)

	ctx := WithGrafanaConfig(callerContext(t), GrafanaConfig{
		ExtraHeaders: map[string]string{"Traceparent": forwarded},
	})
	doRequest(t, transport, ctx, srv.URL)

	assert.NotEqual(t, forwarded, got.Get("traceparent"))
	assert.Contains(t, got.Get("traceparent"), callerTraceID)
}

func TestPropagatedHeaderFields(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	fields := propagatedHeaderFields()
	assert.Contains(t, fields, "Traceparent", "field names must be canonicalised for header lookups")
	assert.Contains(t, fields, "Tracestate")
	assert.Contains(t, fields, "Baggage")

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	assert.Nil(t, propagatedHeaderFields(), "no propagator means nothing is ever injected")
}
