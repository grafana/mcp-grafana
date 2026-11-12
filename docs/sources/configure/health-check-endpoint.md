---
title: Health check endpoint
menuTitle: Health check
description: HTTP health check for SSE and streamable-http transports.
keywords:
  - health
  - healthz
  - MCP
weight: 8
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/configure/health-check-endpoint/
---

# Health check endpoint

When you use the SSE (`-t sse`) or streamable HTTP (`-t streamable-http`) transport, the MCP server exposes a health check at `/healthz`. Load balancers, monitoring, and orchestration can use it to verify that the server is running and accepting connections.

## What you'll achieve

You can probe readiness from scripts or upstream checks when the server uses an HTTP transport.

## Before you begin

- The server running with `-t sse` or `-t streamable-http` (not stdio).

## Send a health check request

**Endpoint:** `GET /healthz`

**Response:**

- Status code: `200 OK`
- Body: `ok`

**Examples:**

```bash
# For streamable HTTP or SSE transport with default listen address (localhost:8000)
curl http://localhost:8000/healthz

# Health check on a separate listen address (for example Kubernetes probes
# while --address is bound to 127.0.0.1 behind a sidecar)
./mcp-grafana -t streamable-http --address 127.0.0.1:8000 --healthz-address :8080
curl http://localhost:8080/healthz
```

The `/healthz` handler only reports that the HTTP server is up; it does not check Grafana connectivity. A `--healthz-address` listener is not wrapped by `--allowed-hosts` / `--allowed-origins` validation, matching `--metrics-address`.

**Note**:  The health check endpoint is only available when using SSE or streamable HTTP transports. It is **not** available with the stdio transport (`-t stdio`), because stdio does not start an HTTP server.

## Next steps

- [Transports and addresses](../transports-and-addresses/)
- [Command-line flags](../command-line-flags/)
