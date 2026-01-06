# Grafana MCP Server - 5-Minute Quickstart

Get the Grafana MCP server running with Claude Desktop in 5 minutes.

## Prerequisites

| Requirement | Version |
|-------------|---------|
| Grafana | 9.0+ |
| Go (source install) | 1.21+ |
| Docker (Docker install) | 20.10+ |

## Step 1: Install

Choose one method:

**Option A: Go install (recommended)**
```bash
go install github.com/grafana/mcp-grafana/cmd/mcp-grafana@latest
```

**Option B: Docker**
```bash
docker pull mcp/grafana
```

**Option C: Download binary**

Download from the [releases page](https://github.com/grafana/mcp-grafana/releases) and place in your `$PATH`.

## Step 2: Create a Grafana Service Account

1. Log into your Grafana instance
2. Navigate to **Administration → Service Accounts**
3. Click **Add service account**
4. Name it (e.g., `mcp-grafana`) and assign the **Editor** role
5. Click **Add service account token**
6. Copy the generated token immediately — it won't be shown again

## Step 3: Configure Claude Desktop

Find your config file:

| OS | Path |
|----|------|
| macOS | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Windows | `%APPDATA%\Claude\claude_desktop_config.json` |
| Linux | `~/.config/Claude/claude_desktop_config.json` |

Add this configuration:

**If using the binary:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxx"
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
        "-e", "GRAFANA_URL",
        "-e", "GRAFANA_SERVICE_ACCOUNT_TOKEN",
        "mcp/grafana",
        "-t", "stdio"
      ],
      "env": {
        "GRAFANA_URL": "http://host.docker.internal:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxx"
      }
    }
  }
}
```

> **Note for Docker users:** Use `host.docker.internal:3000` instead of `localhost:3000`. The container cannot reach the host's localhost directly. On Linux, use `--network host` instead.

**For Grafana Cloud:**

Replace the URL with your instance URL:
```
"GRAFANA_URL": "https://myinstance.grafana.net"
```

## Step 4: Verify

1. Restart Claude Desktop completely (quit and reopen)
2. Ask Claude: **"What dashboards are available in Grafana?"**

If it works, you'll see a list of your dashboards. If not, see Troubleshooting below.

## Example Queries

Once configured, try these prompts:

- "List all dashboards in my Grafana instance"
- "Show me the datasources configured in Grafana"
- "Search for dashboards containing 'kubernetes'"
- "Get the panels from dashboard with UID abc123"
- "Query Prometheus for the `up` metric over the last hour"
- "What alert rules are currently firing?"
- "Who is on-call right now?"

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `GRAFANA_URL` | Yes | Grafana instance URL |
| `GRAFANA_SERVICE_ACCOUNT_TOKEN` | Yes* | Service account token (recommended) |
| `GRAFANA_USERNAME` | No | Basic auth username (alternative to token) |
| `GRAFANA_PASSWORD` | No | Basic auth password (alternative to token) |
| `GRAFANA_ORG_ID` | No | Organization ID for multi-tenant setups |

*Either token or username/password is required.

## Troubleshooting

| Error | Cause | Solution |
|-------|-------|----------|
| `spawn mcp-grafana ENOENT` | Binary not in PATH | Use the full path to the binary, e.g., `/Users/you/go/bin/mcp-grafana` |
| `TypeError: fetch failed` | VSCode client bug with HTTP redirects | Use SSE transport instead of streamable-http, or update VSCode |
| `Server exited before responding to 'initialize' request` | Connection issue or wrong URL | Verify `GRAFANA_URL` is reachable from your machine |
| `401 Unauthorized` | Invalid or expired token | Generate a new service account token |
| `400 Bad Request: id is invalid` | Grafana version < 9.0 | Upgrade Grafana to 9.0+ |
| Empty query results | Time range issue | Check that your query time range contains data |
| `connection refused` (Docker) | localhost doesn't work in container | Use `host.docker.internal:3000` on Mac/Windows |

### Debug Mode

Add `-debug` to see detailed HTTP request/response logs:

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": ["-debug"],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxx"
      }
    }
  }
}
```

## Next Steps

- See the full [README](README.md) for all available tools and RBAC permissions
- Check [CLI Flags Reference](README.md#cli-flags-reference) for advanced configuration
- Use `--disable-write` flag for read-only mode in production environments
