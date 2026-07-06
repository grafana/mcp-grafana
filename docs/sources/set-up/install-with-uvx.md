---
title: Install with uvx
menuTitle: Install with uvx
description: Run the Grafana MCP server with uvx for a zero-install setup.
keywords:
  - uvx
  - uv
  - MCP
  - quick start
weight: 1
aliases: []
---

# Install with uvx

[`uv`](https://docs.astral.sh/uv/) is Astral's Python package manager and toolchain. [`uvx`](https://docs.astral.sh/uv/guides/tools/) runs a command from a published package in an isolated environment, without installing that package globally. It is similar to `npx` for Node.js.

Use `uvx` to run the Grafana MCP server without installing a release binary yourself.

## What you'll achieve

You add the server to your MCP client configuration (for example, Claude Desktop or Cursor) and connect to Grafana using a URL and a service account token.

## Before you begin

- Install [`uv`](https://docs.astral.sh/uv/getting-started/installation/) and ensure `uvx` is in your `$PATH`.
- Have a Grafana instance (Grafana 9.0 or later) and a [service account token](https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana).

## Add the server to your MCP client

Add the following to your MCP client configuration file. Replace `http://localhost:3000` with your Grafana URL if it is different, and `YOUR_SERVICE_ACCOUNT_TOKEN` with your service account token.

```json
{
  "mcpServers": {
    "grafana": {
      "command": "uvx",
      "args": ["mcp-grafana"],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<YOUR_SERVICE_ACCOUNT_TOKEN>"
      }
    }
  }
}
```

For Grafana Cloud, set `GRAFANA_URL` to your instance URL, for example `https://myinstance.grafana.net`.

## Next steps

- [Install with Docker](../install-with-docker/) if you prefer to run the server in a container.
- [Configure authentication](../../configure/authentication/) for service accounts and RBAC.
