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
aliases: []
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

## Next steps

- [Server TLS (streamable-http)](../server-tls-streamable-http/) if you need HTTPS for the MCP server.
- [Client TLS](../client-tls-grafana-connection/) if Grafana is behind mTLS or custom certificates.
