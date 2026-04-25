---
title: Health check endpoint
menuTitle: Health check
description: HTTP health check for SSE and streamable-http transports.
keywords:
  - health
  - healthz
  - MCP
weight: 8
aliases: []
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

# With custom address
curl http://localhost:9090/healthz
```

**Note**:  The health check endpoint is only available when using SSE or streamable HTTP transports. It is **not** available with the stdio transport (`-t stdio`), because stdio does not start an HTTP server.

## Next steps

- [Transports and addresses](../transports-and-addresses/)
- [Command-line flags](../command-line-flags/)
