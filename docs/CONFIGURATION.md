# Configuration Guide

This guide covers all configuration options for the Grafana MCP server and how to set it up with various MCP clients.

## Table of Contents

- [Overview](#overview)
- [Authentication](#authentication)
- [Environment Variables](#environment-variables)
- [Transport Modes](#transport-modes)
- [Client Configuration](#client-configuration)
- [Debug Mode](#debug-mode)
- [TLS Configuration](#tls-configuration)
- [Tool Configuration](#tool-configuration)
- [Health Check Endpoint](#health-check-endpoint)

## Overview

The Grafana MCP server can be configured through:
- **Environment variables** - For authentication and Grafana connection details
- **Command-line flags** - For server behavior, transport mode, and feature toggles
- **Client configuration** - For integrating with MCP clients like Claude Desktop, VSCode, etc.

## Authentication

The server supports two authentication methods:

### Service Account Token (Recommended)

Create a service account in Grafana and generate a token:

```bash
export GRAFANA_URL=http://localhost:3000
export GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token>
```

> **Best Practice:** Service account tokens are more secure and provide better audit trails than username/password authentication.

### Username/Password

For development or testing environments:

```bash
export GRAFANA_URL=http://localhost:3000
export GRAFANA_USERNAME=admin
export GRAFANA_PASSWORD=admin
```

> **Warning:** Username/password authentication may be less secure and is not recommended for production use.

### Grafana Cloud

For Grafana Cloud instances, use your instance URL:

```bash
export GRAFANA_URL=https://myinstance.grafana.net
export GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-cloud-token>
```

## Environment Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `GRAFANA_URL` | Your Grafana instance URL | Yes | - |
| `GRAFANA_SERVICE_ACCOUNT_TOKEN` | Service account token for authentication | Yes* | - |
| `GRAFANA_USERNAME` | Username for basic authentication | Yes* | - |
| `GRAFANA_PASSWORD` | Password for basic authentication | Yes* | - |
| `GRAFANA_API_KEY` | **(Deprecated)** Use `GRAFANA_SERVICE_ACCOUNT_TOKEN` instead | No | - |

\* Either `GRAFANA_SERVICE_ACCOUNT_TOKEN` or both `GRAFANA_USERNAME` and `GRAFANA_PASSWORD` are required.

## Transport Modes

The server supports three transport modes:

### STDIO (Default)

Direct stdin/stdout communication, ideal for local AI assistants:

```bash
mcp-grafana -t stdio
```

**Use cases:**
- Claude Desktop
- Local AI assistant integrations
- Testing with MCP Inspector

### SSE (Server-Sent Events)

HTTP server mode for remote clients:

```bash
mcp-grafana -t sse --address localhost:8000
```

**Use cases:**
- Remote client connections
- VSCode with remote MCP servers
- Multiple client connections

### Streamable HTTP

HTTP server with streaming support for multiple concurrent connections:

```bash
mcp-grafana -t streamable-http --address localhost:8000
```

**Use cases:**
- Production deployments
- Load-balanced setups
- Multiple concurrent clients

## Client Configuration

### Claude Desktop

Claude Desktop uses a JSON configuration file located at:
- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`
- **Linux:** `~/.config/Claude/claude_desktop_config.json`

#### Using Binary

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

> **Note:** If you see `Error: spawn mcp-grafana ENOENT`, specify the full path to the binary (e.g., `/usr/local/bin/mcp-grafana` or `C:\\Program Files\\mcp-grafana\\mcp-grafana.exe`).

#### Using Docker

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
        "mcp/grafana",
        "-t",
        "stdio"
      ],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

> **Important:** The `-t stdio` argument is essential to override the default SSE mode in the Docker image.

For more examples, see [Claude Desktop Examples](examples/claude-desktop.md).

### VSCode

VSCode configuration depends on the transport mode you're using.

#### Remote Server (SSE Mode)

If running the MCP server separately in SSE mode, configure `.vscode/settings.json`:

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "sse",
        "url": "http://localhost:8000/sse"
      }
    }
  }
}
```

#### With HTTPS

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "sse",
        "url": "https://localhost:8443/sse"
      }
    }
  }
}
```

For more examples, see [VSCode Examples](examples/vscode.md).

### Cline

Cline (VSCode extension) configuration:

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

### MCP Inspector

For testing with the MCP Inspector:

```bash
npx @modelcontextprotocol/inspector mcp-grafana
```

## Debug Mode

Enable debug mode for detailed HTTP request/response logging:

```bash
mcp-grafana --debug
```

### Claude Desktop with Debug Mode

**Binary:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": ["--debug"],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

**Docker:**

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
        "mcp/grafana",
        "-t",
        "stdio",
        "--debug"
      ],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

**What Debug Mode Shows:**
- Full HTTP requests to Grafana API
- Response status codes and headers
- Request/response bodies
- Timing information
- Error details

## TLS Configuration

### Client TLS (Grafana Connection)

Configure TLS for connecting to Grafana instances with custom certificates or mTLS.

#### Available Options

| Flag | Description |
|------|-------------|
| `--tls-cert-file` | Path to TLS certificate file for client authentication |
| `--tls-key-file` | Path to TLS private key file for client authentication |
| `--tls-ca-file` | Path to TLS CA certificate file for server verification |
| `--tls-skip-verify` | Skip TLS certificate verification (insecure, testing only) |

#### Example: Client Certificate Authentication

**Command Line:**

```bash
mcp-grafana \
  --tls-cert-file /path/to/client.crt \
  --tls-key-file /path/to/client.key \
  --tls-ca-file /path/to/ca.crt
```

**Claude Desktop:**

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
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

**Docker:**

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
        "mcp/grafana",
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
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

#### Example: Self-Signed Certificates (Testing Only)

```bash
mcp-grafana --tls-skip-verify --debug
```

> **Warning:** Never use `--tls-skip-verify` in production environments. It disables certificate verification and makes connections vulnerable to man-in-the-middle attacks.

#### Example: Custom CA Only

```bash
mcp-grafana --tls-ca-file /path/to/ca.crt
```

#### What Client TLS Affects

Client TLS configuration applies to all connections from the MCP server to Grafana, including:
- Main Grafana OpenAPI client
- Prometheus datasource clients
- Loki datasource clients
- Incident management clients
- Sift investigation clients
- Alerting clients
- OnCall clients
- Asserts clients

### Server TLS (MCP Server Connection)

Configure TLS for securing connections **to** the MCP server (only for `streamable-http` transport).

> **Important:** Server TLS is completely separate from client TLS. Server TLS secures the connection between your MCP client and the MCP server, while client TLS secures the connection between the MCP server and Grafana.

#### Available Options

| Flag | Description |
|------|-------------|
| `--server.tls-cert-file` | Path to TLS certificate file for server HTTPS |
| `--server.tls-key-file` | Path to TLS private key file for server HTTPS |

#### Example: HTTPS Server

**Command Line:**

```bash
mcp-grafana \
  -t streamable-http \
  --address :8443 \
  --server.tls-cert-file /path/to/server.crt \
  --server.tls-key-file /path/to/server.key
```

**Docker:**

```bash
docker run --rm -p 8443:8443 \
  -v /path/to/certs:/certs:ro \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana \
  -t streamable-http \
  -addr :8443 \
  --server.tls-cert-file /certs/server.crt \
  --server.tls-key-file /certs/server.key
```

Clients would then connect to `https://localhost:8443/` instead of `http://localhost:8000/`.

#### Programmatic Usage

If you're using this library programmatically in Go:

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

## Tool Configuration

Control which tools are available to MCP clients.

### Disable Specific Tool Categories

```bash
# Disable OnCall tools
mcp-grafana --disable-oncall

# Disable multiple categories
mcp-grafana --disable-oncall --disable-incident --disable-sift
```

### Available Disable Flags

| Flag | Category | Tools Affected |
|------|----------|----------------|
| `--disable-search` | Search | Dashboard search |
| `--disable-datasource` | Datasources | List/get datasources |
| `--disable-dashboard` | Dashboard | Dashboard CRUD operations |
| `--disable-prometheus` | Prometheus | PromQL queries and metadata |
| `--disable-loki` | Loki | LogQL queries and metadata |
| `--disable-pyroscope` | Pyroscope | Profile queries and metadata |
| `--disable-incident` | Incident | Incident management |
| `--disable-sift` | Sift | Sift investigations |
| `--disable-alerting` | Alerting | Alert rules and contact points |
| `--disable-oncall` | OnCall | OnCall schedules and users |
| `--disable-admin` | Admin | Teams and users |
| `--disable-navigation` | Navigation | Deeplink generation |
| `--disable-asserts` | Asserts | Asserts plugin integration |

### Enable Specific Tools Only

```bash
# Enable only specific tools
mcp-grafana --enabled-tools list_dashboards,get_dashboard_by_uid,query_prometheus
```

### Why Disable Tools?

- **Reduce context window usage** - Fewer tools mean less metadata sent to the AI
- **Security** - Limit access to specific Grafana features
- **Performance** - Skip initialization of unused clients
- **Simplicity** - Focus on specific use cases

## Health Check Endpoint

When using SSE or streamable-http transports, a health check endpoint is available at `/healthz`.

### Check Health

```bash
curl http://localhost:8000/healthz
# Response: ok (HTTP 200)
```

### Use Cases

- Kubernetes liveness/readiness probes
- Load balancer health checks
- Monitoring systems
- Docker healthchecks

### Kubernetes Example

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8000
  initialDelaySeconds: 5
  periodSeconds: 10
```

> **Note:** The health check endpoint is only available for SSE and streamable-http transports, not for stdio.

## Advanced Configuration Examples

### Production Setup with All Features

```bash
mcp-grafana \
  -t streamable-http \
  --address :8443 \
  --server.tls-cert-file /certs/server.crt \
  --server.tls-key-file /certs/server.key \
  --tls-cert-file /certs/client.crt \
  --tls-key-file /certs/client.key \
  --tls-ca-file /certs/ca.crt \
  --debug
```

### Minimal Setup for Dashboard Queries Only

```bash
mcp-grafana \
  --disable-incident \
  --disable-oncall \
  --disable-sift \
  --disable-alerting \
  --disable-admin
```

### Development Setup with Debug

```bash
export GRAFANA_URL=http://localhost:3000
export GRAFANA_USERNAME=admin
export GRAFANA_PASSWORD=admin

mcp-grafana --debug
```

## Troubleshooting Configuration

### Common Issues

**"Error: spawn mcp-grafana ENOENT"**
- Use the full path to the binary in your client configuration
- Ensure the binary is in your PATH

**"Connection refused"**
- Verify GRAFANA_URL is correct
- Check if Grafana is running and accessible
- Verify firewall rules

**"Authentication failed"**
- Check your service account token is valid
- Verify the token hasn't expired
- Ensure the service account has necessary permissions

**"TLS handshake failed"**
- Verify certificate paths are correct
- Check certificate validity
- Ensure CA certificate matches the server certificate

For more troubleshooting help, see the [Troubleshooting Guide](TROUBLESHOOTING.md).

## Next Steps

- [Explore available features](FEATURES.md)
- [Review tool reference](TOOLS_REFERENCE.md)
- [Set up RBAC permissions](RBAC.md)
- [Check out examples](examples/)