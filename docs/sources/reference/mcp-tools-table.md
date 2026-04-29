---
title: MCP tools reference
menuTitle: MCP tools
description: MCP tools, required Grafana RBAC permissions and scopes, and related guidance.
keywords:
  - MCP
  - tools
  - RBAC
weight: 1
aliases:
  - ../features-and-rbac/
---

# MCP tools reference

Use the table to confirm minimum Grafana RBAC permissions and scopes for each MCP tool. The sections after the table summarize RBAC patterns, optional categories, and a few operational notes.

{{< admonition type="note" >}}
The tool list and behavior reflect the current server release. This page is not a roadmap or a commitment to future features.
{{< /admonition >}}

## What you'll achieve

You can verify that a service account has the right permissions before you enable tools in production, and you can apply common scope patterns without rereading Grafana’s RBAC docs.

## Before you begin

- Grafana 9.0 or later for full API support.
- Optional: a [service account](https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana) whose permissions match the tools you enable.

## Review the tools table

The following table lists MCP tools, required RBAC permissions, and typical scopes. Categories marked with `*` are off until you add them to `--enabled-tools` (refer to [Command-line flags](../../configure/command-line-flags/)). The table does not include [proxied tools](../../configure/proxied-tools/) from external MCP servers (for example Grafana Tempo).

