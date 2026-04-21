---
title: Observability (metrics and tracing)
menuTitle: Observability
description: Expose Prometheus metrics and OpenTelemetry tracing from the Grafana MCP server.
keywords:
  - Prometheus
  - metrics
  - OpenTelemetry
  - tracing
  - MCP
weight: 2
aliases: []
---

# Observability (metrics and tracing)

The MCP server can expose **Prometheus metrics** and supports **[OpenTelemetry](https://opentelemetry.io/)** distributed tracing, following the [OTel MCP semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).

Metrics require the **SSE** or **streamable-http** transport. Tracing uses standard `OTEL_*` environment variables and works independently of `--metrics`.

## What you'll achieve

You can scrape MCP operation metrics or export traces to Tempo or Grafana Cloud while the server runs over HTTP transports.

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
| `mcp_server_operation_duration_seconds` | Histogram | MCP operation duration (labels: `mcp_method_name`, `gen_ai_tool_name`, `error_type`, `network_transport`, `mcp_protocol_version`) |
| `mcp_server_session_duration_seconds` | Histogram | MCP client session duration (labels: `network_transport`, `mcp_protocol_version`) |
| `http_server_request_duration_seconds` | Histogram | HTTP server request duration (from otelhttp) |

**Note**: Metrics are only available when using SSE or streamable HTTP transports. They are **not** available with stdio transport.

## Enable OpenTelemetry tracing

When `OTEL_EXPORTER_OTLP_ENDPOINT` is set, the server exports traces via OTLP/gRPC.

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

## Run with Docker (metrics and tracing)

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
