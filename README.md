# Grafana MCP server

[![Unit Tests](https://github.com/grafana/mcp-grafana/actions/workflows/unit.yml/badge.svg)](https://github.com/grafana/mcp-grafana/actions/workflows/unit.yml)
[![Integration Tests](https://github.com/grafana/mcp-grafana/actions/workflows/integration.yml/badge.svg)](https://github.com/grafana/mcp-grafana/actions/workflows/integration.yml)
[![E2E Tests](https://github.com/grafana/mcp-grafana/actions/workflows/e2e.yml/badge.svg)](https://github.com/grafana/mcp-grafana/actions/workflows/e2e.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/grafana/mcp-grafana.svg)](https://pkg.go.dev/github.com/grafana/mcp-grafana)

A [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server for Grafana.

This provides access to your Grafana instance and the surrounding ecosystem.

**New to MCP or just want to get started quickly?** See the [5-Minute Quickstart Guide](QUICKSTART.md).

## Requirements

| Requirement | Version | Notes |
|-------------|---------|-------|
| Grafana | 9.0+ | Required for full functionality. Earlier versions may fail on datasource operations. |
| Go | 1.21+ | Only required if building from source |
| Docker | 20.10+ | Only required if using Docker installation |

## Example Queries

Once configured, you can ask Claude things like:

- "List all dashboards in my Grafana instance"
- "Show me the datasources configured in Grafana"
- "Search for dashboards containing 'kubernetes'"
- "Get the panels from dashboard with UID abc123"
- "Query Prometheus for the `up` metric over the last hour"
- "What alert rules are currently firing?"
- "Who is on-call right now?"
- "Create a deeplink to dashboard xyz with a 1-hour time range"

## Features

*The following features are currently available in MCP server. This list is for informational purposes only and does not represent a roadmap or commitment to future features.*

### Dashboards

* **Search for dashboards:** Find dashboards by title or other metadata
* **Get dashboard by UID:** Retrieve full dashboard details using its unique identifier. *Warning: Large dashboards can consume significant context window space.*
* **Get dashboard summary:** Get a compact overview of a dashboard including title, panel count, panel types, variables, and metadata without the full JSON to minimize context window usage
* **Get dashboard property:** Extract specific parts of a dashboard using JSONPath expressions (e.g., `$.title`, `$.panels[*].title`) to fetch only needed data and reduce context window consumption
* **Update or create a dashboard:** Modify existing dashboards or create new ones. *Warning: Requires full dashboard JSON which can consume large amounts of context window space.*
* **Patch dashboard:** Apply specific changes to a dashboard without requiring the full JSON, significantly reducing context window usage for targeted modifications
* **Get panel queries and datasource info:** Get the title, query string, and datasource information (including UID and type, if available) from every panel in a dashboard

#### Context Window Management

The dashboard tools now include several strategies to manage context window usage effectively ([issue #101](https://github.com/grafana/mcp-grafana/issues/101)):

* **Use `get_dashboard_summary`** for dashboard overview and planning modifications
* **Use `get_dashboard_property`** with JSONPath when you only need specific dashboard parts
* **Avoid `get_dashboard_by_uid`** unless you specifically need the complete dashboard JSON

### Datasources

* **List and fetch datasource information:** View all configured datasources and retrieve detailed information about each.
  + *Supported datasource types: Prometheus, Loki.*

### Prometheus Querying

* **Query Prometheus:** Execute PromQL queries (supports both instant and range metric queries) against Prometheus datasources.
* **Query Prometheus metadata:** Retrieve metric metadata, metric names, label names, and label values from Prometheus datasources.

### Loki Querying

* **Query Loki logs and metrics:** Run both log queries and metric queries using LogQL against Loki datasources.
* **Query Loki metadata:** Retrieve label names, label values, and stream statistics from Loki datasources.

### Incidents

* **Search, create, and update incidents:** Manage incidents in Grafana Incident, including searching, creating, and adding activities to incidents.

### Sift Investigations

* **List Sift investigations:** Retrieve a list of Sift investigations, with support for a limit parameter.
* **Get Sift investigation:** Retrieve details of a specific Sift investigation by its UUID.
* **Get Sift analyses:** Retrieve a specific analysis from a Sift investigation.
* **Find error patterns in logs:** Detect elevated error patterns in Loki logs using Sift.
* **Find slow requests:** Detect slow requests using Sift (Tempo).

### Alerting

* **List and fetch alert rule information:** View alert rules and their statuses (firing/normal/error/etc.) in Grafana.
* **List contact points:** View configured notification contact points in Grafana.

### Grafana OnCall

* **List and manage schedules:** View and manage on-call schedules in Grafana OnCall.
* **Get shift details:** Retrieve detailed information about specific on-call shifts.
* **Get current on-call users:** See which users are currently on call for a schedule.
* **List teams and users:** View all OnCall teams and users.
* **List alert groups:** View and filter alert groups from Grafana OnCall by various criteria including state, integration, labels, and time range.
* **Get alert group details:** Retrieve detailed information about a specific alert group by its ID.

### Admin

* **List teams:** View all configured teams in Grafana.
* **List Users:** View all users in an organization in Grafana.

### Navigation

* **Generate deeplinks:** Create accurate deeplink URLs for Grafana resources instead of relying on LLM URL guessing.
  + **Dashboard links:** Generate direct links to dashboards using their UID (e.g., `http://localhost:3000/d/dashboard-uid`)
  + **Panel links:** Create links to specific panels within dashboards with viewPanel parameter (e.g., `http://localhost:3000/d/dashboard-uid?viewPanel=5`)
  + **Explore links:** Generate links to Grafana Explore with pre-configured datasources (e.g., `http://localhost:3000/explore?left={"datasource":"prometheus-uid"}`)
  + **Time range support:** Add time range parameters to links (`from=now-1h&to=now`)
  + **Custom parameters:** Include additional query parameters like dashboard variables or refresh intervals

### Annotations

* **Get Annotations:** Query annotations with filters. Supports time range, dashboard UID, tags, and match mode.
* **Create Annotation:** Create a new annotation on a dashboard or panel.
* **Create Graphite Annotation:** Create annotations using Graphite format (`what`, `when`, `tags`, `data`).
* **Update Annotation:** Replace all fields of an existing annotation (full update).
* **Patch Annotation:** Update only specific fields of an annotation (partial update).
* **Get Annotation Tags:** List available annotation tags with optional filtering.

The list of tools is configurable, so you can choose which tools you want to make available to the MCP client.
This is useful if you don't use certain functionality or if you don't want to take up too much of the context window.
To disable a category of tools, use the `--disable-<category>` flag when starting the server. For example, to disable
the OnCall tools, use `--disable-oncall`, or to disable navigation deeplink generation, use `--disable-navigation`.

#### RBAC Permissions

Each tool requires specific RBAC permissions to function properly. When creating a service account for the MCP server, ensure it has the necessary permissions based on which tools you plan to use. The permissions listed are the minimum required actions - you may also need appropriate scopes (e.g., `datasources:*`, `dashboards:*`, `folders:*`) depending on your use case.

Tip: If you're not familiar with Grafana RBAC or you want a quicker, simpler setup instead of configuring many granular scopes, you can assign a built-in role such as `Editor` to the service account. The `Editor` role grants broad read/write access that will allow most MCP server operations; it is less granular (and therefore less restrictive) than manually-applied scopes, so use it only when convenience is more important than strict least-privilege access.

**Note:** Grafana Incident and Sift tools use basic Grafana roles instead of fine-grained RBAC permissions:

* **Viewer role:** Required for read-only operations (list incidents, get investigations)
* **Editor role:** Required for write operations (create incidents, modify investigations)

For more information about Grafana RBAC, see the [official documentation](https://grafana.com/docs/grafana/latest/administration/roles-and-permissions/access-control/).

#### RBAC Scopes

Scopes define the specific resources that permissions apply to. Each action requires both the appropriate permission and scope combination.

**Common Scope Patterns:**

* **Broad access:** Use `*` wildcards for organization-wide access

  + `datasources:*` - Access to all datasources
  + `dashboards:*` - Access to all dashboards
  + `folders:*` - Access to all folders
  + `teams:*` - Access to all teams
* **Limited access:** Use specific UIDs or IDs to restrict access to individual resources

  + `datasources:uid:prometheus-uid` - Access only to a specific Prometheus datasource
  + `dashboards:uid:abc123` - Access only to dashboard with UID `abc123`
  + `folders:uid:xyz789` - Access only to folder with UID `xyz789`
  + `teams:id:5` - Access only to team with ID `5`
  + `global.users:id:123` - Access only to user with ID `123`

**Examples:**

* **Full MCP server access:** Grant broad permissions for all tools

  ```
  datasources:* (datasources:read, datasources:query)
  dashboards:* (dashboards:read, dashboards:create, dashboards:write)
  folders:* (for dashboard creation and alert rules)
  teams:* (teams:read)
  global.users:* (users:read)
  ```
* **Limited datasource access:** Only query specific Prometheus and Loki instances

  ```
  datasources:uid:prometheus-prod (datasources:query)
  datasources:uid:loki-prod (datasources:query)
  ```
* **Dashboard-specific access:** Read only specific dashboards

  ```
  dashboards:uid:monitoring-dashboard (dashboards:read)
  dashboards:uid:alerts-dashboard (dashboards:read)
  ```

### Tools

| Tool | Category | Description | Required RBAC Permissions | Required Scopes |
| --- | --- | --- | --- | --- |
| `list_teams` | Admin | List all teams | `teams:read` | `teams:*` or `teams:id:1` |
| `list_users_by_org` | Admin | List all users in an organization | `users:read` | `global.users:*` or `global.users:id:123` |
| `search_dashboards` | Search | Search for dashboards | `dashboards:read` | `dashboards:*` or `dashboards:uid:abc123` |
| `get_dashboard_by_uid` | Dashboard | Get a dashboard by uid | `dashboards:read` | `dashboards:uid:abc123` |
| `update_dashboard` | Dashboard | Update or create a new dashboard | `dashboards:create`, `dashboards:write` | `dashboards:*`, `folders:*` or `folders:uid:xyz789` |
| `get_dashboard_panel_queries` | Dashboard | Get panel title, queries, datasource UID and type from a dashboard | `dashboards:read` | `dashboards:uid:abc123` |
| `get_dashboard_property` | Dashboard | Extract specific parts of a dashboard using JSONPath expressions | `dashboards:read` | `dashboards:uid:abc123` |
| `get_dashboard_summary` | Dashboard | Get a compact summary of a dashboard without full JSON | `dashboards:read` | `dashboards:uid:abc123` |
| `list_datasources` | Datasources | List datasources | `datasources:read` | `datasources:*` |
| `get_datasource_by_uid` | Datasources | Get a datasource by uid | `datasources:read` | `datasources:uid:prometheus-uid` |
| `get_datasource_by_name` | Datasources | Get a datasource by name | `datasources:read` | `datasources:*` or `datasources:uid:loki-uid` |
| `query_prometheus` | Prometheus | Execute a query against a Prometheus datasource | `datasources:query` | `datasources:uid:prometheus-uid` |
| `list_prometheus_metric_metadata` | Prometheus | List metric metadata | `datasources:query` | `datasources:uid:prometheus-uid` |
| `list_prometheus_metric_names` | Prometheus | List available metric names | `datasources:query` | `datasources:uid:prometheus-uid` |
| `list_prometheus_label_names` | Prometheus | List label names matching a selector | `datasources:query` | `datasources:uid:prometheus-uid` |
| `list_prometheus_label_values` | Prometheus | List values for a specific label | `datasources:query` | `datasources:uid:prometheus-uid` |
| `list_incidents` | Incident | List incidents in Grafana Incident | Viewer role | N/A |
| `create_incident` | Incident | Create an incident in Grafana Incident | Editor role | N/A |
| `add_activity_to_incident` | Incident | Add an activity item to an incident in Grafana Incident | Editor role | N/A |
| `get_incident` | Incident | Get a single incident by ID | Viewer role | N/A |
| `query_loki_logs` | Loki | Query and retrieve logs using LogQL (either log or metric queries) | `datasources:query` | `datasources:uid:loki-uid` |
| `list_loki_label_names` | Loki | List all available label names in logs | `datasources:query` | `datasources:uid:loki-uid` |
| `list_loki_label_values` | Loki | List values for a specific log label | `datasources:query` | `datasources:uid:loki-uid` |
| `query_loki_stats` | Loki | Get statistics about log streams | `datasources:query` | `datasources:uid:loki-uid` |
| `list_alert_rules` | Alerting | List alert rules | `alert.rules:read` | `folders:*` or `folders:uid:alerts-folder` |
| `get_alert_rule_by_uid` | Alerting | Get alert rule by UID | `alert.rules:read` | `folders:uid:alerts-folder` |
| `list_contact_points` | Alerting | List notification contact points | `alert.notifications:read` | Global scope |
| `list_oncall_schedules` | OnCall | List schedules from Grafana OnCall | `grafana-oncall-app.schedules:read` | Plugin-specific scopes |
| `get_oncall_shift` | OnCall | Get details for a specific OnCall shift | `grafana-oncall-app.schedules:read` | Plugin-specific scopes |
| `get_current_oncall_users` | OnCall | Get users currently on-call for a specific schedule | `grafana-oncall-app.schedules:read` | Plugin-specific scopes |
| `list_oncall_teams` | OnCall | List teams from Grafana OnCall | `grafana-oncall-app.user-settings:read` | Plugin-specific scopes |
| `list_oncall_users` | OnCall | List users from Grafana OnCall | `grafana-oncall-app.user-settings:read` | Plugin-specific scopes |
| `list_alert_groups` | OnCall | List alert groups from Grafana OnCall with filtering options | `grafana-oncall-app.alert-groups:read` | Plugin-specific scopes |
| `get_alert_group` | OnCall | Get a specific alert group from Grafana OnCall by its ID | `grafana-oncall-app.alert-groups:read` | Plugin-specific scopes |
| `get_sift_investigation` | Sift | Retrieve an existing Sift investigation by its UUID | Viewer role | N/A |
| `get_sift_analysis` | Sift | Retrieve a specific analysis from a Sift investigation | Viewer role | N/A |
| `list_sift_investigations` | Sift | Retrieve a list of Sift investigations with an optional limit | Viewer role | N/A |
| `find_error_pattern_logs` | Sift | Finds elevated error patterns in Loki logs. | Editor role | N/A |
| `find_slow_requests` | Sift | Finds slow requests from the relevant tempo datasources. | Editor role | N/A |
| `list_pyroscope_label_names` | Pyroscope | List label names matching a selector | `datasources:query` | `datasources:uid:pyroscope-uid` |
| `list_pyroscope_label_values` | Pyroscope | List label values matching a selector for a label name | `datasources:query` | `datasources:uid:pyroscope-uid` |
| `list_pyroscope_profile_types` | Pyroscope | List available profile types | `datasources:query` | `datasources:uid:pyroscope-uid` |
| `fetch_pyroscope_profile` | Pyroscope | Fetches a profile in DOT format for analysis | `datasources:query` | `datasources:uid:pyroscope-uid` |
| `get_assertions` | Asserts | Get assertion summary for a given entity | Plugin-specific permissions | Plugin-specific scopes |
| `generate_deeplink` | Navigation | Generate accurate deeplink URLs for Grafana resources | None (read-only URL generation) | N/A |
| `get_annotations` | Annotations | Fetch annotations with filters | `annotations:read` | `annotations:*` or `annotations:id:123` |
| `create_annotation` | Annotations | Create a new annotation on a dashboard or panel | `annotations:write` | `annotations:*` |
| `create_graphite_annotation` | Annotations | Create an annotation using Graphite format | `annotations:write` | `annotations:*` |
| `update_annotation` | Annotations | Replace all fields of an annotation (full update) | `annotations:write` | `annotations:*` |
| `patch_annotation` | Annotations | Update only specific fields of an annotation (partial update) | `annotations:write` | `annotations:*` |
| `get_annotation_tags` | Annotations | List annotation tags with optional filtering | `annotations:read` | `annotations:*` |

## CLI Flags Reference

The `mcp-grafana` binary supports various command-line flags for configuration:

**Transport Options:**

* `-t, --transport`: Transport type (`stdio`, `sse`, or `streamable-http`) - default: `stdio`
* `--address`: The host and port for SSE/streamable-http server - default: `localhost:8000`
* `--base-path`: Base path for the SSE/streamable-http server
* `--endpoint-path`: Endpoint path for the streamable-http server - default: `/`

**Debug and Logging:**

* `--debug`: Enable debug mode for detailed HTTP request/response logging

**Tool Configuration:**

* `--enabled-tools`: Comma-separated list of enabled categories - default: all categories enabled - example: "loki,datasources"
* `--disable-search`: Disable search tools
* `--disable-datasource`: Disable datasource tools
* `--disable-incident`: Disable incident tools
* `--disable-prometheus`: Disable prometheus tools
* `--disable-write`: Disable write tools (create/update operations)
* `--disable-loki`: Disable loki tools
* `--disable-alerting`: Disable alerting tools
* `--disable-dashboard`: Disable dashboard tools
* `--disable-oncall`: Disable oncall tools
* `--disable-asserts`: Disable asserts tools
* `--disable-sift`: Disable sift tools
* `--disable-admin`: Disable admin tools
* `--disable-pyroscope`: Disable pyroscope tools
* `--disable-navigation`: Disable navigation tools

### Read-Only Mode

The `--disable-write` flag provides a way to run the MCP server in read-only mode, preventing any write operations to your Grafana instance. This is useful for scenarios where you want to provide safe, read-only access such as:

* Using service accounts with limited read-only permissions
* Providing AI assistants with observability data without modification capabilities
* Running in production environments where write access should be restricted
* Testing and development scenarios where you want to prevent accidental modifications

When `--disable-write` is enabled, the following write operations are disabled:

**Dashboard Tools:**

* `update_dashboard`

**Folder Tools:**

* `create_folder`

**Incident Tools:**

* `create_incident`
* `add_activity_to_incident`

**Alerting Tools:**

* `create_alert_rule`
* `update_alert_rule`
* `delete_alert_rule`

**Annotation Tools:**

* `create_annotation`
* `create_graphite_annotation`
* `update_annotation`
* `patch_annotation`

**Sift Tools:**

* `find_error_pattern_logs` (creates investigations)
* `find_slow_requests` (creates investigations)

All read operations remain available, allowing you to query dashboards, run PromQL/LogQL queries, list resources, and retrieve data.

**Client TLS Configuration (for Grafana connections):**

* `--tls-cert-file`: Path to TLS certificate file for client authentication
* `--tls-key-file`: Path to TLS private key file for client authentication
* `--tls-ca-file`: Path to TLS CA certificate file for server verification
* `--tls-skip-verify`: Skip TLS certificate verification (insecure)

**Server TLS Configuration (streamable-http transport only):**

* `--server.tls-cert-file`: Path to TLS certificate file for server HTTPS
* `--server.tls-key-file`: Path to TLS private key file for server HTTPS

## Usage

This MCP server works with both local Grafana instances and Grafana Cloud.

- For **local Grafana**, use `http://localhost:3000`
- For **Grafana Cloud**, use your instance URL (e.g., `https://myinstance.grafana.net`)

### Step 1: Create a Service Account

1. Log into your Grafana instance
2. Navigate to **Administration → Service Accounts**
3. Click **Add service account**
4. Name it (e.g., `mcp-grafana`) and assign the **Editor** role
5. Click **Add service account token**
6. Copy the generated token immediately — it won't be shown again

For detailed steps, see the [Grafana service account documentation](https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana).

> **Note:** The environment variable `GRAFANA_API_KEY` is deprecated and will be removed in a future version. Please migrate to using `GRAFANA_SERVICE_ACCOUNT_TOKEN` instead.

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `GRAFANA_URL` | Yes | Grafana instance URL (e.g., `http://localhost:3000` or `https://myinstance.grafana.net`) |
| `GRAFANA_SERVICE_ACCOUNT_TOKEN` | Yes* | Service account token (recommended) |
| `GRAFANA_USERNAME` | No | Basic auth username (alternative to token) |
| `GRAFANA_PASSWORD` | No | Basic auth password (alternative to token) |
| `GRAFANA_ORG_ID` | No | Organization ID for multi-tenant setups |

*Either `GRAFANA_SERVICE_ACCOUNT_TOKEN` or `GRAFANA_USERNAME`/`GRAFANA_PASSWORD` is required.

### Multi-Organization Support

You can specify which organization to interact with using either:

* **Environment variable:** Set `GRAFANA_ORG_ID` to the numeric organization ID
* **HTTP header:** Set `X-Grafana-Org-Id` when using SSE or streamable HTTP transports (header takes precedence over environment variable)

### Step 2: Install mcp-grafana

Choose one installation method:

**Option A: Docker image**

```bash
docker pull mcp/grafana
```

**Option B: Download binary**

Download from the [releases page](https://github.com/grafana/mcp-grafana/releases) and place it in your `$PATH`.

**Option C: Build from source**

Requires Go 1.21+:

```bash
GOBIN="$HOME/go/bin" go install github.com/grafana/mcp-grafana/cmd/mcp-grafana@latest
```

**Option D: Deploy to Kubernetes using Helm**

```bash
helm repo add grafana https://grafana.github.io/helm-charts
helm install --set grafana.apiKey=<Grafana_ApiKey> --set grafana.url=<GrafanaUrl> my-release grafana/grafana-mcp
```

### Step 3: Configure Your MCP Client

#### Claude Desktop Configuration

Find your config file location:

| OS | Path |
|----|------|
| macOS | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Windows | `%APPDATA%\Claude\claude_desktop_config.json` |
| Linux | `~/.config/Claude/claude_desktop_config.json` |

**Configuration using the binary:**

For local Grafana:

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

For Grafana Cloud:

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "https://myinstance.grafana.net",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxx"
      }
    }
  }
}
```

**Configuration using Docker:**

> **Important:** When running via Docker, `localhost` refers to the container itself, not your host machine. Use `host.docker.internal` on Mac/Windows to reach services on your host.

For local Grafana (Mac/Windows):

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

For local Grafana on Linux, use `--network host` instead:

```json
{
  "mcpServers": {
    "grafana": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "--network", "host",
        "-e", "GRAFANA_URL",
        "-e", "GRAFANA_SERVICE_ACCOUNT_TOKEN",
        "mcp/grafana",
        "-t", "stdio"
      ],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxx"
      }
    }
  }
}
```

For Grafana Cloud (any OS):

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
        "GRAFANA_URL": "https://myinstance.grafana.net",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxx"
      }
    }
  }
}
```

> **Note:** The `-t stdio` argument is essential when using Docker because it overrides the default SSE mode in the Docker image.

**With multi-organization support:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxx",
        "GRAFANA_ORG_ID": "2"
      }
    }
  }
}
```

**With username/password authentication:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_USERNAME": "admin",
        "GRAFANA_PASSWORD": "your-password"
      }
    }
  }
}
```

