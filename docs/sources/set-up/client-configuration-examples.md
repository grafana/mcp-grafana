---
title: Client configuration examples
menuTitle: Client configuration examples
description: Example MCP client JSON for uvx, binary, Docker, VS Code, and debug mode.
keywords:
  - MCP
  - configuration
  - Claude
  - Docker
weight: 5
aliases: []
---

# Client configuration examples

This page walks through credentials, installation options, and MCP client JSON patterns for common editors and runtimes.

The MCP server works with local Grafana and [Grafana Cloud](https://grafana.com/docs/grafana-cloud/). For Grafana Cloud, use your instance URL (for example, `https://myinstance.grafana.net`) instead of `http://localhost:3000` in the examples below.

## What you'll achieve

You can copy working configuration blocks for uvx, the binary, Docker, VS Code remote, debug mode, and TLS.

## Before you begin

1. If you use a **service account token**, create a service account in Grafana with the permissions your tools need, create a token, and copy it into your config. Refer to the [Grafana service account documentation](https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana).

   Tip: assigning the built-in **Editor** role is a simple option when you do not want to tune every scope; it is broader than least-privilege.

{{< admonition type="note" >}}
The environment variable `GRAFANA_API_KEY` is deprecated in favor of `GRAFANA_SERVICE_ACCOUNT_TOKEN`. The old name still works but may log warnings.
{{< /admonition >}}

2. Install `mcp-grafana` using one of the methods in [Set up](../../set-up/).

3. Add the server block to your client configuration using one of the patterns below.

For organization targeting and custom headers, refer to [Multi-organization and headers](../../configure/multi-organization-and-headers/).

## Multi-organization support
 
You can specify which organization to interact with using either:

- **Environment variable:** Set `GRAFANA_ORG_ID` to the numeric organization ID
- **HTTP header:** Set `X-Grafana-Org-Id` when using SSE or streamable HTTP transports (header takes precedence over environment variable - meaning you can set a default org as well).

When an organization ID is provided, the MCP server will set the `X-Grafana-Org-Id` header on all requests to Grafana, ensuring that operations are performed within the specified organization context.

**Example with organization ID:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_USERNAME": "<your username>",
        "GRAFANA_PASSWORD": "<your password>",
        "GRAFANA_ORG_ID": "2"
      }
    }
  }
}
```

## Custom HTTP headers

You can add arbitrary HTTP headers to all Grafana API requests using the `GRAFANA_EXTRA_HEADERS` environment variable. The value should be a JSON object mapping header names to values.

**Example with custom headers:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your token>",
        "GRAFANA_EXTRA_HEADERS": "{\"X-Custom-Header\": \"custom-value\", \"X-Tenant-ID\": \"tenant-123\"}"
      }
    }
  }
}
```

## Install options

