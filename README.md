# Grafana MCP server

A [Model Context Protocol][mcp] (MCP) server for Grafana.

This provides access to your Grafana instance and the surrounding ecosystem.

## Features

- [x] Search for dashboards
- [x] List and fetch datasource information
- [ ] Query datasources
  - [x] Prometheus
  - [ ] Loki (log queries, metric queries)
  - [ ] Tempo
  - [ ] Pyroscope
- [x] Query Prometheus metadata
  - [x] Metric metadata
  - [x] Metric names
  - [x] Label names
  - [x] Label values
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
| `query_prometheus` | Prometheus | Execute a query against a Prometheus datasource |
| `list_prometheus_metric_metadata` | Prometheus | List metric metadata |
| `list_prometheus_metric_names` | Prometheus | List available metric names |
| `list_prometheus_label_names` | Prometheus | List label names matching a selector |
| `list_prometheus_label_values` | Prometheus | List values for a specific label |
| `list_incidents` | Incident | List incidents in Grafana Incident |
| `create_incident` | Incident | Create an incident in Grafana Incident |
| `add_activity_to_incident` | Incident | Add an activity item to an incident in Grafana Incident |
| `resolve_incident` | Incident | Resolve an incident in Grafana Incident |

## Usage

1. Create a service account in Grafana with enough permissions to use the tools you want to use,
   generate a service account token, and copy it to the clipboard for use in the configuration file.
   Follow the [Grafana documentation][service-account] for details.

2. Download the latest release of `mcp-grafana` from the [releases page](https://github.com/grafana/mcp-grafana/releases) and place it in your `$PATH`.

   If you have a Go toolchain installed you can also build and install it from source, using the `GOBIN` environment variable
   to specify the directory where the binary should be installed. This should also be in your `PATH`.

   ```bash
   GOBIN="$HOME/go/bin" go install github.com/grafana/mcp-grafana/cmd/mcp-grafana@latest
   ```

3. Add the server configuration to your client configuration file. For example, for Claude Desktop:

   ```json
   {
     "mcpServers": {
       "grafana": {
         "command": "mcp-grafana",
         "args": [],
         "env": {
           "GRAFANA_URL": "http://localhost:3000",
           "GRAFANA_API_KEY": "<your service account token>"
         }
       }
     }
   }
   ```

> Note: if you see `Error: spawn mcp-grafana ENOENT` in Claude Desktop, you need to specify the full path to `mcp-grafana`.

## Development

Contributions are welcome! Please open an issue or submit a pull request if you have any suggestions or improvements.

This project is written in Go. Install Go following the instructions for your platform.

To run the server, use:

```bash
make run
```

You can also run the server using the SSE transport inside Docker. To build the image, use

```
make build-image
```

And to run the image, use:

```
docker run -it --rm -p 8000:8000 mcp-grafana:latest
```

### Testing

To run unit tests, run:

```bash
make test
```

**TODO: add integration tests and cloud tests.**

More comprehensive integration tests will require a Grafana instance to be running locally on port 3000; you can start one with Docker Compose:

```bash
docker-compose up -d
```

The integration tests can be run with:

```bash
uv run pytest --integration tests
```

If you're adding more tools, please add integration tests for them. The existing tests should be a good starting point.

Certain tools use functionality that is only available in Grafana Cloud. Such tests should use the `mark_cloud` Pytest mark; see the [incident_test.py](tests/tools/incident_test.py) file for an example.
Use the `GRAFANA_URL` and `GRAFANA_API_KEY` environment variables to configure the Grafana instance to use for testing, and run these tests with:

```bash
GRAFANA_URL=https://myinstance.grafana.net GRAFANA_API_KEY=my-api-key uv run pytest --cloud tests
```

### Linting

To lint the code, run:

```bash
make lint
```

## License

This project is licensed under the [Apache License, Version 2.0](LICENSE).

[mcp]: https://modelcontextprotocol.io/
[service-account]: https://grafana.com/docs/grafana/latest/administration/service-accounts/