#### VSCode Configuration

For VSCode with a remote MCP server running in SSE mode, add to `.vscode/settings.json`:

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "sse",
        "url": "http://localhost:8000/sse"
      }
    }
  }
}
```

For HTTPS:

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "sse",
        "url": "https://localhost:8443/sse"
      }
    }
  }
}
```

### Docker Transport Modes

The Docker image's entrypoint defaults to SSE mode, but most users want STDIO mode for Claude Desktop integration.

**STDIO Mode (for Claude Desktop):**

```bash
docker run --rm -i \
  -e GRAFANA_URL=http://host.docker.internal:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=glsa_xxxx \
  mcp/grafana -t stdio
```

**SSE Mode (for remote clients):**

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://host.docker.internal:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=glsa_xxxx \
  mcp/grafana
```

**Streamable HTTP Mode:**

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://host.docker.internal:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=glsa_xxxx \
  mcp/grafana -t streamable-http
```

**HTTPS Streamable HTTP Mode:**

```bash
docker run --rm -p 8443:8443 \
  -v /path/to/certs:/certs:ro \
  -e GRAFANA_URL=http://host.docker.internal:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=glsa_xxxx \
  mcp/grafana \
  -t streamable-http \
  -addr :8443 \
  --server.tls-cert-file /certs/server.crt \
  --server.tls-key-file /certs/server.key
```

