# Installation Guide

This guide covers all the ways you can install and deploy the Grafana MCP server.

## Prerequisites

Before installing, ensure you have:

- **Grafana 9.0 or later** - Required for full functionality (some datasource operations require Grafana 9.0+)
- **Grafana instance access** - Either local or Grafana Cloud
- **Authentication credentials** - Service account token (recommended) or username/password

### Creating a Service Account Token

1. Navigate to your Grafana instance
2. Go to **Administration** → **Service Accounts**
3. Click **Add service account**
4. Configure the necessary permissions (see [RBAC Guide](RBAC.md))
5. Click **Add service account token**
6. Copy the token for use in configuration

> **Note:** The environment variable `GRAFANA_API_KEY` is deprecated and will be removed in a future version. Please use `GRAFANA_SERVICE_ACCOUNT_TOKEN` instead. The old variable name will continue to work for backward compatibility but will show deprecation warnings.

For detailed instructions, see the [Grafana service account documentation](https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana).

## Installation Methods

Choose the installation method that best suits your environment:

### Method 1: Docker (Recommended)

Docker is the easiest way to get started with the Grafana MCP server.

#### Pull the Image

```bash
docker pull mcp/grafana
```

#### Run in STDIO Mode (for AI Assistants)

Most users will want STDIO mode for direct integration with AI assistants like Claude Desktop:

```bash
# For local Grafana
docker run --rm -i \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t stdio

# For Grafana Cloud
docker run --rm -i \
  -e GRAFANA_URL=https://myinstance.grafana.net \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t stdio
```

> **Important:** The `-i` flag keeps stdin open, and `-t stdio` overrides the default SSE mode.

#### Run in SSE Mode (for Remote Clients)

SSE mode runs the server as an HTTP endpoint that clients connect to:

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana
```

> **Note:** SSE mode is the default when using the Docker image. You must expose port 8000 using the `-p` flag.

#### Run in Streamable HTTP Mode

Streamable HTTP mode operates as an independent process handling multiple client connections:

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t streamable-http
```

#### HTTPS with Server TLS

For HTTPS streamable HTTP mode with server TLS certificates:

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

### Method 2: Download Binary

Download pre-built binaries for your platform:

1. Visit the [releases page](https://github.com/grafana/mcp-grafana/releases)
2. Download the appropriate binary for your platform:
   - `mcp-grafana-linux-amd64` for Linux
   - `mcp-grafana-darwin-amd64` for macOS (Intel)
   - `mcp-grafana-darwin-arm64` for macOS (Apple Silicon)
   - `mcp-grafana-windows-amd64.exe` for Windows
3. Make the binary executable (Linux/macOS):
   ```bash
   chmod +x mcp-grafana
   ```
4. Move it to a directory in your `$PATH`:
   ```bash
   sudo mv mcp-grafana /usr/local/bin/
   ```

#### Verify Installation

```bash
mcp-grafana --help
```

### Method 3: Build from Source

If you have Go installed, you can build from source:

#### Prerequisites

- Go 1.21 or later
- Git

#### Build and Install

```bash
# Install to default Go bin directory
go install github.com/grafana/mcp-grafana/cmd/mcp-grafana@latest

# Or specify custom installation directory
GOBIN="$HOME/bin" go install github.com/grafana/mcp-grafana/cmd/mcp-grafana@latest
```

Make sure the installation directory is in your `$PATH`.

#### Build from Cloned Repository

```bash
# Clone the repository
git clone https://github.com/grafana/mcp-grafana.git
cd mcp-grafana

# Build
make build

# The binary will be in ./dist/mcp-grafana
```

### Method 4: Kubernetes with Helm

Deploy to Kubernetes using the official Helm chart:

#### Prerequisites

- Kubernetes cluster
- Helm 3.x installed
- `kubectl` configured

#### Install with Helm

```bash
# Add the Grafana Helm repository
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update

# Install the chart
helm install my-mcp-grafana grafana/grafana-mcp \
  --set grafana.url=<GrafanaUrl> \
  --set grafana.apiKey=<GrafanaToken>
```

#### Install with Custom Values

Create a `values.yaml` file:

```yaml
grafana:
  url: "https://myinstance.grafana.net"
  apiKey: "<your-token>"

# Optional: Configure service type
service:
  type: LoadBalancer
  port: 8000

# Optional: Configure resource limits
resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

Install with custom values:

```bash
helm install my-mcp-grafana grafana/grafana-mcp -f values.yaml
```

For more information, see the [Helm chart documentation](https://github.com/grafana/helm-charts/tree/main/charts/grafana-mcp).

## Post-Installation

After installing, you'll need to:

1. **Configure your MCP client** - See the [Configuration Guide](CONFIGURATION.md)
2. **Verify connectivity** - Test the connection to your Grafana instance
3. **Set up permissions** - Ensure your service account has the necessary RBAC permissions (see [RBAC Guide](RBAC.md))

## Verifying Installation

### Test with STDIO Mode

```bash
# Set environment variables
export GRAFANA_URL=http://localhost:3000
export GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token>

# Run the server
mcp-grafana -t stdio
```

The server should start without errors. You can test it by sending MCP protocol messages via stdin.

### Test with SSE/HTTP Mode

```bash
# Start the server
mcp-grafana -t sse

# In another terminal, check the health endpoint
curl http://localhost:8000/healthz
# Should return: ok
```

## Upgrading

### Docker

Pull the latest image:

```bash
docker pull mcp/grafana:latest
```

### Binary

Download the latest release and replace the existing binary:

```bash
# Download new version
wget https://github.com/grafana/mcp-grafana/releases/latest/download/mcp-grafana-linux-amd64
chmod +x mcp-grafana-linux-amd64
sudo mv mcp-grafana-linux-amd64 /usr/local/bin/mcp-grafana
```

### Go Install

```bash
go install github.com/grafana/mcp-grafana/cmd/mcp-grafana@latest
```

### Helm

```bash
helm repo update
helm upgrade my-mcp-grafana grafana/grafana-mcp
```

## Uninstalling

### Docker

Simply stop and remove containers. No additional cleanup needed.

### Binary

Remove the binary:

```bash
sudo rm /usr/local/bin/mcp-grafana
```

### Helm

```bash
helm uninstall my-mcp-grafana
```

## Next Steps

- [Configure your MCP client](CONFIGURATION.md)
- [Explore available features](FEATURES.md)
- [Set up RBAC permissions](RBAC.md)
- [Review troubleshooting guide](TROUBLESHOOTING.md)