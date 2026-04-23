---
title: Grafana MCP server
menuTitle: Grafana MCP server
description: Connect AI assistants and LLM clients to Grafana using the Model Context Protocol.
keywords:
  - MCP
  - Model Context Protocol
  - Grafana
  - AI
  - LLM
weight: 1
aliases: []
---

# Grafana MCP server

This documentation helps you install the Grafana MCP server, connect MCP-compatible clients, and configure authentication, transports, and tools.

The Grafana MCP server is a [Model Context Protocol (MCP)](https://modelcontextprotocol.io/docs/getting-started/intro) server that gives AI assistants and LLM clients access to your Grafana instance. You can query metrics and logs, search and manage dashboards, manage alert rules, work with Grafana Incident and Sift, and generate deeplinks to Grafana resources.

## Overview

Use the Grafana MCP server to let your preferred MCP-compatible client (for example, Claude Desktop, Cursor, or VS Code with Copilot) talk to Grafana. The server exposes tools for dashboards, datasources (Prometheus, Loki, and others), alerting, incidents, OnCall, and more. You configure which tools are enabled and how the server connects to Grafana (authentication and transport).

## Quick start

This quick start requires [uv](https://docs.astral.sh/uv/getting-started/installation/). Add this to your MCP client configuration (for example Claude Desktop or Cursor):

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

For Grafana Cloud, set `GRAFANA_URL` to your instance URL (for example `https://myinstance.grafana.net`). Refer to [Clients](clients/) and [Set up](set-up/) for next steps.

Grafana **9.0 or later** is required for full functionality. Some features, particularly datasource-related operations, may not work correctly with earlier versions due to missing API endpoints.

## Explore the docs

- [Clients](clients/) – Cursor, Claude Desktop, VS Code, and more.
- [Set up](set-up/) – uvx, Docker, binary, Helm, and [client configuration examples](set-up/client-configuration-examples/).
- [Configure](configure/) – Authentication, [command-line flags](configure/command-line-flags/), transports, TLS, and tools.
- [Introduction](introduction/) – MCP concepts and RBAC overview.
- [Reference](reference/) – [MCP tools reference](reference/mcp-tools-table/) (tools, permissions, scopes, and RBAC guidance).
- [Guides](guides/) – Query metrics and logs, dashboards, alerts, deeplinks, incidents.
- [Troubleshooting](troubleshooting/) – Common issues including Grafana version compatibility.
- [Developer](developer/) – Go SDK, observability, build and test.

## License

The project is licensed under the [Apache License, Version 2.0](https://github.com/grafana/mcp-grafana/blob/main/LICENSE).
