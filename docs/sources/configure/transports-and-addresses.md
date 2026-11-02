---
title: Transports and addresses
menuTitle: Transports and addresses
description: "Choose how the Grafana MCP server communicates with clients: stdio, SSE, or streamable-http."
keywords:
  - transport
  - stdio
  - SSE
  - streamable-http
  - MCP
weight: 2
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/configure/transports-and-addresses/
---

# Transports and addresses

The Grafana MCP server supports three transports: stdio (default for local use), SSE, and streamable-http. Choose the one that matches how your MCP client connects.

## What you'll achieve

You run the server with the right transport and, for SSE or streamable-http, the correct address and path so your client can connect.

## Before you begin

- The server installed (for example, [Install with uvx](../../set-up/install-with-uvx/) or [Install with Docker](../../set-up/install-with-docker/)).

## Use stdio transport

With stdio (the default when you run the binary or uvx), the server talks to the client over standard input and output. Use this when the client launches the server as a subprocess (for example, Claude Desktop, Cursor). No `--address` or port is needed.

```bash
mcp-grafana
# or
uvx mcp-grafana
```

## Use SSE or streamable-http transport

With `-t sse` or `-t streamable-http`, the server listens on an address (default `localhost:8000`). Use `--address` to change host and port, and optionally `--base-path` or `--endpoint-path` for path configuration.

```bash
mcp-grafana -t sse
mcp-grafana -t streamable-http --address :9090
```

Clients connect to the server URL (for example, `http://localhost:8000/sse` for SSE). For streamable-http, the default endpoint path is `/mcp` (override with `--endpoint-path` if your client expects a different path).

`--base-path` prefixes the application routes on the listener:

```bash
mcp-grafana -t sse --address :8000 --base-path /my-base --metrics
```

serves `/my-base/sse`. `/healthz` and `/metrics` are internal-only endpoints for probes and scrapers, so they always stay at the server root — never under `--base-path` — regardless of this flag.

{{< admonition type="note" >}}
Over SSE, per-request headers do not reach tool calls: `X-Grafana-Service-Account-Token` / `X-Grafana-API-Key`, `X-Grafana-Org-Id`, and headers listed in `GRAFANA_FORWARD_HEADERS` have no effect, tool calls use the server's environment credentials, and `X-Grafana-URL` overrides do not work. Use streamable-http when each caller needs its own Grafana URL, credentials, or organization.
{{< /admonition >}}

### Keep idle SSE sessions alive behind a proxy

Reverse proxies and load balancers close connections that stay idle longer than their timeout, for example, 60 seconds for ingress-nginx's default `proxy-read-timeout`. When that happens to an SSE event stream, the server ends the session, and the client's next tool call fails. Set `--sse-keepalive` to an interval below the proxy's idle timeout, so the server pings each client and the stream never sits idle that long:

```bash
mcp-grafana -t sse --sse-keepalive 30s
```

A client is disconnected after 3 pings in a row go unanswered for half the interval each, so one slow reply doesn't end the session. The interval must be at least `1s`. The flag has no effect on the stdio and streamable-http transports.

## Next steps

- [Server TLS (streamable-http)](../server-tls-streamable-http/) if you need HTTPS for the MCP server.
- [Client TLS](../client-tls-grafana-connection/) if Grafana is behind mTLS or custom certificates.
