---
title: Client TLS (Grafana connection)
menuTitle: Client TLS
description: Use TLS certificates when the Grafana MCP server connects to Grafana (mTLS or custom CA).
keywords:
  - TLS
  - mTLS
  - certificate
  - Grafana
  - MCP
weight: 4
aliases: []
---

# Client TLS (Grafana connection)

If your Grafana instance uses mTLS or a custom CA, configure the MCP server to use the correct certificates when it calls the Grafana API.

## What you'll achieve

The server uses your client certificate and key (and optionally a CA file) for HTTPS requests to Grafana, or verifies Grafana’s server certificate with a custom CA.

## Before you begin

- Client certificate and key (and CA file if needed) for your Grafana endpoint.
- The server installed (binary or Docker with access to the cert files).

## Configure client TLS

Use these flags when starting the server:

- **--tls-cert-file** – Path to the client certificate file.
- **--tls-key-file** – Path to the client private key file.
- **--tls-ca-file** – Path to the CA certificate file for server verification.
- **--tls-skip-verify** – Skip TLS verification (insecure; use only for testing).

Example:

```bash
mcp-grafana \
  --tls-cert-file /path/to/client.crt \
  --tls-key-file /path/to/client.key \
  --tls-ca-file /path/to/ca.crt
```

With Docker, mount the cert directory and pass the paths inside the container (for example, `/certs/client.crt`).

## Next steps

- [Client configuration examples](../../set-up/client-configuration-examples/) for full MCP JSON (binary and Docker) with TLS flags.
- [Server TLS (streamable-http)](../server-tls-streamable-http/) if you want HTTPS for the MCP server itself.
- [Authentication](../authentication/) for Grafana credentials.
- [Command-line flags](../command-line-flags/) for all TLS-related flags.
