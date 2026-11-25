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

VS Code reads MCP servers from a config file, not from `settings.json`. There are two locations:

| Scope                 | Path                                                                                                    |
| :-------------------- | :------------------------------------------------------------------------------------------------------ |
| User (all workspaces) | `~/.copilot/mcp-config.json` (or `$COPILOT_HOME/mcp-config.json`)                                       |
| Workspace             | `.mcp.json` in the workspace root (requires the `chat.mcp.workspaceRootConfig.enabled` setting) |

Put your service account token in the user file, not in a workspace file you might commit. You can also open **Chat: Open Customizations** from the Command Palette and choose **MCP Servers** to add or edit servers.

### Run the server over stdio

VS Code starts `mcp-grafana` for you:

```json
{
  "mcpServers": {
    "grafana": {
      "type": "stdio",
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      },
      "tools": ["*"]
    }
  }
}
```

Or with Docker:

```json
{
  "mcpServers": {
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
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      },
      "tools": ["*"]
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
  "mcpServers": {
    "grafana": {
      "type": "http",
      "url": "http://localhost:8000/mcp",
      "tools": ["*"]
    }
  }
}
```

The legacy SSE transport (`--transport sse`, URL `http://localhost:8000/sse`, `"type": "sse"`) also works.

### Older `mcp.json` format

Earlier VS Code versions use `.vscode/mcp.json` (or **MCP: Open User Configuration**) with a top-level `servers` key instead of `mcpServers`, and support `inputs` for prompting for the token. VS Code has deprecated this format. If you already have one, the server entries above work there with `servers` as the key. See the [VS Code MCP documentation](https://code.visualstudio.com/docs/copilot/customization/mcp-servers) for details.

## Debug mode

Start the server with debug logging:

```bash
mcp-grafana --transport streamable-http --address localhost:8000 -debug
```

## Verify configuration

1. Run **MCP: List Servers** from the Command Palette and check that **grafana** is listed.
2. Open Copilot Chat (Ctrl+Alt+I) and switch to **Agent** mode.
3. Send a message. VS Code starts MCP servers when a chat session begins, so starting **grafana** before you've sent a message fails with "Cannot start an MCP server before the session runtime has been created".
4. Open the tools picker and check that the Grafana tools are enabled, then ask: "List my Grafana dashboards".

## Troubleshooting

**Server not connecting:**

- For stdio, check that `mcp-grafana` is on your `PATH` or use its full path in `command`
- For HTTP, verify the server is running and the URL path matches the transport (`/mcp` for streamable HTTP, `/sse` for SSE)
- Send a chat message first, then run **MCP: List Servers**, select **grafana**, and choose **Show Output**

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
