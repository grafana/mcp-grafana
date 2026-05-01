# OTLP Log Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When `OTEL_EXPORTER_OTLP_ENDPOINT` is set, `cmd/mcp-grafana` exports structured logs via OTLP in addition to the existing stderr text output, with `trace_id`/`span_id` auto-attached via the `otelslog` bridge.

**Architecture:**
- Add a `LoggerProvider` alongside the existing `TracerProvider` in `observability.Setup`, gated on the same `OTEL_EXPORTER_OTLP_ENDPOINT` env var.
- In `cmd/mcp-grafana/main.go`, install a `slog.Handler` that fans out to the existing stderr `TextHandler` (unchanged) AND the OTLP handler from `otelslog`. Fan-out is additive — stderr output is preserved exactly as today (@sd2k's ask).
- Shut the `LoggerProvider` down in the same `defer` block as the existing `TracerProvider`.

**Tech Stack:**
- `go.opentelemetry.io/contrib/bridges/otelslog` — slog → OTel log bridge
- `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc` — OTLP/gRPC log exporter (matches existing `otlptracegrpc`)
- `go.opentelemetry.io/otel/sdk/log` — OTel log SDK (`LoggerProvider`, `BatchProcessor`)
- Standard library `log/slog` for fan-out via a custom `slog.Handler`

---

## File Structure

**New:**
- `observability/logs.go` — `setupLogging(ctx, cfg, res)` returns a `*sdklog.LoggerProvider` (or nil) gated on `OTEL_EXPORTER_OTLP_ENDPOINT`; `fanoutHandler` type that implements `slog.Handler` and dispatches to N child handlers.
- `observability/logs_test.go` — unit tests (build-tag `unit`) for `fanoutHandler` and for env-var gating of `setupLogging`.

**Modified:**
- `observability/observability.go` — extend `Observability` struct with `loggerProvider *sdklog.LoggerProvider`; call `setupLogging` from `Setup`; shut down in `Shutdown`.
- `cmd/mcp-grafana/main.go` — replace the single-handler `slog.SetDefault` call with one that installs the fan-out handler (stderr text + OTLP) when OTLP is configured; fall back to stderr-only otherwise. Do this AFTER `observability.Setup` so the logger provider exists.
- `go.mod` / `go.sum` — `go get` the three new OTel deps at versions matching the existing `otlptracegrpc v1.42.0` / `otel v1.43.0` majors.
- `README.md` — add a "Logs" subsection under "Observability"; update the `## Observability` lead sentence; add a local + Grafana Cloud example; add the env var to the Docker example.
- `docs/sources/developer/observability-metrics-and-tracing.md` — rename page or add a "Logs" section; mirror README changes; update page title/description/keywords.
- `CHANGELOG.md` — `Added` entry under a new `[Unreleased]` section.

---

### Task 1: Add OTel log SDK dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Fetch the three new OTel log packages**

Run:

```bash
go get go.opentelemetry.io/otel/sdk/log
go get go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc
go get go.opentelemetry.io/contrib/bridges/otelslog
```

Note: `sdk/log` and `otlploggrpc` live on a separate `v0.x` track from the rest of the SDK. Pick whatever `go get` + `go mod tidy` settles on; verify with `go build ./...` before proceeding.

- [ ] **Step 2: Tidy and verify build**

Run:

```bash
go mod tidy
go build ./...
```

Expected: clean build, `go.mod` contains the three new direct dependencies.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add OTel log SDK, otlploggrpc exporter, and otelslog bridge"
```

---

### Task 2: Fan-out `slog.Handler` — failing test

**Files:**
- Create: `observability/logs_test.go`

- [ ] **Step 1: Write the failing test**

```go
//go:build unit
// +build unit

package observability

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

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
	h := newFanoutHandler(
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
	h := newFanoutHandler(
		slog.NewTextHandler(&bufDebug, &slog.HandlerOptions{Level: slog.LevelDebug}),
		slog.NewTextHandler(&bufError, &slog.HandlerOptions{Level: slog.LevelError}),
	)

	assert.True(t, h.Enabled(context.Background(), slog.LevelInfo))
}

func TestFanoutHandler_WithAttrsPropagatesToChildren(t *testing.T) {
	var buf bytes.Buffer
	h := newFanoutHandler(
		slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}),
	).WithAttrs([]slog.Attr{slog.String("service", "mcp-grafana")})

	slog.New(h).Info("msg")
	assert.Contains(t, buf.String(), "service=mcp-grafana")
}

