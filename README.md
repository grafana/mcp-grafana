# Grafana MCP server

A [Model Context Protocol][mcp] (MCP) server for Grafana.

This provides access to your Grafana instance and the surrounding ecosystem.

## Features

- [x] Search for dashboards
- [x] Dashboards
  - [x] Get dashboard by UID
  - [x] Update or create a dashboard (DISCLAIMER: Be careful with context windows. See https://github.com/grafana/mcp-grafana/issues/101 for details)
- [x] List and fetch datasource information
- [ ] Query datasources
  - [x] Prometheus
  - [x] Loki
    - [x] Log queries
    - [x] Metric queries
  - [ ] Tempo
  - [ ] Pyroscope
- [x] Query Prometheus metadata
  - [x] Metric metadata
  - [x] Metric names
  - [x] Label names
  - [x] Label values
- [x] Query Loki metadata
  - [x] Label names
  - [x] Label values
  - [x] Stats
- [x] Search, create, update and close incidents
- [x] Start Sift investigations and view the results
  - [x] Create Investigations
  - [x] List Investigations with a limit parameter
  - [x] Get Investigation
  - [x] Get Analyses
  - [x] Find error patterns in logs using Sift
  - [x] Find slow requests using Sift
  - [ ] Add tools on the other Sift Checks
- [ ] Alerting
  - [x] List and fetch alert rule information
  - [x] Get alert rule statuses (firing/normal/error/etc.)
  - [ ] Create and change alert rules
  - [x] List contact points
  - [ ] Create and change contact points
- [x] Access Grafana OnCall functionality
  - [x] List and manage schedules
  - [x] Get shift details
  - [x] Get current on-call users
  - [x] List teams and users
  - [ ] List alert groups

The list of tools is configurable, so you can choose which tools you want to make available to the MCP client.
This is useful if you don't use certain functionality or if you don't want to take up too much of the context window.
To disable a category of tools, use the `--disable-<category>` flag when starting the server. For example, to disable
the OnCall tools, use `--disable-oncall`.

### Tools

| Tool                              | Category    | Description                                                        |
|-----------------------------------|-------------|--------------------------------------------------------------------|
| `search_dashboards`               | Search      | Search for dashboards                                              |
| `get_dashboard_by_uid`            | Dashboard   | Get a dashboard by uid                                             |
| `update_dashboard`                | Dashboard   | Update or create a new dashboard                                   |
| `list_datasources`                | Datasources | List datasources                                                   |
| `get_datasource_by_uid`           | Datasources | Get a datasource by uid                                            |
| `get_datasource_by_name`          | Datasources | Get a datasource by name                                           |
| `query_prometheus`                | Prometheus  | Execute a query against a Prometheus datasource                    |
| `list_prometheus_metric_metadata` | Prometheus  | List metric metadata                                               |
| `list_prometheus_metric_names`    | Prometheus  | List available metric names                                        |
| `list_prometheus_label_names`     | Prometheus  | List label names matching a selector                               |
| `list_prometheus_label_values`    | Prometheus  | List values for a specific label                                   |
| `list_incidents`                  | Incident    | List incidents in Grafana Incident                                 |
| `create_incident`                 | Incident    | Create an incident in Grafana Incident                             |
| `add_activity_to_incident`        | Incident    | Add an activity item to an incident in Grafana Incident            |
| `resolve_incident`                | Incident    | Resolve an incident in Grafana Incident                            |
| `query_loki_logs`                 | Loki        | Query and retrieve logs using LogQL (either log or metric queries) |
| `list_loki_label_names`           | Loki        | List all available label names in logs                             |
| `list_loki_label_values`          | Loki        | List values for a specific log label                               |
| `query_loki_stats`                | Loki        | Get statistics about log streams                                   |
| `list_alert_rules`                | Alerting    | List alert rules                                                   |
| `get_alert_rule_by_uid`           | Alerting    | Get alert rule by UID                                              |
| `list_oncall_schedules`           | OnCall      | List schedules from Grafana OnCall                                 |
| `get_oncall_shift`                | OnCall      | Get details for a specific OnCall shift                            |
| `get_current_oncall_users`        | OnCall      | Get users currently on-call for a specific schedule                |
| `list_oncall_teams`               | OnCall      | List teams from Grafana OnCall                                     |
| `list_oncall_users`               | OnCall      | List users from Grafana OnCall                                     |
| `get_investigation`               | Sift        | Retrieve an existing Sift investigation by its UUID                |
| `get_analysis`                    | Sift        | Retrieve a specific analysis from a Sift investigation             |
| `list_investigations`             | Sift        | Retrieve a list of Sift investigations with an optional limit      |
| `find_error_pattern_logs`         | Sift        | Finds elevated error patterns in Loki logs.                        |
| `find_slow_requests`              | Sift        | Finds slow requests from the relevant tempo datasources.           |

## Usage

1. Create a service account in Grafana with enough permissions to use the tools you want to use,
   generate a service account token, and copy it to the clipboard for use in the configuration file.
   Follow the [Grafana documentation][service-account] for details.

2. You have several options to install `mcp-grafana`:

   * **Docker image**: Use the pre-built Docker image from Docker Hub:

     ```bash
     docker pull mcp/grafana
     docker run -p 8000:8000 -e GRAFANA_URL=http://localhost:3000 -e GRAFANA_API_KEY=<your service account token> mcp/grafana
     ```

   * **Download binary**: Download the latest release of `mcp-grafana` from the [releases page](https://github.com/grafana/mcp-grafana/releases) and place it in your `$PATH`.

   * **Build from source**: If you have a Go toolchain installed you can also build and install it from source, using the `GOBIN` environment variable
     to specify the directory where the binary should be installed. This should also be in your `PATH`.

     ```bash
     GOBIN="$HOME/go/bin" go install github.com/grafana/mcp-grafana/cmd/mcp-grafana@latest
     ```

3. Add the server configuration to your client configuration file. For example, for Claude Desktop:

   **If using the binary:**
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

   **If using Docker:**
   ```json
   {
     "mcpServers": {
       "grafana": {
         "command": "docker",
         "args": [
           "run",
           "--rm",
           "-p",
           "8000:8000",
           "-e",
           "GRAFANA_URL",
           "-e",
           "GRAFANA_API_KEY",
           "mcp/grafana"
         ],
         "env": {
           "GRAFANA_URL": "http://localhost:3000",
           "GRAFANA_API_KEY": "<your service account token>"
         }
       }
     }
   }
   ```

> Note: if you see `Error: spawn mcp-grafana ENOENT` in Claude Desktop, you need to specify the full path to `mcp-grafana`.


**Using VSCode with remote MCP server**

Make sure your `.vscode/settings.json` includes:

```json
"mcp": {
  "servers": {
    "grafana": {
      "type": "sse",
      "url": "http://localhost:8000/sse"
    }
  }
}
```

### Debug Mode

You can enable debug mode for the Grafana transport by adding the `-debug` flag to the command. This will provide detailed logging of HTTP requests and responses between the MCP server and the Grafana API, which can be helpful for troubleshooting.

To use debug mode with the Claude Desktop configuration, update your config as follows:

**If using the binary:**
```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": ["-debug"],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_API_KEY": "<your service account token>"
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
        "-p",
        "8000:8000",
        "-e",
        "GRAFANA_URL",
        "-e",
        "GRAFANA_API_KEY",
        "mcp/grafana",
        "-debug"
      ],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_API_KEY": "<your service account token>"
      }
    }
  }
}
```

## Development

Contributions are welcome! Please open an issue or submit a pull request if you have any suggestions or improvements.

This project is written in Go. Install Go following the instructions for your platform.

To run the server, use:

```bash
make run
```

You can also run the server using the SSE transport inside a custom built Docker image. To build the image, use

```
make build-image
```

And to run the image, use:

```
docker run -it --rm -p 8000:8000 mcp-grafana:latest
```

### Testing

There are three types of tests available:

1. Unit Tests (no external dependencies required):
```bash
make test-unit
```

You can also run unit tests with:
```bash
make test
```

2. Integration Tests (requires docker containers to be up and running):
```bash
make test-integration
```

3. Cloud Tests (requires cloud Grafana instance and credentials):
```bash
make test-cloud
```
> Note: Cloud tests are automatically configured in CI. For local development, you'll need to set up your own Grafana Cloud instance and credentials.

More comprehensive integration tests will require a Grafana instance to be running locally on port 3000; you can start one with Docker Compose:

```bash
docker-compose up -d
```

The integration tests can be run with:

```bash
make test-all
```

If you're adding more tools, please add integration tests for them. The existing tests should be a good starting point.

### Linting

To lint the code, run:

```bash
make lint
```

This includes a custom linter that checks for unescaped commas in `jsonschema` struct tags. The commas in `description` fields must be escaped with `\\,` to prevent silent truncation. You can run just this linter with:

```bash
make lint-jsonschema
```

See the [JSONSchema Linter documentation](internal/linter/jsonschema/README.md) for more details.

## License

This project is licensed under the [Apache License, Version 2.0](LICENSE).

[mcp]: https://modelcontextprotocol.io/
[service-account]: https://grafana.com/docs/grafana/latest/administration/service-accounts/
