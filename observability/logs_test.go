//go:build unit
// +build unit

package observability

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

var errBoom = errors.New("boom")

type failingHandler struct{}

func (failingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (failingHandler) Handle(context.Context, slog.Record) error { return errBoom }
func (f failingHandler) WithAttrs([]slog.Attr) slog.Handler      { return f }
func (f failingHandler) WithGroup(string) slog.Handler           { return f }

func TestFanoutHandler_DispatchesToAllChildren(t *testing.T) {
	var bufA, bufB bytes.Buffer
	h := NewFanoutHandler(
		slog.NewTextHandler(&bufA, &slog.HandlerOptions{Level: slog.LevelInfo}),
		slog.NewTextHandler(&bufB, &slog.HandlerOptions{Level: slog.LevelInfo}),
	)

	logger := slog.New(h)
	logger.Info("hello world", "k", "v")

	assert.Contains(t, bufA.String(), "hello world")
	assert.Contains(t, bufA.String(), "k=v")
	assert.Contains(t, bufB.String(), "hello world")
	assert.Contains(t, bufB.String(), "k=v")
}

func TestFanoutHandler_EnabledIfAnyChildEnabled(t *testing.T) {
	var bufDebug, bufError bytes.Buffer
	h := NewFanoutHandler(
		slog.NewTextHandler(&bufDebug, &slog.HandlerOptions{Level: slog.LevelDebug}),
		slog.NewTextHandler(&bufError, &slog.HandlerOptions{Level: slog.LevelError}),
	)

	assert.True(t, h.Enabled(context.Background(), slog.LevelInfo))
}

func TestFanoutHandler_WithAttrsPropagatesToChildren(t *testing.T) {
	var buf bytes.Buffer
	h := NewFanoutHandler(
		slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}),
	).WithAttrs([]slog.Attr{slog.String("service", "mcp-grafana")})

	slog.New(h).Info("msg")
	assert.Contains(t, buf.String(), "service=mcp-grafana")
}

func TestFanoutHandler_WithGroupPropagatesToChildren(t *testing.T) {
	var buf bytes.Buffer
	h := NewFanoutHandler(
		slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}),
	).WithGroup("grp")

	slog.New(h).Info("msg", "k", "v")
	assert.Contains(t, buf.String(), "grp.k=v")
}

func TestFanoutHandler_AggregatesErrors(t *testing.T) {
	h := NewFanoutHandler(failingHandler{}, failingHandler{})
	err := h.Handle(context.Background(), slog.Record{})
	require.Error(t, err)
	assert.Equal(t, 2, strings.Count(err.Error(), "boom"))
}

func TestFanoutHandler_ZeroChildren(t *testing.T) {
	h := NewFanoutHandler()
	assert.False(t, h.Enabled(context.Background(), slog.LevelInfo))
	assert.NoError(t, h.Handle(context.Background(), slog.Record{}))
	require.NotNil(t, h.WithAttrs(nil))
	require.NotNil(t, h.WithGroup("grp"))
}

func TestFanoutHandler_WithGroupEmptyNameReturnsReceiver(t *testing.T) {
	var buf bytes.Buffer
	h := NewFanoutHandler(slog.NewTextHandler(&buf, nil))
	// WithGroup("") must return the receiver unchanged (contract of slog.Handler).
	// Compare the underlying pointer identity.
	same := h.WithGroup("")
	assert.Equal(t, reflect.ValueOf(h).Pointer(), reflect.ValueOf(same).Pointer())
}

func TestFanoutHandler_WithAttrsEmptyReturnsReceiver(t *testing.T) {
	var buf bytes.Buffer
	h := NewFanoutHandler(slog.NewTextHandler(&buf, nil))
	// WithAttrs on an empty slice must return the receiver (contract of slog.Handler).
	sameNil := h.WithAttrs(nil)
	sameEmpty := h.WithAttrs([]slog.Attr{})
	assert.Equal(t, reflect.ValueOf(h).Pointer(), reflect.ValueOf(sameNil).Pointer())
	assert.Equal(t, reflect.ValueOf(h).Pointer(), reflect.ValueOf(sameEmpty).Pointer())
}

