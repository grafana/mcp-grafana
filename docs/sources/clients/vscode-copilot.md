---
title: VS Code and GitHub Copilot
menuTitle: VS Code Copilot
description: Set up the Grafana MCP server for VS Code with GitHub Copilot agent mode.
keywords:
  - VS Code
  - GitHub Copilot
  - MCP
  - client
weight: 4
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/clients/vscode-copilot/
---

# VS Code and GitHub Copilot

This guide helps you set up the `mcp-grafana` server for VS Code with GitHub Copilot agent mode.

## Prerequisites

- VS Code with GitHub Copilot extension
- Grafana 9.0+ with a service account token
- `mcp-grafana` binary installed

## Setup

VS Code reads MCP servers from an `mcp.json` file, not from `settings.json`. Use `.vscode/mcp.json` in your workspace, or run **MCP: Open User Configuration** from the Command Palette for a config that applies to all workspaces.

### Run the server over stdio

VS Code starts `mcp-grafana` for you and prompts for the token the first time, so it isn't stored in the file:

```json
{
  "inputs": [
    {
      "type": "promptString",
      "id": "grafana-token",
      "description": "Grafana service account token",
      "password": true
    }
  ],
  "servers": {
    "grafana": {
      "type": "stdio",
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "${input:grafana-token}"
      }
    }
  }
}
```

Or with Docker:

```json
{
  "inputs": [
    {
      "type": "promptString",
      "id": "grafana-token",
      "description": "Grafana service account token",
      "password": true
    }
  ],
  "servers": {
    "grafana": {
      "type": "stdio",
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
        "GRAFANA_URL": "http://host.docker.internal:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "${input:grafana-token}"
      }
    }
  }
}
```

### Connect to a running server

To share one server between clients, or run it as a service, start it with the streamable HTTP transport:

```bash
export GRAFANA_URL="http://localhost:3000"
export GRAFANA_SERVICE_ACCOUNT_TOKEN="<your-token>"
mcp-grafana --transport streamable-http --address localhost:8000
```

Then point VS Code at it:

```json
{
  "servers": {
    "grafana": {
      "type": "http",
      "url": "http://localhost:8000/mcp"
    }
  }
}
```

The legacy SSE transport (`--transport sse`, URL `http://localhost:8000/sse`, `"type": "sse"`) also works.

{{< admonition type="note" >}}
VS Code is moving to a newer configuration format (`~/.copilot/mcp-config.json` for the user and `.mcp.json` for the workspace) that uses a top-level `mcpServers` key instead of `servers`. It has no `inputs`, so use environment variables for credentials. Check the [VS Code MCP documentation](https://code.visualstudio.com/docs/copilot/customization/mcp-servers) for the current format.
{{< /admonition >}}

## Debug mode

Start the server with debug logging:

```bash
mcp-grafana --transport streamable-http --address localhost:8000 -debug
```

## Verify configuration

1. Run **MCP: List Servers** from the Command Palette and check that **grafana** is running. Select it to start it or show its output.
2. Open Copilot Chat (Ctrl+Alt+I) and switch to **Agent** mode.
3. Open the tools picker and check that the Grafana tools are enabled.
4. Ask: "List my Grafana dashboards"

## Troubleshooting

**Server not connecting:**

- For stdio, check that `mcp-grafana` is on your `PATH` or use its full path in `command`
- For HTTP, verify the server is running and the URL path matches the transport (`/mcp` for streamable HTTP, `/sse` for SSE)
- Run **MCP: List Servers**, select **grafana**, then **Show Output**

**Tools not appearing:**

- Agent mode is required; MCP tools aren't available in Ask or Edit mode
- Check that your organization hasn't disabled MCP servers for Copilot

## Running as a service

For persistent server, create a systemd unit or launchd plist.

**Linux systemd** (`~/.config/systemd/user/mcp-grafana.service`):

```ini
[Unit]
Description=Grafana MCP Server
After=network.target

[Service]
ExecStart=/path/to/mcp-grafana --transport streamable-http --address localhost:8000
Environment=GRAFANA_URL=http://localhost:3000
Environment=GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token>
Restart=always

[Install]
WantedBy=default.target
```

Enable with:

```bash
systemctl --user enable --now mcp-grafana
```

## Read-only mode

```bash
mcp-grafana --transport streamable-http --address localhost:8000 --disable-write
```

## Next steps

- [Transports and addresses](../../configure/transports-and-addresses/) for SSE details.
- [Configure authentication](../../configure/authentication/) for Grafana credentials.