You can install `mcp-grafana` in several ways:

   - **uvx (recommended)**: If you have [uv](https://docs.astral.sh/uv/getting-started/installation/) installed, no extra setup is needed — `uvx` will automatically download and run the server:

     ```bash
     uvx mcp-grafana
     ```

   - **Docker image**: Use the pre-built Docker image from Docker Hub.

     **Important**: The Docker image's entrypoint is configured to run the MCP server in SSE mode by default, but most users will want to use STDIO mode for direct integration with AI assistants like Claude Desktop:

     1. **STDIO Mode**: For stdio mode you must explicitly override the default with `-t stdio` and include the `-i` flag to keep stdin open:

     ```bash
     docker pull grafana/mcp-grafana
     # For local Grafana:
     docker run --rm -i -e GRAFANA_URL=http://localhost:3000 -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your service account token> grafana/mcp-grafana -t stdio
     # For Grafana Cloud:
     docker run --rm -i -e GRAFANA_URL=https://myinstance.grafana.net -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your service account token> grafana/mcp-grafana -t stdio
     ```

     2. **SSE Mode**: In this mode, the server runs as an HTTP server that clients connect to. You must expose port 8000 using the `-p` flag:

     ```bash
     docker pull grafana/mcp-grafana
     docker run --rm -p 8000:8000 -e GRAFANA_URL=http://localhost:3000 -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your service account token> grafana/mcp-grafana
     ```

     3. **Streamable HTTP Mode**: In this mode, the server operates as an independent process that can handle multiple client connections. You must expose port 8000 using the `-p` flag: For this mode you must explicitly override the default with `-t streamable-http`

     ```bash
     docker pull grafana/mcp-grafana
     docker run --rm -p 8000:8000 -e GRAFANA_URL=http://localhost:3000 -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your service account token> grafana/mcp-grafana -t streamable-http
     ```

     For HTTPS streamable HTTP mode with server TLS certificates:

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

   - **Download binary**: Download the latest release of `mcp-grafana` from the [releases page](https://github.com/grafana/mcp-grafana/releases) and place it in your `$PATH`.

   - **Build from source**: If you have a Go toolchain installed you can also build and install it from source, using the `GOBIN` environment variable
     to specify the directory where the binary should be installed. This should also be in your `$PATH`.

     ```bash
     GOBIN="$HOME/go/bin" go install github.com/grafana/mcp-grafana/cmd/mcp-grafana@latest
     ```

   - **Deploy to Kubernetes using Helm**: use the [Helm chart from the Grafana helm-charts repository](https://github.com/grafana/helm-charts/tree/main/charts/grafana-mcp)

     ```bash
     helm repo add grafana https://grafana.github.io/helm-charts
     helm install --set grafana.apiKey=<Grafana_ApiKey> --set grafana.url=<GrafanaUrl> my-release grafana/grafana-mcp
     ```


## Add the server to your client

Add the server configuration to your client configuration file. For example, for Claude Desktop:

   **If using `uvx`:**

   ```json
   {
     "mcpServers": {
       "grafana": {
         "command": "uvx",
         "args": ["mcp-grafana"],
         "env": {
           "GRAFANA_URL": "http://localhost:3000",
           "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your service account token>"
         }
       }
     }
   }
   ```

   **If using the binary:**

   ```json
   {
     "mcpServers": {
       "grafana": {
         "command": "mcp-grafana",
         "args": [],
         "env": {
           "GRAFANA_URL": "http://localhost:3000",  // Or "https://myinstance.grafana.net" for Grafana Cloud
           "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your service account token>",
           // If using username/password authentication
           "GRAFANA_USERNAME": "<your username>",
           "GRAFANA_PASSWORD": "<your password>",
           // Optional: specify organization ID for multi-org support
           "GRAFANA_ORG_ID": "1"
         }
       }
     }
   }
   ```

{{< admonition type="note" >}}
If you see `Error: spawn mcp-grafana ENOENT` in Claude Desktop, specify the full path to `mcp-grafana`.
{{< /admonition >}}

**If using Docker:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "-e",
        "GRAFANA_URL",
        "-e",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN",
        "grafana/mcp-grafana",
        "-t",
        "stdio"
      ],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",  // Or "https://myinstance.grafana.net" for Grafana Cloud
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your service account token>",
        // If using username/password authentication
        "GRAFANA_USERNAME": "<your username>",
        "GRAFANA_PASSWORD": "<your password>",
        // Optional: specify organization ID for multi-org support
        "GRAFANA_ORG_ID": "1"
      }
    }
  }
}
```

{{< admonition type="note" >}}
The `-t stdio` argument is essential here because it overrides the default SSE mode in the Docker image.
{{< /admonition >}}

**Using VSCode with remote MCP server**

If you're using VSCode and running the MCP server in SSE mode (which is the default when using the Docker image without overriding the transport), make sure your `.vscode/settings.json` includes the following:

```json
"mcp": {
  "servers": {
    "grafana": {
      "type": "sse",
      "url": "http://localhost:8000/sse"
    }
  }
}
```

If you terminate TLS in front of an **SSE** server (or the listener still speaks SSE on `/sse`), your client URL might look like `https://localhost:8443/sse` with `type: "sse"`.

If you run **streamable-http** with server TLS (for example the Docker example using `-t streamable-http`, `--address :8443`, and `--server.tls-*`), the MCP HTTP endpoint is `--endpoint-path` (default `/mcp`), for example `https://localhost:8443/mcp`. That is **not** the same as `/sse`; use the client and `type` your editor documents for streamable HTTP, not the SSE snippet above.

## Debug mode

You can enable debug mode for the Grafana transport by adding the `-debug` flag to the command. This will provide detailed logging of HTTP requests and responses between the MCP server and the Grafana API, which can be helpful for troubleshooting.

To use debug mode with the Claude Desktop configuration, update your config as follows:

**If using the binary:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": ["-debug"],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",  // Or "https://myinstance.grafana.net" for Grafana Cloud
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your service account token>"
      }
    }
  }
}
```

**If using Docker:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "-e",
        "GRAFANA_URL",
        "-e",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN",
        "grafana/mcp-grafana",
        "-t",
        "stdio",
        "-debug"
      ],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",  // Or "https://myinstance.grafana.net" for Grafana Cloud
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your service account token>"
      }
    }
  }
}
```

{{< admonition type="note" >}}
As with the standard configuration, the `-t stdio` argument is required to override the default SSE mode in the Docker image.
{{< /admonition >}}

## TLS configuration (client to Grafana)