### Debug Mode

Enable debug mode for detailed HTTP request/response logging:

**Binary:**

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

**Docker:**

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
        "-t", "stdio",
        "-debug"
      ],
      "env": {
        "GRAFANA_URL": "http://host.docker.internal:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxx"
      }
    }
  }
}
```

### TLS Configuration

If your Grafana instance is behind mTLS or requires custom TLS certificates:

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
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxx"
      }
    }
  }
}
```

**With Docker:**

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
        "-e", "GRAFANA_SERVICE_ACCOUNT_TOKEN",
        "mcp/grafana",
        "-t", "stdio",
        "--tls-cert-file", "/certs/client.crt",
        "--tls-key-file", "/certs/client.key",
        "--tls-ca-file", "/certs/ca.crt"
      ],
      "env": {
        "GRAFANA_URL": "https://secure-grafana.example.com",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxx"
      }
    }
  }
}
```

**CLI examples:**

```bash
# Self-signed certificates (testing only)
./mcp-grafana --tls-skip-verify -debug

# Client certificate authentication
./mcp-grafana \
  --tls-cert-file /path/to/client.crt \
  --tls-key-file /path/to/client.key \
  --tls-ca-file /path/to/ca.crt \
  -debug

# Custom CA certificate only
./mcp-grafana --tls-ca-file /path/to/ca.crt
```

### Server TLS Configuration (Streamable HTTP Only)

When using streamable HTTP transport, you can configure the MCP server to serve HTTPS:

```bash
./mcp-grafana \
  -t streamable-http \
  --server.tls-cert-file /path/to/server.crt \
  --server.tls-key-file /path/to/server.key \
  -addr :8443
