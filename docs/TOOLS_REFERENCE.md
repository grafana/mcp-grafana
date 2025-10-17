# Tools & CLI Reference

Complete reference for all available tools and command-line flags in the Grafana MCP server.

## Table of Contents

- [Tools Overview](#tools-overview)
- [Tools by Category](#tools-by-category)
- [CLI Flags Reference](#cli-flags-reference)

## Tools Overview

This table lists all available tools, their categories, descriptions, and required RBAC permissions.

| Tool | Category | Description | Required RBAC Permissions | Required Scopes |
|------|----------|-------------|---------------------------|-----------------|
| `list_teams` | Admin | List all teams | `teams:read` | `teams:*` or `teams:id:1` |
| `list_users_by_org` | Admin | List all users in an organization | `users:read` | `global.users:*` or `global.users:id:123` |
| `search_dashboards` | Search | Search for dashboards | `dashboards:read` | `dashboards:*` or `dashboards:uid:abc123` |
| `get_dashboard_by_uid` | Dashboard | Get a dashboard by uid | `dashboards:read` | `dashboards:uid:abc123` |
| `update_dashboard` | Dashboard | Update or create a new dashboard | `dashboards:create`, `dashboards:write` | `dashboards:*`, `folders:*` or `folders:uid:xyz789` |
| `get_dashboard_panel_queries` | Dashboard | Get panel title, queries, datasource UID and type from a dashboard | `dashboards:read` | `dashboards:uid:abc123` |
| `get_dashboard_property` | Dashboard | Extract specific parts of a dashboard using JSONPath expressions | `dashboards:read` | `dashboards:uid:abc123` |
| `get_dashboard_summary` | Dashboard | Get a compact summary of a dashboard without full JSON | `dashboards:read` | `dashboards:uid:abc123` |
| `patch_dashboard` | Dashboard | Apply specific changes to a dashboard without requiring full JSON | `dashboards:write` | `dashboards:uid:abc123` |
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
| `find_error_pattern_logs` | Sift | Finds elevated error patterns in Loki logs | Editor role | N/A |
| `find_slow_requests` | Sift | Finds slow requests from the relevant tempo datasources | Editor role | N/A |
| `list_pyroscope_label_names` | Pyroscope | List label names matching a selector | `datasources:query` | `datasources:uid:pyroscope-uid` |
| `list_pyroscope_label_values` | Pyroscope | List label values matching a selector for a label name | `datasources:query` | `datasources:uid:pyroscope-uid` |
| `list_pyroscope_profile_types` | Pyroscope | List available profile types | `datasources:query` | `datasources:uid:pyroscope-uid` |
| `fetch_pyroscope_profile` | Pyroscope | Fetches a profile in DOT format for analysis | `datasources:query` | `datasources:uid:pyroscope-uid` |
| `get_assertions` | Asserts | Get assertion summary for a given entity | Plugin-specific permissions | Plugin-specific scopes |
| `generate_deeplink` | Navigation | Generate accurate deeplink URLs for Grafana resources | None (read-only URL generation) | N/A |

## Tools by Category

### Admin Tools

**Purpose:** Manage teams and users in your Grafana organization.

- `list_teams` - List all teams
- `list_users_by_org` - List all users in an organization

**Disable with:** `--disable-admin`

### Search Tools

**Purpose:** Search for dashboards across your Grafana instance.

- `search_dashboards` - Search for dashboards by title, tags, or other metadata

**Disable with:** `--disable-search`

### Dashboard Tools

**Purpose:** Full CRUD operations on Grafana dashboards with context-aware features.

- `get_dashboard_by_uid` - Get complete dashboard JSON
- `get_dashboard_summary` - Get compact dashboard overview
- `get_dashboard_property` - Extract specific dashboard parts with JSONPath
- `get_dashboard_panel_queries` - Get panel queries and datasource information
- `update_dashboard` - Create or update dashboards
- `patch_dashboard` - Apply targeted changes without full JSON

**Disable with:** `--disable-dashboard`

**Recommended:** Use `get_dashboard_summary` or `get_dashboard_property` instead of `get_dashboard_by_uid` to reduce context window usage.

### Datasource Tools

**Purpose:** List and retrieve datasource information.

- `list_datasources` - List all datasources
- `get_datasource_by_uid` - Get datasource by UID
- `get_datasource_by_name` - Get datasource by name

**Disable with:** `--disable-datasource`

### Prometheus Tools

**Purpose:** Query Prometheus datasources and retrieve metric metadata.

- `query_prometheus` - Execute PromQL queries (instant and range)
- `list_prometheus_metric_metadata` - Get metric metadata
- `list_prometheus_metric_names` - List available metrics
- `list_prometheus_label_names` - List label names
- `list_prometheus_label_values` - List label values

**Disable with:** `--disable-prometheus`

### Loki Tools

**Purpose:** Query logs and retrieve metadata from Loki datasources.

- `query_loki_logs` - Execute LogQL queries (log and metric queries)
- `list_loki_label_names` - List log label names
- `list_loki_label_values` - List log label values
- `query_loki_stats` - Get log stream statistics

**Disable with:** `--disable-loki`

### Pyroscope Tools

**Purpose:** Query continuous profiling data from Pyroscope datasources.

- `list_pyroscope_label_names` - List profile label names
- `list_pyroscope_label_values` - List profile label values
- `list_pyroscope_profile_types` - List available profile types
- `fetch_pyroscope_profile` - Fetch profiles in DOT format

**Disable with:** `--disable-pyroscope`

### Incident Tools

**Purpose:** Manage incidents in Grafana Incident.

- `list_incidents` - Search and list incidents
- `create_incident` - Create new incidents
- `add_activity_to_incident` - Add activity items to incidents
- `get_incident` - Get incident details by ID

**Disable with:** `--disable-incident`

**Note:** Requires basic Grafana roles (Viewer/Editor) instead of RBAC permissions.

### Sift Tools

**Purpose:** Automated issue detection and investigation with Grafana Sift.

- `list_sift_investigations` - List Sift investigations
- `get_sift_investigation` - Get investigation details
- `get_sift_analysis` - Get specific analysis from investigation
- `find_error_pattern_logs` - Detect error patterns in logs
- `find_slow_requests` - Detect slow requests in traces

**Disable with:** `--disable-sift`

**Note:** Requires basic Grafana roles (Viewer/Editor) instead of RBAC permissions.

### Alerting Tools

**Purpose:** View alert rules and notification contact points.

- `list_alert_rules` - List all alert rules and their states
- `get_alert_rule_by_uid` - Get alert rule details
- `list_contact_points` - List notification contact points

**Disable with:** `--disable-alerting`

### OnCall Tools

**Purpose:** Manage on-call schedules and view alert groups in Grafana OnCall.

- `list_oncall_schedules` - List on-call schedules
- `get_oncall_shift` - Get shift details
- `get_current_oncall_users` - Get current on-call users
- `list_oncall_teams` - List OnCall teams
- `list_oncall_users` - List OnCall users
- `list_alert_groups` - List and filter alert groups
- `get_alert_group` - Get alert group details

**Disable with:** `--disable-oncall`

### Navigation Tools

**Purpose:** Generate accurate deeplink URLs to Grafana resources.

- `generate_deeplink` - Generate URLs for dashboards, panels, and Explore

**Disable with:** `--disable-navigation`

### Asserts Tools

**Purpose:** Integration with Grafana Asserts plugin.

- `get_assertions` - Get assertion summaries for entities

**Disable with:** `--disable-asserts`

## CLI Flags Reference

Complete reference for all command-line flags supported by `mcp-grafana`.

### Transport Options

Configure how the MCP server communicates with clients.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-t, --transport` | string | `stdio` | Transport type: `stdio`, `sse`, or `streamable-http` |
| `--address` | string | `localhost:8000` | Host and port for SSE/streamable-http server |
| `--base-path` | string | - | Base path for the SSE/streamable-http server |
| `--endpoint-path` | string | `/` | Endpoint path for the streamable-http server |

**Examples:**

```bash
# STDIO mode (default)
mcp-grafana -t stdio

# SSE mode on custom port
mcp-grafana -t sse --address :9090

# Streamable HTTP with custom base path
mcp-grafana -t streamable-http --base-path /mcp
```

### Debug and Logging

Enable detailed logging for troubleshooting.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--debug` | boolean | `false` | Enable debug mode for detailed HTTP request/response logging |

**Examples:**

```bash
# Enable debug mode
mcp-grafana --debug

# Debug with specific transport
mcp-grafana -t sse --debug
```

### Tool Configuration

Control which tools are available to MCP clients.

#### Enable Specific Tools

| Flag | Type | Description |
|------|------|-------------|
| `--enabled-tools` | string | Comma-separated list of enabled tools (default: all tools enabled) |

**Example:**

```bash
# Enable only specific tools
mcp-grafana --enabled-tools list_dashboards,get_dashboard_by_uid,query_prometheus
```

#### Disable Tool Categories

| Flag | Category Disabled | Tools Affected |
|------|-------------------|----------------|
| `--disable-search` | Search | `search_dashboards` |
| `--disable-datasource` | Datasources | `list_datasources`, `get_datasource_by_uid`, `get_datasource_by_name` |
| `--disable-dashboard` | Dashboard | All dashboard CRUD operations |
| `--disable-prometheus` | Prometheus | All Prometheus query and metadata tools |
| `--disable-loki` | Loki | All Loki query and metadata tools |
| `--disable-pyroscope` | Pyroscope | All Pyroscope profiling tools |
| `--disable-incident` | Incident | All incident management tools |
| `--disable-sift` | Sift | All Sift investigation tools |
| `--disable-alerting` | Alerting | All alerting tools |
| `--disable-oncall` | OnCall | All Grafana OnCall tools |
| `--disable-admin` | Admin | All admin tools (teams, users) |
| `--disable-navigation` | Navigation | Deeplink generation |
| `--disable-asserts` | Asserts | Asserts plugin integration |

**Examples:**

```bash
# Disable OnCall tools
mcp-grafana --disable-oncall

# Disable multiple categories
mcp-grafana --disable-oncall --disable-incident --disable-sift

# Minimal setup - only dashboards and datasources
mcp-grafana --disable-prometheus --disable-loki --disable-alerting \
  --disable-oncall --disable-incident --disable-sift --disable-admin
```

### Client TLS Configuration

Configure TLS for connections from the MCP server to Grafana.

| Flag | Type | Description |
|------|------|-------------|
| `--tls-cert-file` | string | Path to TLS certificate file for client authentication |
| `--tls-key-file` | string | Path to TLS private key file for client authentication |
| `--tls-ca-file` | string | Path to TLS CA certificate file for server verification |
| `--tls-skip-verify` | boolean | Skip TLS certificate verification (insecure, testing only) |

**Examples:**

```bash
# Client certificate authentication
mcp-grafana \
  --tls-cert-file /path/to/client.crt \
  --tls-key-file /path/to/client.key \
  --tls-ca-file /path/to/ca.crt

# Custom CA only
mcp-grafana --tls-ca-file /path/to/ca.crt

# Skip verification (testing only - DO NOT USE IN PRODUCTION)
mcp-grafana --tls-skip-verify
```

**Applies to:** All HTTP connections from MCP server to Grafana, including Prometheus, Loki, Incident, Sift, Alerting, OnCall, and Asserts clients.

### Server TLS Configuration

Configure TLS for connections to the MCP server (streamable-http transport only).

| Flag | Type | Description |
|------|------|-------------|
| `--server.tls-cert-file` | string | Path to TLS certificate file for server HTTPS |
| `--server.tls-key-file` | string | Path to TLS private key file for server HTTPS |

**Examples:**

```bash
# HTTPS server on port 8443
mcp-grafana \
  -t streamable-http \
  --address :8443 \
  --server.tls-cert-file /path/to/server.crt \
  --server.tls-key-file /path/to/server.key
```

**Note:** Server TLS is separate from client TLS. Server TLS secures connections **to** the MCP server, while client TLS secures connections **from** the MCP server to Grafana.

### Complete Example

Comprehensive example with multiple flags:

```bash
mcp-grafana \
  -t streamable-http \
  --address :8443 \
  --debug \
  --server.tls-cert-file /certs/server.crt \
  --server.tls-key-file /certs/server.key \
  --tls-cert-file /certs/client.crt \
  --tls-key-file /certs/client.key \
  --tls-ca-file /certs/ca.crt \
  --disable-oncall \
  --disable-incident
```

This configuration:
- Uses streamable-http transport on port 8443
- Enables debug logging
- Serves HTTPS to clients
- Uses mTLS to connect to Grafana
- Disables OnCall and Incident tools

## Environment Variables

While not CLI flags, these environment variables are required:

| Variable | Required | Description |
|----------|----------|-------------|
| `GRAFANA_URL` | Yes | Your Grafana instance URL |
| `GRAFANA_SERVICE_ACCOUNT_TOKEN` | Yes* | Service account token for authentication |
| `GRAFANA_USERNAME` | Yes* | Username for basic authentication |
| `GRAFANA_PASSWORD` | Yes* | Password for basic authentication |

\* Either `GRAFANA_SERVICE_ACCOUNT_TOKEN` or both `GRAFANA_USERNAME` and `GRAFANA_PASSWORD` are required.

## Next Steps

- [Configure your MCP client](CONFIGURATION.md)
- [Review RBAC permissions](RBAC.md)
- [Explore features in detail](FEATURES.md)
- [Check out examples](examples/)