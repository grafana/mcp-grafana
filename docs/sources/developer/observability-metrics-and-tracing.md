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
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/developer/observability-metrics-and-tracing/
---

# Observability (metrics, tracing, and logs)

The MCP server can expose **Prometheus metrics** and supports **[OpenTelemetry](https://opentelemetry.io/)** distributed tracing and log export, following the [OTel MCP semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).

Metrics require the **SSE** or **streamable-http** transport. Tracing and log export use standard `OTEL_*` environment variables and work with any transport, independently of `--metrics`.

**Note**: mcp-grafana currently only supports the OTLP/gRPC transport for both traces and logs. `OTEL_EXPORTER_OTLP_PROTOCOL` (and its `_TRACES_PROTOCOL` / `_LOGS_PROTOCOL` variants) are not honored — gRPC is used regardless.

## What you'll achieve

You can scrape MCP operation metrics (HTTP transports only) and export traces and logs to Tempo, Loki, or Grafana Cloud under any transport, including stdio.

## Before you begin

- The server running with **SSE** or **streamable-http** (metrics are not available with stdio).

## Enable Prometheus metrics

When using SSE or streamable HTTP transports, enable Prometheus metrics with `--metrics`:

```bash
# Metrics on the main server at /metrics
./mcp-grafana -t streamable-http --metrics
```
```bash
# Metrics on a separate listen address
./mcp-grafana -t streamable-http --metrics --metrics-address :9090
```

**Available metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `mcp_server_operation_duration_seconds` | Histogram | MCP operation duration (labels: `mcp_method_name`, `gen_ai_tool_name`, `error_type`, `network_transport`, `mcp_protocol_version`, and — for `tools/call` on selected tools — `mcp_tool_operation`, `mcp_tool_resource_type`, `mcp_tool_phase`) |
| `mcp_server_session_duration_seconds` | Histogram | MCP client session duration (labels: `network_transport`, `mcp_protocol_version`) |
| `http_server_request_duration_seconds` | Histogram | HTTP server request duration (from otelhttp) |

**Note**: Metrics are only available when using SSE or streamable HTTP transports. They are **not** available with stdio transport.

### Tool-call dimension labels

For `tools/call`, `mcp_server_operation_duration_seconds` can carry up to three extra low-cardinality labels so durations are sliceable by what the call was doing:

| Label | Source | Notes |
|-------|--------|-------|
| `mcp_tool_operation` | the tool's `operation` argument | Multiplexer tools only (e.g. `alerting_manage_rules`); one of the tool's declared operations, else `other`. |
| `mcp_tool_resource_type` | the tool's `type` argument | e.g. the datasource plugin type on `create_datasource`; a plugin type the server ships a schema for, else `other`. |
| `mcp_tool_phase` | the tool's result `_meta` | Phase of a multi-call flow (e.g. `create_datasource` schema guidance vs. actual creation). |

Arguments are raw client input, so the two argument-derived labels are allowlisted by tool **and** by value: unlisted tools emit neither, and unlisted values collapse into one `other` bucket, giving each label a fixed maximum series count. Tool validation alone would not bound them — a rejected `operation` is still instrumented, and `create_datasource`'s `type` is free text that succeeds for unknown values. `mcp_tool_phase` is exempt: the tool sets it on its own result, and tools must keep that value set small.

The high-cardinality **target** of a call (the datasource `uid`, else `name`) is never a metric label — it is span-only as `mcp.tool.target`, and empty for calls that name no entity (see below).

## Enable OpenTelemetry tracing

When `OTEL_EXPORTER_OTLP_ENDPOINT` (or the signal-specific `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) is set, the server exports traces via OTLP/gRPC.

Local example:
```bash
# Send traces to a local Tempo instance
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 \
OTEL_EXPORTER_OTLP_INSECURE=true \
./mcp-grafana -t streamable-http
```

Grafana Cloud example:

```bash
# Send traces to Grafana Cloud with authentication
OTEL_EXPORTER_OTLP_ENDPOINT=https://tempo-us-central1.grafana.net:443 \
OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic ..." \
./mcp-grafana -t streamable-http
```

Tool call spans follow naming like `tools/call <tool_name>` and include attributes such as `gen_ai.tool.name`, `mcp.method.name`, and `mcp.session.id`. The server supports W3C trace context propagation from the `_meta` field of tool call requests.

### Trace context propagation

Under an HTTP transport (SSE or streamable-http) the server takes part in [W3C trace context](https://www.w3.org/TR/trace-context/) propagation on both sides, so a caller, mcp-grafana, and Grafana appear as one connected trace:

- **Inbound**: a `traceparent`/`tracestate` header on the incoming request (from an MCP client or an upstream proxy) parents the server span, continuing the caller's trace instead of starting a new one.
- **Outbound**: requests to the Grafana API carry a `traceparent` naming mcp-grafana's own span, so Grafana's spans hang off ours.

Propagation is always active — it does not require `OTEL_EXPORTER_OTLP_ENDPOINT`. With trace export off, no spans are recorded but an inbound trace context is still passed through to Grafana rather than dropped.

The propagators used are configured with the standard [`OTEL_PROPAGATORS`](https://opentelemetry.io/docs/languages/sdk-configuration/general/#otel_propagators) environment variable, defaulting to `tracecontext,baggage`. Set it to interoperate with a non-W3C system (`b3`, `b3multi`, `jaeger`, `xray`, `ottrace`, in any comma-separated combination) or to `none` to disable propagation entirely.

Tool call requests may also carry `traceparent`/`tracestate` in their MCP `_meta` field; when present, that context parents the tool span. This is the only propagation channel available under stdio, where there is no HTTP request to read headers from.

Listing `traceparent` or `tracestate` in `GRAFANA_FORWARD_HEADERS` is not needed and no longer has an effect: forwarding the *caller's* `traceparent` verbatim would parent Grafana's spans onto the caller and cut mcp-grafana out of the middle of the trace, so a forwarded value never overrides the propagated one. Forwarded trace headers still apply when propagation is disabled with `OTEL_PROPAGATORS=none`.

Tool-call spans also carry the [tool-call dimensions](#tool-call-dimension-labels) `mcp.tool.operation`, `mcp.tool.resource_type`, and `mcp.tool.phase`, plus the span-only `mcp.tool.target` (the datasource `uid`, else `name`) for grouping the spans that touch one entity. `mcp.tool.target` is empty when a call names no entity — notably `create_datasource`'s `phase=schema` call, where the datasource does not exist yet — so stitching that call to the later `phase=created` one relies on the shared trace (when the client propagates context via `_meta`) or on `mcp.session.id` plus `mcp_tool_resource_type`, rather than on the target. Unlike the metric labels, these are attached for **all** tools with their **raw** values (no allowlist, no `other` bucket), since traces are high-cardinality by design. They require an **HTTP transport** (SSE or streamable-http, so a server span exists to enrich) **and tracing enabled**, but **not** `--metrics`; with stdio or tracing off, enrichment is a no-op.

## Enable OpenTelemetry logs

When `OTEL_EXPORTER_OTLP_ENDPOINT` (or the signal-specific `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`) is set, the server also exports structured logs via OTLP/gRPC in addition to the existing plain-text stderr output. Logs carry `trace_id` and `span_id` from the active span so they correlate with exported traces.

Traces and logs resolve their endpoints independently, so the two signals can be enabled separately:

- Setting only `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` enables tracing **without** log export.
- Setting only `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` enables log export without tracing.
- Setting the generic `OTEL_EXPORTER_OTLP_ENDPOINT` enables both.

```bash
# Send logs and traces to a local OTel collector
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 \
OTEL_EXPORTER_OTLP_INSECURE=true \
./mcp-grafana -t streamable-http
```

Stderr logging continues unchanged; operators can pipe stderr to `/dev/null` if they only want logs going to the OTel collector.

Logs can be sent directly to any managed backend that accepts OTLP/gRPC — for example, Grafana Cloud — by pointing `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` (or the generic `OTEL_EXPORTER_OTLP_ENDPOINT`) at the remote gRPC endpoint and supplying auth via `OTEL_EXPORTER_OTLP_LOGS_HEADERS` (or `OTEL_EXPORTER_OTLP_HEADERS`), mirroring the tracing example above. A local OTel collector is optional — useful for fan-out, batching, or multi-backend routing, but not required.

The signal-specific variants `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`, `OTEL_EXPORTER_OTLP_LOGS_HEADERS`, `OTEL_EXPORTER_OTLP_LOGS_INSECURE`, `OTEL_EXPORTER_OTLP_LOGS_CERTIFICATE`, `OTEL_EXPORTER_OTLP_LOGS_TIMEOUT`, and `OTEL_EXPORTER_OTLP_LOGS_COMPRESSION` are honored and override their generic `OTEL_EXPORTER_OTLP_*` counterparts — see the [OTel exporter spec](https://opentelemetry.io/docs/specs/otel/protocol/exporter/) for the full list and precedence rules.

**Note**: If the configured collector is unreachable, log records are buffered in memory (default queue: 2048) and the oldest records are dropped once the queue fills. The process continues without blocking the service. Configure a local OTel collector if you need lossless buffering during outages.

Logs are also exported under the stdio transport, which makes it easy to centralize logs from local `mcp-grafana` instances invoked by IDE clients.

## Run with Docker (metrics, tracing, and logs)

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your token> \
  -e OTEL_EXPORTER_OTLP_ENDPOINT=http://tempo:4317 \
  -e OTEL_EXPORTER_OTLP_INSECURE=true \
  grafana/mcp-grafana \
  -t streamable-http --metrics
```

## Next steps

- [Build, test, and lint](../build-and-test/)
- [Transports and addresses](../../configure/transports-and-addresses/)
- [Command-line flags](../../configure/command-line-flags/)
