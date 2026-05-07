package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
)

// OTLPLogsEndpoint returns the resolved OTLP logs endpoint, preferring the
// signal-specific OTEL_EXPORTER_OTLP_LOGS_ENDPOINT over the generic
// OTEL_EXPORTER_OTLP_ENDPOINT. Returns "" when neither is set, which is the
// signal that OTLP log export is disabled.
func OTLPLogsEndpoint() string {
	if v := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"); v != "" {
		return v
	}
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
}

type fanoutHandler struct {
	children []slog.Handler
}

// NewFanoutHandler returns a slog.Handler that dispatches each record to every
// provided child handler. Safe for concurrent use once constructed.
func NewFanoutHandler(children ...slog.Handler) slog.Handler {
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
		// Clone per slog.Handler contract: "Copies of a Record share state.
		// Do not modify a Record after handing out a copy to it."
		if err := f.handleChild(ctx, c, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// handleChild dispatches to one child handler with panic isolation so a single
// buggy child (e.g. a misbehaving exporter or LogValuer) cannot crash the
// caller of slog.Info. Panics are converted to errors and aggregated with
// regular Handle errors.
func (f *fanoutHandler) handleChild(ctx context.Context, c slog.Handler, r slog.Record) (err error) {
	defer func() {
		if p := recover(); p != nil {
			// Write to stderr directly (not slog) so a panicking child cannot
			// re-enter itself via slog.Default(), and so the trace survives
			// slog discarding the returned error.
			fmt.Fprintf(os.Stderr, "fanout child panicked: %v\n%s", p, debug.Stack())
			err = fmt.Errorf("fanout child panicked: %v", p)
		}
	}()
	return c.Handle(ctx, r)
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Per the slog.Handler contract, WithAttrs with an empty slice returns the receiver.
	if len(attrs) == 0 {
		return f
	}
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

// setupLogging returns an OTLP LoggerProvider when OTLPLogsEndpoint() is set,
// otherwise (nil, nil). The gRPC exporter respects the standard OTEL_* env vars
// (including the signal-specific OTEL_EXPORTER_OTLP_LOGS_* variants) for
// endpoint, headers, TLS, etc.
func setupLogging(ctx context.Context, res *sdkresource.Resource) (*sdklog.LoggerProvider, error) {
	if OTLPLogsEndpoint() == "" {
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
