//go:build unit
// +build unit

package observability

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errBoom = errors.New("boom")

type failingHandler struct{}

func (failingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (failingHandler) Handle(context.Context, slog.Record) error { return errBoom }
func (f failingHandler) WithAttrs([]slog.Attr) slog.Handler       { return f }
func (f failingHandler) WithGroup(string) slog.Handler            { return f }

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