func TestFanoutHandler_WithGroupPropagatesToChildren(t *testing.T) {
	var buf bytes.Buffer
	h := newFanoutHandler(
		slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}),
	).WithGroup("grp")

	slog.New(h).Info("msg", "k", "v")
	assert.Contains(t, buf.String(), "grp.k=v")
}

func TestFanoutHandler_AggregatesErrors(t *testing.T) {
	h := newFanoutHandler(failingHandler{}, failingHandler{})
	err := h.Handle(context.Background(), slog.Record{})
	require.Error(t, err)
	assert.Equal(t, 2, strings.Count(err.Error(), "boom"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test -v -tags unit ./observability/ -run TestFanoutHandler
```

Expected: compile failure — `newFanoutHandler` undefined.

---

### Task 3: Fan-out `slog.Handler` — implementation

**Files:**
- Create: `observability/logs.go`

- [ ] **Step 1: Write the implementation**

```go
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
	next := make([]slog.Handler, len(f.children))
	for i, c := range f.children {
		next[i] = c.WithGroup(name)
	}
	return &fanoutHandler{children: next}
}
```

- [ ] **Step 2: Run the tests — expect all pass**

Run:

```bash
go test -v -tags unit ./observability/ -run TestFanoutHandler
```

Expected: 5 PASS.

- [ ] **Step 3: Commit**

```bash
git add observability/logs.go observability/logs_test.go
git commit -m "feat(observability): add fan-out slog.Handler"
```

---

### Task 4: `setupLogging` — failing test

**Files:**
- Modify: `observability/logs_test.go`

- [ ] **Step 1: Append the following test cases**

```go
func TestSetupLogging_DisabledWhenEnvUnset(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
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
```

Add `"time"` to the imports.

- [ ] **Step 2: Run the tests — expect compile failure**

Run:

```bash
go test -v -tags unit ./observability/ -run TestSetupLogging
```

Expected: `setupLogging` undefined.

---

### Task 5: `setupLogging` — implementation

**Files:**
- Modify: `observability/logs.go`

- [ ] **Step 1: Append imports and `setupLogging` function**

Add imports to `observability/logs.go`:

```go
"os"

"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
sdklog "go.opentelemetry.io/otel/sdk/log"
sdkresource "go.opentelemetry.io/otel/sdk/resource"
```

Append function:

```go
// setupLogging returns an OTLP LoggerProvider when OTEL_EXPORTER_OTLP_ENDPOINT
// is set, otherwise (nil, nil). The gRPC exporter respects the standard
// OTEL_EXPORTER_OTLP_* env vars for endpoint, headers, TLS, etc.
func setupLogging(ctx context.Context, res *sdkresource.Resource) (*sdklog.LoggerProvider, error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
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
```

- [ ] **Step 2: Run the tests — expect pass**

Run:

```bash
go test -v -tags unit ./observability/ -run TestSetupLogging
```

Expected: 2 PASS.

- [ ] **Step 3: Commit**

```bash
git add observability/logs.go observability/logs_test.go
git commit -m "feat(observability): add setupLogging with OTLP log exporter"
```

---

### Task 6: Wire `LoggerProvider` into `Observability`

**Files:**
- Modify: `observability/observability.go`

- [ ] **Step 1: Add the field and setup call**

Add import near the existing sdk imports:

```go
sdklog "go.opentelemetry.io/otel/sdk/log"
```

Add field to the `Observability` struct (alongside `tracerProvider`):

```go
	loggerProvider *sdklog.LoggerProvider
```

Inside `Setup`, after the trace-exporter block (after line 119 — the `obs.tracerProvider = tp` line — but before the `if !cfg.MetricsEnabled` early return), add:

```go
	// Set up OTLP log exporter when OTEL_EXPORTER_OTLP_ENDPOINT is configured.
	// The gRPC exporter respects standard OTEL_* env vars for endpoint, headers,
	// TLS (OTEL_EXPORTER_OTLP_INSECURE), etc. See cmd/mcp-grafana/main.go for
	// how the slog.Default handler is wired on top of this provider.
	lp, logErr := setupLogging(context.Background(), res)
	if logErr != nil {
		return nil, logErr
	}
	obs.loggerProvider = lp
```

- [ ] **Step 2: Extend `Shutdown` to include the logger provider**

Replace the current `Shutdown` body with:

```go
func (o *Observability) Shutdown(ctx context.Context) error {
	var errs []error
	if o.tracerProvider != nil {
		if err := o.tracerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if o.loggerProvider != nil {
		if err := o.loggerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if o.meterProvider != nil {
		if err := o.meterProvider.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
```

Add `"errors"` import if not already present.

- [ ] **Step 3: Add a getter so `main.go` can access the provider**

Append to `observability/observability.go`:

```go
// LoggerProvider returns the OTLP log provider, or nil if OTLP logging is
// not configured (OTEL_EXPORTER_OTLP_ENDPOINT not set).
func (o *Observability) LoggerProvider() *sdklog.LoggerProvider {
	return o.loggerProvider
}
```

- [ ] **Step 4: Run full observability unit tests**

Run:

```bash
go test -v -tags unit ./observability/
```

Expected: all existing tests still pass; no regressions.

- [ ] **Step 5: Commit**

```bash
git add observability/observability.go
git commit -m "feat(observability): wire LoggerProvider into Setup and Shutdown"
```

---

### Task 7: Install fan-out handler in `cmd/mcp-grafana/main.go`

**Files:**
- Modify: `cmd/mcp-grafana/main.go` (function `run`, lines 308–322)

- [ ] **Step 1: Add import**

Add to the import block:

```go
"go.opentelemetry.io/contrib/bridges/otelslog"
```

- [ ] **Step 2: Replace the existing `slog.SetDefault` + observability setup block**

Find this block (currently lines 308–322):

```go
func run(transport, addr, basePath, endpointPath string, logLevel slog.Level, dt disabledTools, gc mcpgrafana.GrafanaConfig, tls tlsConfig, obs observability.Config, sessionIdleTimeoutMinutes int) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	// Set up observability (metrics and tracing)
	o, err := observability.Setup(obs)
	if err != nil {
		return fmt.Errorf("failed to setup observability: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := o.Shutdown(shutdownCtx); err != nil {
			slog.Error("failed to shutdown observability", "error", err)
		}
	}()
```

Replace with:

```go
func run(transport, addr, basePath, endpointPath string, logLevel slog.Level, dt disabledTools, gc mcpgrafana.GrafanaConfig, tls tlsConfig, obs observability.Config, sessionIdleTimeoutMinutes int) error {
	// Install stderr logging first so any errors from observability.Setup
	// are captured even if OTLP logging can't be initialized.
	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(stderrHandler))

	// Set up observability (metrics, tracing, and logs)
	o, err := observability.Setup(obs)
	if err != nil {
		return fmt.Errorf("failed to setup observability: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := o.Shutdown(shutdownCtx); err != nil {
			slog.Error("failed to shutdown observability", "error", err)
		}
	}()

	// If the OTLP log exporter is configured, fan-out slog records to both
	// stderr (unchanged behavior) AND OTLP via the otelslog bridge. The bridge
	// attaches trace_id / span_id from context automatically, so log records
	// correlate with the spans that mcp-grafana already emits.
	if lp := o.LoggerProvider(); lp != nil {
		otlpHandler := otelslog.NewHandler("mcp-grafana", otelslog.WithLoggerProvider(lp))
		slog.SetDefault(slog.New(observability.NewFanoutHandler(stderrHandler, otlpHandler)))
		slog.Info("OTLP log export enabled", "endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
```

- [ ] **Step 3: Export `newFanoutHandler` as `NewFanoutHandler`**

Because `cmd/mcp-grafana/main.go` lives in a different package, rename the constructor in `observability/logs.go` from `newFanoutHandler` to `NewFanoutHandler`. Update `observability/logs_test.go` callsites to match.

- [ ] **Step 4: Build and run unit tests**

Run:

```bash
go build ./...
go test -v -tags unit ./...
```

Expected: clean build, all unit tests pass.

- [ ] **Step 5: Smoke test — no OTLP endpoint**

Run:

```bash
go run ./cmd/mcp-grafana --help >/dev/null
```

Expected: exits 0, no panics. (No OTLP env var set, so stderr-only path is exercised.)

- [ ] **Step 6: Commit**

```bash
git add cmd/mcp-grafana/main.go observability/logs.go observability/logs_test.go
git commit -m "feat: export logs via OTLP when OTEL_EXPORTER_OTLP_ENDPOINT is set"
```

---

### Task 8: README — add "Logs" section under Observability

**Files:**
- Modify: `README.md` (around lines 946–1000)

- [ ] **Step 1: Update the lead sentence under `### Observability`**

Replace line 948:

```markdown
The MCP server supports Prometheus metrics and OpenTelemetry distributed tracing, following the [OTel MCP semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).
```

With:

```markdown
The MCP server supports Prometheus metrics, OpenTelemetry distributed tracing, and OpenTelemetry log export, following the [OTel MCP semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/). Tracing and log export are configured via standard `OTEL_*` environment variables and work with any transport.
```

- [ ] **Step 2: Insert a `#### Logs` subsection immediately after the `#### Tracing` section (after line 988, before `**Docker example...`)**

Insert:

````markdown
#### Logs

When `OTEL_EXPORTER_OTLP_ENDPOINT` is set — the same trigger that enables tracing — the server also exports structured logs via OTLP/gRPC in addition to the existing plain-text stderr output. The `otelslog` bridge automatically attaches `trace_id` and `span_id` from the active span, so log records correlate with the traces the server already emits.

Stderr logging is unchanged when OTLP logging is enabled; you can continue to rely on container logs or pipe stderr to `/dev/null` if you prefer.

```bash
# Send both logs and traces to a local OTel collector
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 \
OTEL_EXPORTER_OTLP_INSECURE=true \
./mcp-grafana -t streamable-http

# Send logs and traces to Grafana Cloud with authentication
OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-prod-us-central-0.grafana.net/otlp \
OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic ..." \
./mcp-grafana -t streamable-http
```

Logs are also exported under the stdio transport, which makes it easy to centralize logs from local `mcp-grafana` instances invoked by IDE clients.
````

- [ ] **Step 3: Verify the rendered markdown**

Run:

```bash
grep -n "#### Logs\|#### Tracing\|#### Metrics" README.md
```

Expected: three matches in the order Metrics → Tracing → Logs.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs(readme): document OTLP log export"
```

---

### Task 9: Developer docs — add "Logs" section

**Files:**
- Modify: `docs/sources/developer/observability-metrics-and-tracing.md`

- [ ] **Step 1: Update frontmatter**

Replace lines 1–13 of the file with:

```markdown
---
title: Observability (metrics, tracing, and logs)
menuTitle: Observability
description: Expose Prometheus metrics and OpenTelemetry tracing and logs from the Grafana MCP server.
keywords:
  - Prometheus
  - metrics
  - OpenTelemetry
  - tracing
  - logs
  - MCP
weight: 2
aliases: []
---
```

- [ ] **Step 2: Update `# Observability` heading and intro (lines 15–19)**

Replace:

```markdown
# Observability (metrics and tracing)

The MCP server can expose **Prometheus metrics** and supports **[OpenTelemetry](https://opentelemetry.io/)** distributed tracing, following the [OTel MCP semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).

Metrics require the **SSE** or **streamable-http** transport. Tracing uses standard `OTEL_*` environment variables and works independently of `--metrics`.
```

With:

```markdown
# Observability (metrics, tracing, and logs)

The MCP server can expose **Prometheus metrics** and supports **[OpenTelemetry](https://opentelemetry.io/)** distributed tracing and log export, following the [OTel MCP semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).

Metrics require the **SSE** or **streamable-http** transport. Tracing and log export use standard `OTEL_*` environment variables and work with any transport, independently of `--metrics`.
```

- [ ] **Step 3: Insert `## Enable OpenTelemetry logs` section between the tracing section and the Docker section (after line 73, before `## Run with Docker`)**

Insert:

````markdown
## Enable OpenTelemetry logs

When `OTEL_EXPORTER_OTLP_ENDPOINT` is set — the same trigger as tracing — the server also exports structured logs via OTLP/gRPC in addition to the existing plain-text stderr output. Logs carry `trace_id` and `span_id` from the active span so they correlate with exported traces.

```bash
# Send logs and traces to a local OTel collector
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 \
OTEL_EXPORTER_OTLP_INSECURE=true \
./mcp-grafana -t streamable-http
```

Stderr logging continues unchanged; operators can pipe stderr to `/dev/null` if they only want logs going to the OTel collector.

````

- [ ] **Step 4: Commit**

```bash
git add docs/sources/developer/observability-metrics-and-tracing.md
git commit -m "docs: document OTLP log export in developer observability guide"
```

---

### Task 10: CHANGELOG entry

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add `[Unreleased]` section**

Insert directly below line 6 (the `and this project adheres to Semantic Versioning` line), before `## [0.13.1]`:

```markdown

## [Unreleased]

### Added

- Export logs via OTLP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, consistent with existing OTLP trace export. Stderr logging is preserved. ([#811](https://github.com/grafana/mcp-grafana/issues/811))
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): add OTLP log export entry"
```

---

### Task 11: End-to-end smoke test

**Files:** (no code changes)

- [ ] **Step 1: Start a local OTel collector**

If the repo already has a docker-compose collector, use it. Otherwise, run:

```bash
docker run --rm -p 4317:4317 -p 4318:4318 otel/opentelemetry-collector:latest \
  --config=/etc/otelcol/config.yaml
```

Expected: collector listens on 4317 and logs received telemetry to its own stdout.

- [ ] **Step 2: Run `mcp-grafana` with OTLP enabled**

Run:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 \
OTEL_EXPORTER_OTLP_INSECURE=true \
GRAFANA_URL=http://localhost:3000 \
go run ./cmd/mcp-grafana --transport streamable-http --log-level debug
```

Expected (stderr):
- Unchanged plain-text lines exactly as before, including `"OTLP log export enabled"` line.

Expected (collector stdout):
- Log records with `body="Starting Grafana MCP server..."`, resource attrs `service.name=mcp-grafana` / `service.version=...`.
- Records emitted during tool calls include non-empty `trace_id` / `span_id`.

- [ ] **Step 3: Confirm OTLP-disabled path**

Run without `OTEL_*` env vars:

```bash
GRAFANA_URL=http://localhost:3000 go run ./cmd/mcp-grafana
```

Expected: identical stderr output to today (no new "OTLP log export enabled" line, no new behavior).

- [ ] **Step 4: No commit** — this task is verification only.

---

### Task 12: Final quality gates

**Files:** (no code changes)

- [ ] **Step 1: Lint**

Run: `make lint`
Expected: clean.

- [ ] **Step 2: Unit tests**

Run: `make test-unit`
Expected: all pass.

- [ ] **Step 3: `go mod tidy` idempotent**

Run:

```bash
go mod tidy
git diff --exit-code go.mod go.sum
```

Expected: no diff.

- [ ] **Step 4: Final commit (if any stray formatting)**

```bash
git status
# If clean, skip. Otherwise:
git add -A
git commit -m "chore: formatting and tidy"
```

---

## Self-Review

**Spec coverage:**
- Issue asks for OTLP log export gated on `OTEL_EXPORTER_OTLP_ENDPOINT` — Tasks 1, 4, 5, 7.
- Additive; preserve stderr — Task 7 (fan-out), Task 2 (fan-out dispatches to every child).
- `trace_id` / `span_id` auto-attachment — Task 7 uses `otelslog.NewHandler` with the live `LoggerProvider`; verified in Task 11.
- Shutdown lifecycle — Task 6 adds provider to `Shutdown`.
- README + developer docs updates (user request beyond the issue) — Tasks 8, 9.
- CHANGELOG — Task 10.

**Placeholder scan:** no TBD / TODO / "handle edge cases" phrases; every code step contains the code the engineer needs.

**Type consistency:**
- `newFanoutHandler` (internal) → renamed to `NewFanoutHandler` in Task 7 step 3 so the `cmd/mcp-grafana` package can reference it. Test file and plan callsites updated.
- `setupLogging` signature `(ctx, res) -> (*sdklog.LoggerProvider, error)` consistent between Task 4 (test) and Task 5 (impl).
- `LoggerProvider()` getter added in Task 6 and consumed in Task 7.


---