| Tool                              | Category    | Description                                                         | Required RBAC Permissions               | Required Scopes                                     |
| --------------------------------- | ----------- | ------------------------------------------------------------------- | --------------------------------------- | --------------------------------------------------- |
| `list_teams`                      | Admin*      | List all teams                                                      | `teams:read`                            | `teams:*` or `teams:id:1`                           |
| `list_users_by_org`               | Admin*      | List all users in an organization                                   | `users:read`                            | `global.users:*` or `global.users:id:123`           |
| `list_all_roles`                  | Admin*      | List all Grafana roles                                              | `roles:read`                            | `roles:*`                                           |
| `get_role_details`                | Admin*      | Get details for a Grafana role                                      | `roles:read`                            | `roles:uid:editor`                                  |
| `get_role_assignments`            | Admin*      | List assignments for a role                                         | `roles:read`                            | `roles:uid:editor`                                  |
| `list_user_roles`                 | Admin*      | List roles for users                                                | `roles:read`                            | `global.users:id:123`                               |
| `list_team_roles`                 | Admin*      | List roles for teams                                                | `roles:read`                            | `teams:id:7`                                        |
| `get_resource_permissions`        | Admin*      | List permissions for a resource                                     | `permissions:read`                      | `dashboards:uid:abcd1234`                           |
| `get_resource_description`        | Admin*      | Describe a Grafana resource type                                    | `permissions:read`                      | `dashboards:*`                                      |
| `search_dashboards`               | Search      | Search for dashboards                                               | `dashboards:read`                       | `dashboards:*` or `dashboards:uid:abc123`           |
| `search_folders`                  | Search      | Search for folders by query string                                  | `folders:read`                          | `folders:*` or `folders:uid:xyz789`                 |
| `get_dashboard_by_uid`            | Dashboard   | Get a dashboard by uid                                              | `dashboards:read`                       | `dashboards:uid:abc123`                             |
| `update_dashboard`                | Dashboard   | Update or create a new dashboard                                    | `dashboards:create`, `dashboards:write` | `dashboards:*`, `folders:*` or `folders:uid:xyz789` |
| `get_dashboard_panel_queries`     | Dashboard   | Get panel title, queries, datasource UID and type from a dashboard  | `dashboards:read`                       | `dashboards:uid:abc123`                             |
| `run_panel_query`                 | RunPanelQuery* | Execute one or more dashboard panel queries                       | `dashboards:read`, `datasources:query`  | `dashboards:uid:*`, `datasources:uid:*`             |
| `get_dashboard_property`          | Dashboard   | Extract specific parts of a dashboard using JSONPath expressions    | `dashboards:read`                       | `dashboards:uid:abc123`                             |
| `get_dashboard_summary`           | Dashboard   | Get a compact summary of a dashboard without full JSON              | `dashboards:read`                       | `dashboards:uid:abc123`                             |
| `create_folder`                   | Folder      | Create a Grafana folder with a title and optional UID               | `folders:create`                        | `folders:*`                                         |
| `list_datasources`                | Datasources | List datasources                                                    | `datasources:read`                      | `datasources:*`                                     |
| `get_datasource`                  | Datasources | Get a datasource by UID or name                                     | `datasources:read`                      | `datasources:uid:prometheus-uid`                    |
| `get_query_examples`              | Examples*   | Get example queries for a datasource type                           | `datasources:read`                      | `datasources:*`                                     |
| `query_prometheus`                | Prometheus  | Execute a query against a Prometheus datasource                     | `datasources:query`                     | `datasources:uid:prometheus-uid`                    |
| `list_prometheus_metric_metadata` | Prometheus  | List metric metadata                                                | `datasources:query`                     | `datasources:uid:prometheus-uid`                    |
| `list_prometheus_metric_names`    | Prometheus  | List available metric names                                         | `datasources:query`                     | `datasources:uid:prometheus-uid`                    |
| `list_prometheus_label_names`     | Prometheus  | List label names matching a selector                                | `datasources:query`                     | `datasources:uid:prometheus-uid`                    |
| `list_prometheus_label_values`    | Prometheus  | List values for a specific label                                    | `datasources:query`                     | `datasources:uid:prometheus-uid`                    |
| `query_prometheus_histogram`      | Prometheus  | Calculate histogram percentile values                               | `datasources:query`                     | `datasources:uid:prometheus-uid`                    |
| `list_incidents`                  | Incident    | List incidents in Grafana Incident                                  | Viewer role                             | N/A                                                 |
| `create_incident`                 | Incident    | Create an incident in Grafana Incident                              | Editor role                             | N/A                                                 |
| `add_activity_to_incident`        | Incident    | Add an activity item to an incident in Grafana Incident             | Editor role                             | N/A                                                 |
| `get_incident`                    | Incident    | Get a single incident by ID                                         | Viewer role                             | N/A                                                 |
| `query_loki_logs`                 | Loki        | Query and retrieve logs using LogQL (either log or metric queries)  | `datasources:query`                     | `datasources:uid:loki-uid`                          |
| `list_loki_label_names`           | Loki        | List all available label names in logs                              | `datasources:query`                     | `datasources:uid:loki-uid`                          |
| `list_loki_label_values`          | Loki        | List values for a specific log label                                | `datasources:query`                     | `datasources:uid:loki-uid`                          |
| `query_loki_stats`                | Loki        | Get statistics about log streams                                    | `datasources:query`                     | `datasources:uid:loki-uid`                          |
| `query_loki_patterns`             | Loki        | Query detected log patterns to identify common structures           | `datasources:query`                     | `datasources:uid:loki-uid`                          |
| `query_influxdb`                  | InfluxDB*   | Query InfluxDB using InfluxQL (v1) or Flux (v2)                     | `datasources:query`                     | `datasources:uid:influxdb-uid`                      |
| `list_clickhouse_tables`          | ClickHouse* | List tables in a ClickHouse database                                | `datasources:query`                     | `datasources:uid:*`                                 |
| `describe_clickhouse_table`       | ClickHouse* | Get table schema with column types                                  | `datasources:query`                     | `datasources:uid:*`                                 |
| `query_clickhouse`                | ClickHouse* | Execute SQL queries with macro substitution                         | `datasources:query`                     | `datasources:uid:*`                                 |
| `list_cloudwatch_namespaces`      | CloudWatch* | List available AWS CloudWatch namespaces                            | `datasources:query`                     | `datasources:uid:*`                                 |
| `list_cloudwatch_metrics`         | CloudWatch* | List metrics in a namespace                                         | `datasources:query`                     | `datasources:uid:*`                                 |
| `list_cloudwatch_dimensions`      | CloudWatch* | List dimensions for a metric                                        | `datasources:query`                     | `datasources:uid:*`                                 |
| `query_cloudwatch`                | CloudWatch* | Execute CloudWatch metric queries                                   | `datasources:query`                     | `datasources:uid:*`                                 |
| `query_elasticsearch`             | Elasticsearch* | Query Elasticsearch using Lucene syntax or Query DSL              | `datasources:query`                     | `datasources:uid:elasticsearch-uid`                 |
| `alerting_manage_rules`           | Alerting    | Manage alert rules (list, get, versions, create, update, delete)    | `alert.rules:read` + `alert.rules:write` for mutations | `folders:*` or `folders:uid:alerts-folder` |
| `alerting_manage_routing`         | Alerting    | Manage notification policies, contact points, and time intervals    | `alert.notifications:read`              | Global scope                                        |
| `list_oncall_schedules`           | OnCall      | List schedules from Grafana OnCall                                  | `grafana-oncall-app.schedules:read`     | Plugin-specific scopes                              |
| `get_oncall_shift`                | OnCall      | Get details for a specific OnCall shift                             | `grafana-oncall-app.schedules:read`     | Plugin-specific scopes                              |
| `get_current_oncall_users`        | OnCall      | Get users currently on-call for a specific schedule                 | `grafana-oncall-app.schedules:read`     | Plugin-specific scopes                              |
| `list_oncall_teams`               | OnCall      | List teams from Grafana OnCall                                      | `grafana-oncall-app.user-settings:read` | Plugin-specific scopes                              |
| `list_oncall_users`               | OnCall      | List users from Grafana OnCall                                      | `grafana-oncall-app.user-settings:read` | Plugin-specific scopes                              |
| `list_alert_groups`               | OnCall      | List alert groups from Grafana OnCall with filtering options        | `grafana-oncall-app.alert-groups:read`  | Plugin-specific scopes                              |
| `get_alert_group`                 | OnCall      | Get a specific alert group from Grafana OnCall by its ID            | `grafana-oncall-app.alert-groups:read`  | Plugin-specific scopes                              |
| `get_sift_investigation`          | Sift        | Retrieve an existing Sift investigation by its UUID                 | Viewer role                             | N/A                                                 |
| `get_sift_analysis`               | Sift        | Retrieve a specific analysis from a Sift investigation              | Viewer role                             | N/A                                                 |
| `list_sift_investigations`        | Sift        | Retrieve a list of Sift investigations with an optional limit       | Viewer role                             | N/A                                                 |
| `find_error_pattern_logs`         | Sift        | Finds elevated error patterns in Loki logs.                         | Editor role                             | N/A                                                 |
| `find_slow_requests`              | Sift        | Finds slow requests from the relevant tempo datasources.            | Editor role                             | N/A                                                 |
| `list_pyroscope_label_names`      | Pyroscope   | List label names matching a selector                                | `datasources:query`                     | `datasources:uid:pyroscope-uid`                     |
| `list_pyroscope_label_values`     | Pyroscope   | List label values matching a selector for a label name              | `datasources:query`                     | `datasources:uid:pyroscope-uid`                     |
| `list_pyroscope_profile_types`    | Pyroscope   | List available profile types                                        | `datasources:query`                     | `datasources:uid:pyroscope-uid`                     |
| `fetch_pyroscope_profile`         | Pyroscope   | Fetches a profile in DOT format for analysis                        | `datasources:query`                     | `datasources:uid:pyroscope-uid`                     |
| `get_assertions`                  | Asserts     | Get assertion summary for a given entity                            | Plugin-specific permissions             | Plugin-specific scopes                              |
| `generate_deeplink`               | Navigation  | Generate accurate deeplink URLs for Grafana resources               | None (read-only URL generation)         | N/A                                                 |
| `get_annotations`                 | Annotations | Fetch annotations with filters                                      | `annotations:read`                      | `annotations:*` or `annotations:id:123`             |
| `create_annotation`               | Annotations | Create a new annotation (standard or Graphite format)               | `annotations:write`                     | `annotations:*`                                     |
| `update_annotation`               | Annotations | Update specific fields of an annotation (partial update)            | `annotations:write`                     | `annotations:*`                                     |
| `get_annotation_tags`             | Annotations | List annotation tags with optional filtering                        | `annotations:read`                      | `annotations:*`                                     |
| `get_panel_image`                 | Rendering   | Render a dashboard panel or full dashboard as a PNG image           | `dashboards:read`                       | `dashboards:uid:abc123`                             |

