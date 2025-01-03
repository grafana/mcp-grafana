# Grafana MCP server

A [Model Context Protocol][mcp] (MCP) server for Grafana. 

This provides access to your Grafana instance and the surrounding ecosystem.

## Features

- [x] Search for dashboards
- [x] List and fetch datasource information
- [ ] Query datasources
  - [ ] Prometheus
  - [ ] Loki (log queries, metric queries)
  - [ ] Tempo
  - [ ] Pyroscope
- [ ] Query Prometheus metadata
  - [ ] Metric names
  - [ ] Label names
  - [ ] Label values
- [x] Search, create, update and close incidents
- [ ] Start Sift investigations and view the results

The list of tools is configurable, so you can choose which tools you want to make available to the MCP client.
This is useful if you don't use certain functionality or if you don't want to take up too much of the context window.

### Tools

| Tool | Category | Description |
| --- | --- | --- |
| `search_dashboards` | Search | Search for dashboards |
| `list_datasources` | Datasources | List datasources |
| `get_datasource_by_uid` | Datasources | Get a datasource by uid |
| `get_datasource_by_name` | Datasources | Get a datasource by name |
| `list_incidents` | Incidents | List incidents in Grafana Incident |
| `create_incident` | Incidents | Create an incident in Grafana Incident |
| `add_activity_to_incident` | Incidents | Add an activity item to an incident in Grafana Incident |
| `resolve_incident` | Incidents | Resolve an incident in Grafana Incident |

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

To disable a category of tools, set the environment variable `GRAFANA_TOOLS__<TOOL>__ENABLED` to `"false"`.
For example, to disable the search tools, set `"GRAFANA_TOOLS__SEARCH__ENABLED": "false"`.

[mcp]: https://modelcontextprotocol.io/
[service-account]: https://grafana.com/docs/grafana/latest/administration/service-accounts/
