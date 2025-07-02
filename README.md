# Grafana MCP server

A [Model Context Protocol][mcp] (MCP) server for Grafana.

This provides access to your Grafana instance and the surrounding ecosystem.

## Features

_The following features are currently available in MCP server. This list is for informational purposes only and does not represent a roadmap or commitment to future features._

### Dashboards
- **Search for dashboards:** Find dashboards by title or other metadata
- **Get dashboard by UID:** Retrieve full dashboard details using its unique identifier
- **Smart dashboard update:** Intelligently update dashboards with automatic GitOps detection - uses file management for provisioned dashboards and direct API for regular dashboards. _Note: Use with caution due to context window limitations; see [issue #101](https://github.com/grafana/mcp-grafana/issues/101)_
- **Get panel queries and datasource info:** Get the title, query string, and datasource information (including UID and type, if available) from every panel in a dashboard
- **Get dashboard manager:** Retrieve GitOps management information for dashboards to understand their source control setup

### Datasources
- **List and fetch datasource information:** View all configured datasources and retrieve detailed information about each.
    - _Supported datasource types: Prometheus, Loki._

### Prometheus Querying
- **Query Prometheus:** Execute PromQL queries (supports both instant and range metric queries) against Prometheus datasources.
- **Query Prometheus metadata:** Retrieve metric metadata, metric names, label names, and label values from Prometheus datasources.

### Loki Querying
- **Query Loki logs and metrics:** Run both log queries and metric queries using LogQL against Loki datasources.
- **Query Loki metadata:** Retrieve label names, label values, and stream statistics from Loki datasources.

### Incidents
- **Search, create, update, and close incidents:** Manage incidents in Grafana Incident, including searching, creating, updating, and resolving incidents.

### Sift Investigations
- **Create Sift investigations:** Start a new Sift investigation for analyzing logs or traces.
- **List Sift investigations:** Retrieve a list of Sift investigations, with support for a limit parameter.
- **Get Sift investigation:** Retrieve details of a specific Sift investigation by its UUID.
- **Get Sift analyses:** Retrieve a specific analysis from a Sift investigation.
- **Find error patterns in logs:** Detect elevated error patterns in Loki logs using Sift.
- **Find slow requests:** Detect slow requests using Sift (Tempo).

### Alerting
- **List and fetch alert rule information:** View alert rules and their statuses (firing/normal/error/etc.) in Grafana.
- **List contact points:** View configured notification contact points in Grafana.

### Grafana OnCall
- **List and manage schedules:** View and manage on-call schedules in Grafana OnCall.
- **Get shift details:** Retrieve detailed information about specific on-call shifts.
- **Get current on-call users:** See which users are currently on call for a schedule.
- **List teams and users:** View all OnCall teams and users.

### Admin
- **List teams:** View all configured teams in Grafana.

### GitOps and Provisioning
- **Repository Management:** List, inspect, and manage Git repositories configured for GitOps workflows
- **Branch Operations:** List and filter repository branches and refs for different configuration versions
- **File Operations:** Create, read, update, and delete configuration files in repositories with full version control
- **File History:** View Git commit history for specific files to track configuration evolution
- **Pull Request Management:** Automatically create pull requests for managed repositories to enable code review workflows
- **Manual GitHub PR Creation:** Open GitHub pull request creation pages in the browser for provisioning repositories with pre-filled title, body, and branch information
- **Dashboard Manager Integration:** Understand how dashboards are managed through GitOps and locate their source files

The GitOps features enable complete Infrastructure-as-Code workflows for Grafana configurations, including dashboards, datasources, alerting rules, and more. All changes are tracked in Git with full audit trails, rollback capabilities, and collaborative code review processes.

**Smart Dashboard Management:** The `update_dashboard` tool automatically detects whether a dashboard is managed through GitOps or not:
- **Provisioned dashboards:** Automatically routes updates through the Git repository using file management, ensuring changes are version-controlled and maintaining GitOps compliance
- **Regular dashboards:** Uses the standard Grafana API for direct updates
- **New dashboards:** Always created using the standard API, can later be moved to GitOps management

This intelligent routing ensures that your GitOps workflows are preserved while providing a unified interface for dashboard management.

The list of tools is configurable, so you can choose which tools you want to make available to the MCP client.
This is useful if you don't use certain functionality or if you don't want to take up too much of the context window.
To disable a category of tools, use the `--disable-<category>` flag when starting the server. For example, to disable
the OnCall tools, use `--disable-oncall`.

### Tools

| Tool                              | Category    | Description                                                        |
| --------------------------------- | ----------- | ------------------------------------------------------------------ |
| `list_teams`                      | Admin       | List all teams                                                     |
| `search_dashboards`               | Search      | Search for dashboards                                              |
| `get_dashboard_by_uid`            | Dashboard   | Get a dashboard by uid                                             |
| `update_dashboard`                | Dashboard   | Update or create a new dashboard                                   |
| `get_dashboard_panel_queries`     | Dashboard   | Get panel title, queries, datasource UID and type from a dashboard |
| `get_dashboard_manager`           | Dashboard   | Get dashboard manager details and GitOps source information        |
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
| `list_pyroscope_label_names`      | Pyroscope   | List label names matching a selector                               |
| `list_pyroscope_label_values`     | Pyroscope   | List label values matching a selector for a label name             |
| `list_pyroscope_profile_types`    | Pyroscope   | List available profile types                                       |
| `fetch_pyroscope_profile`         | Pyroscope   | Fetches a profile in DOT format for analysis                       |
| `list_provisioning_repositories`  | GitSync     | List Git repositories configured for GitOps management             |
| `list_provisioning_repository_branches` | GitSync | List branches and refs in a specific repository                    |
| `get_provisioning_repository`     | GitSync     | Get detailed information about a specific repository               |
| `list_provisioning_repository_files` | GitSync  | List files in a repository with optional path filtering            |
| `get_provisioning_repository_file_content` | GitSync | Get the actual content of a file from a repository               |
| `get_provisioning_repository_file_history` | GitSync | Get Git commit history for a specific file                       |
| `manage_provisioning_repository_file` | GitSync   | Create, update, or delete files in repositories with version control |
| `manage_file` | GitSync   | Direct file management tool for GitOps workflows with automatic version control |
| `create_provisioning_repository_pr` | GitSync    | Create pull requests for managed repositories                      |
| `manual_submit_github_pull_request` | GitSync    | Open GitHub pull request creation page in browser for provisioning repositories |

## Usage

1. Create a service account in Grafana with enough permissions to use the tools you want to use,
   generate a service account token, and copy it to the clipboard for use in the configuration file.
   Follow the [Grafana documentation][service-account] for details.

2. You have several options to install `mcp-grafana`:

   - **Docker image**: Use the pre-built Docker image from Docker Hub.

     **Important**: The Docker image's entrypoint is configured to run the MCP server in SSE mode by default, but most users will want to use STDIO mode for direct integration with AI assistants like Claude Desktop:

     1. **STDIO Mode**: For stdio mode you must explicitly override the default with `-t stdio` and include the `-i` flag to keep stdin open:

     ```bash
     docker pull mcp/grafana
     docker run --rm -i -e GRAFANA_URL=http://localhost:3000 -e GRAFANA_API_KEY=<your service account token> mcp/grafana -t stdio
     ```

     2. **SSE Mode**: In this mode, the server runs as an HTTP server that clients connect to. You must expose port 8000 using the `-p` flag:

     ```bash
     docker pull mcp/grafana
     docker run --rm -p 8000:8000 -e GRAFANA_URL=http://localhost:3000 -e GRAFANA_API_KEY=<your service account token> mcp/grafana
     ```

     3. **Streamable HTTP Mode**: In this mode, the server operates as an independent process that can handle multiple client connections. You must expose port 8000 using the `-p` flag: For this mode you must explicitly override the default with `-t streamable-http`

     ```bash
     docker pull mcp/grafana
     docker run --rm -p 8000:8000 -e GRAFANA_URL=http://localhost:3000 -e GRAFANA_API_KEY=<your service account token> mcp/grafana -t streamable-http
     ```

   - **Download binary**: Download the latest release of `mcp-grafana` from the [releases page](https://github.com/grafana/mcp-grafana/releases) and place it in your `$PATH`.

   - **Build from source**: If you have a Go toolchain installed you can also build and install it from source, using the `GOBIN` environment variable
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

> Note: if you see `Error: spawn mcp-grafana ENOENT` in Claude Desktop, you need to specify the full path to `mcp-grafana`.

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
           "-e",
           "GRAFANA_URL",
           "-e",
           "GRAFANA_API_KEY",
           "mcp/grafana",
           "-t",
           "stdio"
         ],
         "env": {
           "GRAFANA_URL": "http://localhost:3000",
           "GRAFANA_API_KEY": "<your service account token>"
         }
       }
     }
   }
   ```

   > Note: The `-t stdio` argument is essential here because it overrides the default SSE mode in the Docker image.

**Using VSCode with remote MCP server**

If you're using VSCode and running the MCP server in SSE mode (which is the default when using the Docker image without overriding the transport), make sure your `.vscode/settings.json` includes the following:

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
        "-i",
        "-e",
        "GRAFANA_URL",
        "-e",
        "GRAFANA_API_KEY",
        "mcp/grafana",
        "-t",
        "stdio",
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

> Note: As with the standard configuration, the `-t stdio` argument is required to override the default SSE mode in the Docker image.

### TLS Configuration

If your Grafana instance is behind mTLS or requires custom TLS certificates, you can configure the MCP server to use custom certificates. The server supports the following TLS configuration options:

- `--tls-cert-file`: Path to TLS certificate file for client authentication
- `--tls-key-file`: Path to TLS private key file for client authentication
- `--tls-ca-file`: Path to TLS CA certificate file for server verification
- `--tls-skip-verify`: Skip TLS certificate verification (insecure, use only for testing)

**Example with client certificate authentication:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [
        "--tls-cert-file", "/path/to/client.crt",
        "--tls-key-file", "/path/to/client.key",
        "--tls-ca-file", "/path/to/ca.crt"
      ],
      "env": {
        "GRAFANA_URL": "https://secure-grafana.example.com",
        "GRAFANA_API_KEY": "<your service account token>"
      }
    }
  }
}
```

