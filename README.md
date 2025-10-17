# Grafana MCP server

[![Unit Tests](https://github.com/grafana/mcp-grafana/actions/workflows/unit.yml/badge.svg)](https://github.com/grafana/mcp-grafana/actions/workflows/unit.yml)
[![Integration Tests](https://github.com/grafana/mcp-grafana/actions/workflows/integration.yml/badge.svg)](https://github.com/grafana/mcp-grafana/actions/workflows/integration.yml)
[![E2E Tests](https://github.com/grafana/mcp-grafana/actions/workflows/e2e.yml/badge.svg)](https://github.com/grafana/mcp-grafana/actions/workflows/e2e.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/grafana/mcp-grafana.svg)](https://pkg.go.dev/github.com/grafana/mcp-grafana)
[![MCP Catalog](https://archestra.ai/mcp-catalog/api/badge/quality/grafana/mcp-grafana)](https://archestra.ai/mcp-catalog/grafana__mcp-grafana)

A [Model Context Protocol][mcp] (MCP) server that provides AI assistants with seamless access to your Grafana instance and its ecosystem. Enable your AI to query metrics, search dashboards, manage incidents, investigate issues with Sift, and more—all through natural language.

## Quick Start

### Prerequisites

- **Grafana 9.0+** (required for full functionality)
- A Grafana instance (local or Grafana Cloud)
- Service account token or username/password credentials

### Installation

Choose your preferred installation method:

**Using Docker (recommended):**
```bash
docker pull mcp/grafana
docker run --rm -i \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t stdio
```

**Using Go:**
```bash
go install github.com/grafana/mcp-grafana/cmd/mcp-grafana@latest
```

**Download Binary:**
Download from the [releases page](https://github.com/grafana/mcp-grafana/releases)

### Basic Configuration

Add to your MCP client configuration (e.g., Claude Desktop):

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

For Docker, VSCode, Kubernetes, and other setups, see the [Configuration Guide](docs/CONFIGURATION.md).

## Features

This MCP server provides AI assistants with powerful Grafana capabilities:

- **📊 Dashboards** - Search, retrieve, update, and create dashboards with context-aware operations
- **🔌 Datasources** - List and query datasources (Prometheus, Loki, Pyroscope)
- **📈 Prometheus** - Execute PromQL queries and retrieve metric metadata
- **📝 Loki** - Query logs with LogQL and retrieve log metadata
- **🚨 Incidents** - Search, create, and manage incidents in Grafana Incident
- **🔍 Sift Investigations** - Detect error patterns and slow requests automatically
- **⚠️ Alerting** - View alert rules and notification contact points
- **📞 Grafana OnCall** - Manage schedules, shifts, and view who's on-call
- **👥 Admin** - List teams and users in your Grafana organization
- **🔗 Navigation** - Generate accurate deeplinks to dashboards, panels, and Explore

See the [Features Guide](docs/FEATURES.md) for detailed descriptions and examples.

## Documentation

### Getting Started
- **[Installation Guide](docs/INSTALLATION.md)** - All installation methods (binary, Docker, Kubernetes, from source)
- **[Configuration Guide](docs/CONFIGURATION.md)** - Client setup, authentication, debug mode, TLS configuration

### Reference
- **[Features & Capabilities](docs/FEATURES.md)** - Detailed feature descriptions and use cases
- **[Tools Reference](docs/TOOLS_REFERENCE.md)** - Complete list of available tools and CLI flags
- **[RBAC & Permissions](docs/RBAC.md)** - Required permissions and scopes for each tool

### Operations
- **[Troubleshooting](docs/TROUBLESHOOTING.md)** - Common issues and solutions
- **[Development Guide](docs/DEVELOPMENT.md)** - Contributing, testing, and linting

### Examples
- [Claude Desktop](docs/examples/claude-desktop.md)
- [VSCode](docs/examples/vscode.md)
- [Docker](docs/examples/docker.md)
- [Kubernetes](docs/examples/kubernetes.md)

## Support & Community

- **Issues:** [GitHub Issues](https://github.com/grafana/mcp-grafana/issues)
- **Discussions:** [GitHub Discussions](https://github.com/grafana/mcp-grafana/discussions)
- **Documentation:** [Grafana Docs](https://grafana.com/docs/)

## Contributing

Contributions are welcome! Please see the [Development Guide](docs/DEVELOPMENT.md) for details on:
- Setting up your development environment
- Running tests
- Code standards and linting
- Submitting pull requests

## License

This project is licensed under the [Apache License, Version 2.0](LICENSE).

[mcp]: https://modelcontextprotocol.io/