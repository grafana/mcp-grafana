---
title: Server TLS (streamable-http)
menuTitle: Server TLS
description: Serve the Grafana MCP server over HTTPS when using streamable-http transport.
keywords:
  - TLS
  - HTTPS
  - streamable-http
  - MCP
weight: 5
aliases: []
---

# Server TLS (streamable-http)

When you use the streamable-http transport, you can serve the MCP server over HTTPS using your own TLS certificate and key.

## What you'll achieve

Clients connect to the server with `https://` instead of `http://`. This is separate from client TLS, which configures how the server connects to Grafana.

## Before you begin

- A TLS certificate and private key for the host and port where the server will listen.
- The server run with `-t streamable-http`.

## Configure server TLS

Use these flags with streamable-http:

- **--server.tls-cert-file** – Path to the server TLS certificate file.
- **--server.tls-key-file** – Path to the server TLS private key file.

Example:

```bash
mcp-grafana -t streamable-http \
  --server.tls-cert-file /path/to/server.crt \
  --server.tls-key-file /path/to/server.key \
  --address :8443
```

Clients then connect to `https://localhost:8443` with the streamable-http path (default `--endpoint-path` is `/mcp`, for example `https://localhost:8443/mcp`).

## Next steps

- [Transports and addresses](../transports-and-addresses/) for transport options.
- [Client TLS (Grafana connection)](../client-tls-grafana-connection/) for TLS toward Grafana.