func TestFanoutHandler_EnabledFalseWhenAllChildrenDisabled(t *testing.T) {
	var bufA, bufB bytes.Buffer
	h := NewFanoutHandler(
		slog.NewTextHandler(&bufA, &slog.HandlerOptions{Level: slog.LevelError}),
		slog.NewTextHandler(&bufB, &slog.HandlerOptions{Level: slog.LevelError}),
	)
	assert.False(t, h.Enabled(context.Background(), slog.LevelDebug))
}

func TestSetupLogging_DisabledWhenEnvUnset(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	lp, err := setupLogging(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, lp)
}

func TestSetupLogging_EnabledWhenEnvSet(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	lp, err := setupLogging(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, lp)

	// Shutdown should succeed even if no collector is actually running.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	assert.NoError(t, lp.Shutdown(shutdownCtx))
}

func TestSetupLogging_EnabledWhenLogsEndpointSet(t *testing.T) {
	// Clear the generic endpoint so only the signal-specific variable is active;
	// verifies gating honors OTEL_EXPORTER_OTLP_LOGS_ENDPOINT as a standalone trigger.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://localhost:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE", "true")
	lp, err := setupLogging(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, lp)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	assert.NoError(t, lp.Shutdown(shutdownCtx))
}

// memExporter is an in-process sdklog.Exporter that captures records so tests
// can assert end-to-end that slog output actually reaches the OTLP bridge.
type memExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *memExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range records {
		e.records = append(e.records, r.Clone())
	}
	return nil
}

func (e *memExporter) ForceFlush(context.Context) error { return nil }
func (e *memExporter) Shutdown(context.Context) error   { return nil }

func (e *memExporter) bodies() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.records))
	for i, r := range e.records {
		out[i] = r.Body().AsString()
	}
	return out
}

// TestFanoutHandler_RoundTripToOTLPExporter guards the end-to-end invariant:
// slog → NewFanoutHandler(stderr, otelslog.NewHandler(lp)) → LoggerProvider →
// exporter. If any wiring step breaks, slog records stop reaching OTel.
func TestFanoutHandler_RoundTripToOTLPExporter(t *testing.T) {
	exp := &memExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	t.Cleanup(func() { _ = lp.Shutdown(context.Background()) })

	var stderr bytes.Buffer
	stderrHandler := slog.NewTextHandler(&stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	otlpHandler := otelslog.NewHandler("mcp-grafana", otelslog.WithLoggerProvider(lp))

	logger := slog.New(NewFanoutHandler(stderrHandler, otlpHandler))
	logger.Info("round-trip", "k", "v")

	require.NoError(t, lp.ForceFlush(context.Background()))

	bodies := exp.bodies()
	require.Len(t, bodies, 1, "OTLP exporter did not receive the record")
	assert.Equal(t, "round-trip", bodies[0])
	assert.Contains(t, stderr.String(), "round-trip")
	assert.Contains(t, stderr.String(), "k=v")
}

func TestFanoutHandler_HandleSkipsDisabledChildren(t *testing.T) {
	var bufDebug, bufError bytes.Buffer
	h := NewFanoutHandler(
		slog.NewTextHandler(&bufDebug, &slog.HandlerOptions{Level: slog.LevelDebug}),
		slog.NewTextHandler(&bufError, &slog.HandlerOptions{Level: slog.LevelError}),
	)

	// Info is below Error threshold — only the debug child should receive.
	slog.New(h).Info("hello", "k", "v")
	assert.Contains(t, bufDebug.String(), "hello")
	assert.Empty(t, bufError.String())
}

// panickingHandler always panics from Handle. Used to verify that
// fanoutHandler.Handle recovers per-child panics, converts them to errors,
// and still delivers the record to healthy siblings.
type panickingHandler struct{}

func (panickingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (panickingHandler) Handle(context.Context, slog.Record) error { panic("boom from child") }
func (h panickingHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h panickingHandler) WithGroup(string) slog.Handler           { return h }

// TestFanoutHandler_RecoversChildPanic verifies two invariants of the
// panic-isolation contract in fanoutHandler.Handle:
//  1. a panic in one child is converted to an error (never re-raised), and
//  2. other children still receive the record.
//
// A regression that removed the defer-recover would crash this test with a
// panic instead of a clean Error.
func TestFanoutHandler_RecoversChildPanic(t *testing.T) {
	var buf bytes.Buffer
	healthy := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	h := NewFanoutHandler(panickingHandler{}, healthy)

	// Call Handle directly so we can observe the returned error (slog.Logger
	// discards it).
	err := h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0))
	require.Error(t, err, "panic should be converted to an error")
	assert.Contains(t, err.Error(), "panicked")
	assert.Contains(t, buf.String(), "msg", "healthy child must still receive record despite sibling panic")
}

