// Package observability — log fan-out handler.
//
// fanoutHandler dispatches each slog record to every child handler so that
// the cmd/mcp-grafana binary can log to stderr AND export via OTLP at the
// same time, without either output losing fidelity.
package observability

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
)

type fanoutHandler struct {
	children []slog.Handler
}

func newFanoutHandler(children ...slog.Handler) *fanoutHandler {
	return &fanoutHandler{children: children}
}

func (f *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, c := range f.children {
		if c.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, c := range f.children {
		if !c.Enabled(ctx, r.Level) {
			continue
		}
		if err := c.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.children))
	for i, c := range f.children {
		next[i] = c.WithAttrs(attrs)
	}
	return &fanoutHandler{children: next}
}

func (f *fanoutHandler) WithGroup(name string) slog.Handler {
	// Per the slog.Handler contract, WithGroup("") must return the receiver.
	if name == "" {
		return f
	}
	next := make([]slog.Handler, len(f.children))
	for i, c := range f.children {
		next[i] = c.WithGroup(name)
	}
	return &fanoutHandler{children: next}
}

// setupLogging returns an OTLP LoggerProvider when either
// OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_LOGS_ENDPOINT is set,
// otherwise (nil, nil). The gRPC exporter itself respects the standard
// OTEL_EXPORTER_OTLP_* env vars (including the signal-specific
// OTEL_EXPORTER_OTLP_LOGS_* variants) for endpoint, headers, TLS, etc.
func setupLogging(ctx context.Context, res *sdkresource.Resource) (*sdklog.LoggerProvider, error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" &&
		os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT") == "" {
		return nil, nil
	}
	exporter, err := otlploggrpc.New(ctx)
	if err != nil {
		return nil, err
	}
	opts := []sdklog.LoggerProviderOption{
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	}
	if res != nil {
		opts = append(opts, sdklog.WithResource(res))
	}
	return sdklog.NewLoggerProvider(opts...), nil
}
