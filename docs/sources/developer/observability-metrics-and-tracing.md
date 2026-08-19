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

### Proxied MCP server metrics

When proxying the MCP servers embedded in Grafana datasources (see the Proxied MCP Servers section of the README), the server also reports how discovery, connection, and re-exposure of the upstream surface went. These are emitted under any transport, but as with every metric here they are only scrapeable when `--metrics` is on, which requires an HTTP transport.

| Metric | Type | Description |
|--------|------|-------------|
| `mcp_discovery_probe_success_total` | Counter | MCP-support probes that found a datasource is MCP-enabled (label: `datasource_type`) |
| `mcp_discovery_probe_deterministic_failure_total` | Counter | Probes that got a clean non-retryable answer, e.g. a `404` from a datasource with no MCP endpoint (label: `datasource_type`) |
| `mcp_discovery_probe_transient_failure_total` | Counter | Probes that exhausted their retries on a timeout, network error, or `5xx` (label: `datasource_type`) |
| `mcp_discovery_probe_retries_total` | Counter | Probe attempts issued as a retry (label: `datasource_type`) |
| `mcp_discovery_connect_success_total` | Counter | Upstream connections established (label: `datasource_type`) |
| `mcp_discovery_connect_deterministic_failure_total` | Counter | Connections that failed for a non-retryable reason, e.g. an auth rejection (label: `datasource_type`) |
| `mcp_discovery_connect_transient_failure_total` | Counter | Connections that exhausted their retries on a transient error (label: `datasource_type`) |
| `mcp_discovery_connect_retries_total` | Counter | Connect attempts issued as a retry (label: `datasource_type`) |
| `mcp_discovery_upstream_list_failure_total` | Counter | Upstream capability listings that failed with something other than method-not-found (labels: `datasource_type`, `mcp_proxied_capability`) |
| `mcp_proxied_capabilities_registered_total` | Counter | Upstream capabilities re-exposed to clients (labels: `mcp_proxied_capability`, `mcp_proxied_scope`) |
| `mcp_proxied_capabilities_skipped_total` | Counter | Upstream capabilities discovered but not re-exposed (labels: `mcp_proxied_capability`, `mcp_proxied_skip_reason`) |
| `mcp_proxied_register_failure_total` | Counter | Failures registering proxied capabilities on a session (labels: `mcp_proxied_capability`, `mcp_proxied_scope`) |

All the labels are low cardinality by construction: `datasource_type` and `mcp_proxied_capability` (`tools`, `resources`, `resource_templates`, `prompts`) come from closed sets, `mcp_proxied_scope` is `server` (registered once at startup, stdio) or `session` (registered per session, HTTP transports), and `mcp_proxied_skip_reason` is one of a fixed set of literals. The datasource **UID** is deliberately absent from all of them — with one series per datasource instance it belongs on spans, not metrics.

The two failure counters are worth alerting on: an upstream listing that fails is tolerated at connect time, so without `mcp_discovery_upstream_list_failure_total` a datasource proxying no resources looks exactly like one that has none. `mcp_proxied_capabilities_skipped_total` reports the known gaps — `mcp_proxied_skip_reason="no_session_prompt_support"` for upstream prompts on an HTTP transport (they cannot be scoped to one session, so they are dropped rather than shared across tenants), and `unnamespaceable_template` for an upstream resource template this server cannot rewrite into a routable namespaced form.

Proxied `resources/read` and `prompts/get` requests are timed by `mcp_server_operation_duration_seconds` like any other MCP operation, under `mcp_method_name="resources/read"` and `mcp_method_name="prompts/get"`.

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

Spans for proxied `resources/read` and `prompts/get` requests carry which datasource instance and upstream target the request was routed to: `datasource.type`, `datasource.uid`, and either `mcp.proxied.upstream.uri` (the original upstream URI recovered from the namespaced URN) or `mcp.proxied.upstream.prompt`. These are span-only for the same reason as `mcp.tool.target` — a UID or resource URI is one series per instance — and they are what turns "a proxied read failed" into "this datasource's `docs://traceql/metrics` failed". Like the tool dimensions they need an HTTP transport and tracing enabled.

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