If your Grafana instance is behind mTLS or requires custom TLS certificates, configure the MCP server to use the correct certificates when calling Grafana:

- `--tls-cert-file`: Path to TLS certificate file for client authentication
- `--tls-key-file`: Path to TLS private key file for client authentication
- `--tls-ca-file`: Path to TLS CA certificate file for server verification
- `--tls-skip-verify`: Skip TLS certificate verification (insecure; use only for testing)

**Example with client certificate authentication:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [
        "--tls-cert-file",
        "/path/to/client.crt",
        "--tls-key-file",
        "/path/to/client.key",
        "--tls-ca-file",
        "/path/to/ca.crt"
      ],
      "env": {
        "GRAFANA_URL": "https://secure-grafana.example.com",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your service account token>"
      }
    }
  }
}
```

**Example with Docker:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "-v",
        "/path/to/certs:/certs:ro",
        "-e",
        "GRAFANA_URL",
        "-e",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN",
        "grafana/mcp-grafana",
        "-t",
        "stdio",
        "--tls-cert-file",
        "/certs/client.crt",
        "--tls-key-file",
        "/certs/client.key",
        "--tls-ca-file",
        "/certs/ca.crt"
      ],
      "env": {
        "GRAFANA_URL": "https://secure-grafana.example.com",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your service account token>"
      }
    }
  }
}
```

The TLS configuration is applied to all HTTP clients used by the MCP server, including:
- The main Grafana OpenAPI client
- Prometheus datasource clients
- Loki datasource clients
- Incident management clients
- Sift investigation clients
- Alerting clients
- Asserts clients

**Direct CLI usage examples:**

For testing with self-signed certificates:
```bash
./mcp-grafana --tls-skip-verify -debug
```

With client certificate authentication:
```bash
./mcp-grafana \
  --tls-cert-file /path/to/client.crt \
  --tls-key-file /path/to/client.key \
  --tls-ca-file /path/to/ca.crt \
  -debug
```

With custom CA certificate only:
```bash
./mcp-grafana --tls-ca-file /path/to/ca.crt
```

**Programmatic usage (Go):**

If you're using this library programmatically, you can also create TLS-enabled context functions:

```go
// Using struct literals
tlsConfig := &mcpgrafana.TLSConfig{
    CertFile: "/path/to/client.crt",
    KeyFile:  "/path/to/client.key",
    CAFile:   "/path/to/ca.crt",
}
grafanaConfig := mcpgrafana.GrafanaConfig{
    Debug:     true,
    TLSConfig: tlsConfig,
}
contextFunc := mcpgrafana.ComposedStdioContextFunc(grafanaConfig)

// Or inline
grafanaConfig := mcpgrafana.GrafanaConfig{
    Debug: true,
    TLSConfig: &mcpgrafana.TLSConfig{
        CertFile: "/path/to/client.crt",
        KeyFile:  "/path/to/client.key",
        CAFile:   "/path/to/ca.crt",
    },
}
contextFunc := mcpgrafana.ComposedStdioContextFunc(grafanaConfig)
```

For a shorter overview, refer to [Client TLS (Grafana connection)](../../configure/client-tls-grafana-connection/).

## Server TLS configuration (Streamable HTTP transport only)

When using the streamable HTTP transport (`-t streamable-http`), you can configure the MCP server to serve HTTPS instead of HTTP. This is useful when you need to secure the connection between your MCP client and the server itself.

The server supports the following TLS configuration options for the streamable HTTP transport:

- `--server.tls-cert-file`: Path to TLS certificate file for server HTTPS (required for TLS)
- `--server.tls-key-file`: Path to TLS private key file for server HTTPS (required for TLS)

**Note**: These flags are completely separate from the client TLS flags documented above. The client TLS flags configure how the MCP server connects to Grafana, while these server TLS flags configure how clients connect to the MCP server when using streamable HTTP transport.

**Example:**
Example with HTTPS streamable HTTP server

```bash
./mcp-grafana \
  -t streamable-http \
  --server.tls-cert-file /path/to/server.crt \
  --server.tls-key-file /path/to/server.key \
  --address :8443
```

This would start the MCP server on HTTPS port 8443. Clients would then connect to `https://localhost:8443/mcp` instead of `http://localhost:8000/mcp` (the default `--endpoint-path` is `/mcp`).

**Docker example:**
Docker example with server TLS:

```bash
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

Refer to [Server TLS (streamable-http)](../../configure/server-tls-streamable-http/) for more detail.

## Next steps

- [Health check endpoint](../../configure/health-check-endpoint/)
- [Observability (metrics and tracing)](../../developer/observability-metrics-and-tracing/)
- [Troubleshooting](../../troubleshooting/)