// TestFanoutHandler_SlogInfoSurvivesChildPanic is the user-facing guarantee:
// application code calling slog.Info must never panic because of a buggy
// handler. slog discards the handler's returned error, so this test also
// verifies that the healthy child still writes the record.
func TestFanoutHandler_SlogInfoSurvivesChildPanic(t *testing.T) {
	var buf bytes.Buffer
	healthy := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	h := NewFanoutHandler(panickingHandler{}, healthy)
	logger := slog.New(h)

	assert.NotPanics(t, func() {
		logger.Info("critical business event", "key", "value")
	})
	assert.Contains(t, buf.String(), "critical business event")
	assert.Contains(t, buf.String(), "key=value")
}

// TestOTLPLogsEndpoint_LogsEndpointTakesPrecedence verifies that when both the
// signal-specific and generic OTEL endpoint env vars are set, OTLPLogsEndpoint
// returns the signal-specific value. A branch-swap regression would pass every
// other test in this suite, so this precedence case is covered directly.
func TestOTLPLogsEndpoint_LogsEndpointTakesPrecedence(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://generic:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://logs-specific:4317")
	assert.Equal(t, "http://logs-specific:4317", OTLPLogsEndpoint())
}

// TestOTLPLogsEndpoint_FallsBackToGeneric verifies the fallback path: when the
// signal-specific env var is empty, the generic endpoint is used.
func TestOTLPLogsEndpoint_FallsBackToGeneric(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://generic:4317")
	assert.Equal(t, "http://generic:4317", OTLPLogsEndpoint())
}

// TestOTLPLogsEndpoint_EmptyWhenNeitherSet verifies the "disabled" signal: when
// neither env var is set, OTLPLogsEndpoint returns "" so setupLogging can
// short-circuit without creating an exporter.
func TestOTLPLogsEndpoint_EmptyWhenNeitherSet(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	assert.Empty(t, OTLPLogsEndpoint())
}

// TestFanoutHandler_PanicWritesStackToStderr verifies that handleChild writes
// the panic message AND a stack trace directly to os.Stderr before returning
// the error. slog.Logger discards errors returned from Handle, so without this
// stderr write panic evidence would be lost entirely in production.
//
// We capture os.Stderr by redirecting it through a pipe for the duration of
// the test — the production code writes to os.Stderr directly (not an injected
// writer), so swapping the package-level var is the cleanest way to observe it.
//
// Do not t.Parallel() this test or any sibling that writes to stderr: the
// os.Stderr swap is process-global and would interleave with concurrent writers.
func TestFanoutHandler_PanicWritesStackToStderr(t *testing.T) {
	// Capture os.Stderr by redirecting to a pipe.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	h := NewFanoutHandler(panickingHandler{})
	// Drive a log through — slog will discard the returned error but handleChild
	// must still have written the panic+stack to stderr before returning.
	slog.New(h).Info("trigger")

	// Close the writer so the read completes, then read everything captured.
	require.NoError(t, w.Close())
	var captured bytes.Buffer
	_, _ = io.Copy(&captured, r)

	got := captured.String()
	assert.Contains(t, got, "fanout child panicked")
	// Stack trace should include a function name; we just assert it's non-trivial
	// (a bare "panicked: X" line is ~30 chars; a stack is hundreds).
	assert.Greater(t, len(got), 100, "expected stack trace, got: %q", got)
}

// NOTE: Coverage gap — observability.go's otel.SetErrorHandler callback is not
// directly unit-tested. otel.SetErrorHandler is a process-global side effect
// guarded by sync.Once, so once any test (or production bootstrap) sets it, it
// cannot be reliably reset for another test. The production behavior (write
// directly to os.Stderr rather than via slog.Default()) is defensive against a
// feedback loop and is simple enough that the failure mode — recursion — would
// surface as a stack blow-up in manual / integration testing.
