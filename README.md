# Grafana MCP server

A [Model Context Protocol][mcp] (MCP) server for Grafana. 

This provides access to your Grafana instance and the surrounding ecosystem.

## Features

- [x] Search for dashboards
- [ ] List and fetch datasource information
- [ ] Query datasources
  - [ ] Prometheus
  - [ ] Loki (log queries, metric queries)
  - [ ] Tempo
  - [ ] Pyroscope
- [ ] Query Prometheus metadata
  - [ ] Metric names
  - [ ] Label names
  - [ ] Label values
- [ ] Search, create, update and close incidents, including attaching notes and other activity
- [ ] Start Sift investigations and view the results

The list of tools is configurable, so you can choose which tools you want to make available to the MCP client.
This is useful if you don't use certain functionality or if you don't want to take up too much of the context window.

### Tools

| Tool | Description |
| --- | --- |
| `search_dashboards` | Search for dashboards |

## Usage

1. Create a service account in Grafana with enough permissions to use the tools you want to use,
generate a service account token, and copy it to the clipboard for use in the configuration file.
Follow the [Grafana documentation][service-account] for details.

2. Add the server configuration to your client configuration file. For example, for Claude Desktop:

```json
{
  "mcpServers": {
    "grafana": {
      "command": "uvx",
      "args": [
        "mcp-grafana"
      ],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_API_KEY": "<your service account token>"
      }
    }
  }
}
```

To disable a set of tools, set the environment variable `GRAFANA_TOOLS__<TOOL>__ENABLED` to `"false"`.
For example, to disable the dashboard search tool, set `"GRAFANA_TOOLS__SEARCH__ENABLED": "false"`.

[mcp]: https://modelcontextprotocol.io/
[service-account]: https://grafana.com/docs/grafana/latest/administration/service-accounts/