_* Categories marked with `*` are off until you add them to `--enabled-tools`._

## Dashboard tools and context window

`update_dashboard` supports full JSON replacement and patch-style updates (`uid` plus `operations`). Prefer patches for small changes so you do not send large dashboard JSON to the model.

To limit context use when working with dashboards ([issue #101](https://github.com/grafana/mcp-grafana/issues/101)):

- Use `get_dashboard_summary` for an overview before edits.
- Use `get_dashboard_property` with JSONPath when you only need part of a dashboard.
- Avoid `get_dashboard_by_uid` unless you need the full dashboard JSON.

## RBAC permissions

Each tool requires specific RBAC permissions. When you create a service account for the MCP server, grant the minimum actions for the tools you enable. You often need matching scopes as well (for example `datasources:*`, `dashboards:*`, `folders:*`).

Tip: If you want a faster setup instead of tuning many scopes, assign a built-in role such as **Editor** to the service account. **Editor** grants broad read and write access for most MCP operations; it is less granular than least privilege.

Grafana Incident and Sift tools use basic Grafana roles instead of fine-grained RBAC permissions:

- **Viewer:** read-only operations (for example list incidents, get investigations).
- **Editor:** write operations (for example create incidents, run analyses that modify state).

Refer to [Grafana RBAC](https://grafana.com/docs/grafana/latest/administration/roles-and-permissions/access-control/) for full detail.

## RBAC scopes

Scopes define which resources a permission applies to. You need the right permission **and** scope together.

**Broad access** (organization-wide) often uses `*` wildcards:

- `datasources:*`
- `dashboards:*`
- `folders:*`
- `teams:*`

**Limited access** uses specific UIDs or IDs:

- `datasources:uid:prometheus-uid`
- `dashboards:uid:abc123`
- `folders:uid:xyz789`
- `teams:id:5`
- `global.users:id:123`

**Examples:**

Full MCP access (typical broad grants):

```
datasources:* (datasources:read, datasources:query)
dashboards:* (dashboards:read, dashboards:create, dashboards:write)
folders:* (for dashboard creation and alert rules)
teams:* (teams:read)
global.users:* (users:read)
```

Limited datasource access (only specific Prometheus and Loki instances):

```
datasources:uid:prometheus-prod (datasources:query)
datasources:uid:loki-prod (datasources:query)
```

Dashboard-only read access:

```
dashboards:uid:monitoring-dashboard (dashboards:read)
dashboards:uid:alerts-dashboard (dashboards:read)
```

## Enable or disable tools

You can limit which tools the server exposes with `--enabled-tools`, `--disable-<category>`, and `--disable-write`. Refer to [Enable and disable tools](../../configure/enable-and-disable-tools/) and [Command-line flags](../../configure/command-line-flags/).

## Panel and dashboard images

`get_panel_image` needs the [Grafana Image Renderer](https://grafana.com/docs/grafana/latest/setup-grafana/image-rendering/) service installed and configured in Grafana.

## Next steps

- [Command-line flags](../../configure/command-line-flags/)
- [Enable and disable tools](../../configure/enable-and-disable-tools/)
- [Introduction](../../introduction/)