**Example with Docker:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "-v", "/path/to/certs:/certs:ro",
        "-e", "GRAFANA_URL",
        "-e", "GRAFANA_API_KEY",
        "mcp/grafana",
        "-t", "stdio",
        "--tls-cert-file", "/certs/client.crt",
        "--tls-key-file", "/certs/client.key",
        "--tls-ca-file", "/certs/ca.crt"
      ],
      "env": {
        "GRAFANA_URL": "https://secure-grafana.example.com",
        "GRAFANA_API_KEY": "<your service account token>"
      }
    }
  }
}
```

The TLS configuration is applied to all HTTP clients used by the MCP server, including:
- The main Grafana OpenAPI client
- Prometheus datasource clients
- Loki datasource clients
- Incident management clients
- Sift investigation clients
- Alerting clients
- Asserts clients

**Direct CLI Usage Examples:**

For testing with self-signed certificates:
```bash
./mcp-grafana --tls-skip-verify -debug
```

With client certificate authentication:
```bash
./mcp-grafana \
  --tls-cert-file /path/to/client.crt \
  --tls-key-file /path/to/client.key \
  --tls-ca-file /path/to/ca.crt \
  -debug
```

With custom CA certificate only:
```bash
./mcp-grafana --tls-ca-file /path/to/ca.crt
```

**Programmatic Usage:**

If you're using this library programmatically, you can also create TLS-enabled context functions:

```go
// Using struct literals
tlsConfig := &mcpgrafana.TLSConfig{
    CertFile: "/path/to/client.crt",
    KeyFile:  "/path/to/client.key",
    CAFile:   "/path/to/ca.crt",
}
grafanaConfig := mcpgrafana.GrafanaConfig{
    Debug:     true,
    TLSConfig: tlsConfig,
}
contextFunc := mcpgrafana.ComposedStdioContextFunc(grafanaConfig)

// Or inline
grafanaConfig := mcpgrafana.GrafanaConfig{
    Debug: true,
    TLSConfig: &mcpgrafana.TLSConfig{
        CertFile: "/path/to/client.crt",
        KeyFile:  "/path/to/client.key",
        CAFile:   "/path/to/ca.crt",
    },
}
contextFunc := mcpgrafana.ComposedStdioContextFunc(grafanaConfig)
```

## Development

Contributions are welcome! Please open an issue or submit a pull request if you have any suggestions or improvements.

This project is written in Go. Install Go following the instructions for your platform.

To run the server locally in STDIO mode (which is the default for local development), use:

```bash
make run
```

To run the server locally in SSE mode, use:

```bash
go run ./cmd/mcp-grafana --transport sse
```

You can also run the server using the SSE transport inside a custom built Docker image. Just like the published Docker image, this custom image's entrypoint defaults to SSE mode. To build the image, use:

```
make build-image
```

And to run the image in SSE mode (the default), use:

```
docker run -it --rm -p 8000:8000 mcp-grafana:latest
```

If you need to run it in STDIO mode instead, override the transport setting:

```
docker run -it --rm mcp-grafana:latest -t stdio
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
