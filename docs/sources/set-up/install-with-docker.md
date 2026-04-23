---
title: Install with Docker
menuTitle: Install with Docker
description: Run the Grafana MCP server using the official Docker image.
keywords:
  - Docker
  - MCP
  - container
weight: 2
aliases: []
---

# Install with Docker

Run the Grafana MCP server using the official image from Docker Hub. The image **defaults to SSE**, but most users will want to use STDIO mode for direct integration with AI assistants like Claude Desktop.

## What you'll achieve

You run the server in a container and connect your MCP client. You can use stdio (typical for Claude Desktop and similar), SSE, streamable HTTP, or streamable HTTP with server TLS.

## Before you begin

- Docker [installed](https://docs.docker.com/get-started/get-docker/).
- A Grafana instance (Grafana 9.0 or later) and a service account token.

## Run the server in STDIO mode

For direct integration with AI assistants, most users will want to use STDIO mode. Pass `-t stdio` and `-i` so the container keeps stdin open.

### Local Grafana:
```bash
docker pull grafana/mcp-grafana
docker run --rm -i \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your service account token> \
  grafana/mcp-grafana -t stdio
```

### Grafana Cloud
```bash
docker pull grafana/mcp-grafana
docker run --rm -i \
  -e GRAFANA_URL=https://myinstance.grafana.net \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your service account token> \
  grafana/mcp-grafana -t stdio
```

## Run the server in SSE mode

In this mode, the server runs as an HTTP server that clients connect to. You must expose port **8000**.

```bash
docker pull grafana/mcp-grafana
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your service account token> \
  grafana/mcp-grafana
```

Point your client at `http://localhost:8000/sse` (or your host and port).

## Run the server in streamable HTTP mode

In this mode, the server operates as an independent process that can handle multiple client connections. You must expose port **8000** and set `-t streamable-http`.

```bash
docker pull grafana/mcp-grafana
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your service account token> \
  grafana/mcp-grafana -t streamable-http
```

The default MCP path is `/mcp` (see `--endpoint-path`). Clients often use `http://localhost:8000/mcp`.

## Run in HTTPS streamable HTTP mode with server TLS certificates

To terminate TLS on the MCP server, mount certificates and set `--server.tls-cert-file`, `--server.tls-key-file`, and `--address` (for example `:8443`):

```bash
docker pull grafana/mcp-grafana
docker run --rm -p 8443:8443 \
  -v /path/to/certs:/certs:ro \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your service account token> \
  grafana/mcp-grafana \
  -t streamable-http \
  --address :8443 \
  --server.tls-cert-file /certs/server.crt \
  --server.tls-key-file /certs/server.key
```

## Next steps

- [Install the binary](../install-the-binary/) for a host-installed binary.
- [Client configuration examples](../client-configuration-examples/) for JSON snippets (including Docker).
- [Transports and addresses](../../configure/transports-and-addresses/) and [Server TLS (streamable-http)](../../configure/server-tls-streamable-http/).