```

### Health Check Endpoint

When using SSE or streamable HTTP transports, the server exposes `/healthz`:

```bash
curl http://localhost:8000/healthz
# Response: ok
```

## Troubleshooting

| Error | Cause | Solution |
|-------|-------|----------|
| `spawn mcp-grafana ENOENT` | Binary not in PATH | Use the full path to the binary (e.g., `/Users/you/go/bin/mcp-grafana`) |
| `TypeError: fetch failed` | VSCode client bug with HTTP redirects | Use SSE transport instead of streamable-http, or update VSCode to June 2025+ |
| `Server exited before responding to 'initialize' request` | Connection issue or wrong URL | Verify `GRAFANA_URL` is reachable from your machine. Check firewall rules. |
| `401 Unauthorized` | Invalid or expired token | Generate a new service account token in Grafana |
| `400 Bad Request: id is invalid` | Grafana version < 9.0 | Upgrade Grafana to version 9.0 or later |
| `connection refused` (Docker) | `localhost` doesn't work inside container | Use `host.docker.internal:3000` on Mac/Windows, or `--network host` on Linux |
| Empty query results | Time range doesn't contain data | Expand your query time range or verify data exists |
| `panic: Invalid transport type` | Old Docker image with unsupported transport | Pull the latest Docker image |
| Datasource operations fail | Grafana version < 9.0 | The `/datasources/uid/{uid}` API requires Grafana 9.0+ |

### Grafana Version Compatibility

If you encounter this error with datasource-related tools:

```
get datasource by uid : [GET /datasources/uid/{uid}][400] getDataSourceByUidBadRequest {"message":"id is invalid"}
```

This indicates you're using Grafana < 9.0. The `/datasources/uid/{uid}` API endpoint was introduced in Grafana 9.0.

**Solution:** Upgrade your Grafana instance to version 9.0 or later.

## Examples

The `/examples` directory contains sample configurations and usage patterns. See the examples for:

- Different authentication methods
- Multi-organization setups
- TLS configurations
- Docker Compose deployments

## Development

Contributions are welcome! Please open an issue or submit a pull request.

### Setup

This project is written in Go 1.21+. Install Go following the instructions for your platform.

Run the server locally in STDIO mode (default):

```bash
make run
```

Run in SSE mode:

```bash
go run ./cmd/mcp-grafana --transport sse
```

Build and run Docker image:

```bash
make build-image
docker run -it --rm -p 8000:8000 mcp-grafana:latest
```

### Testing

**Unit tests (no external dependencies):**

```bash
make test-unit
# or
make test
```

**Integration tests (requires Docker containers):**

```bash
docker-compose up -d
make test-integration
```

**Cloud tests (requires Grafana Cloud credentials):**

```bash
make test-cloud
```

**All tests:**

```bash
make test-all
```

### Linting

```bash
make lint
```

This includes a custom JSONSchema linter. Run it separately with:

```bash
make lint-jsonschema
```

See the [JSONSchema Linter documentation](internal/linter/jsonschema/README.md) for details.

## License

This project is licensed under the [Apache License, Version 2.0](LICENSE).
