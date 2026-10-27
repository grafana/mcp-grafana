---
title: Manage alert rules
menuTitle: Manage alert rules
description: Use the MCP server to list, create, update, and delete Grafana alert rules and manage routing.
keywords:
  - alerting
  - alert rules
  - contact points
  - MCP
weight: 4
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/guides/manage-alert-rules/
---

# Manage alert rules

Use the Grafana MCP server so your AI assistant can list and fetch alert rules, create or update rules, delete rules, and inspect notification policies and contact points.

## What you'll achieve

You ask your assistant to show firing alerts, add a new alert rule, change a rule’s condition or labels, or list contact points. The assistant uses the server’s alerting tools with the permissions you’ve granted.

## Before you begin

- The server [set up](../../set-up/) and [configured](../../configure/authentication/) with access to Grafana.
- The service account must have `alert.rules:read` (and `alert.rules:write` for create/update/delete) and appropriate folder scopes. For routing (contact points, policies), `alert.notifications:read` is required.

## List and inspect alert rules

Ask the assistant to list alert rules or get details for a rule by UID. You can filter by folder or labels. The assistant uses the alerting tools to return rule title, state (firing, pending, normal), and configuration.

## Create or update alert rules

Ask the assistant to create a new alert rule or update an existing one. You provide (or the assistant infers) the folder UID, condition, queries, labels, and annotations. The service account needs write permissions and scope for the folder. The assistant uses the server’s create/update alert rule tool.

Creation defaults to **Grafana-managed**. The assistant can select `managed_by: "datasource"` when the user requests it or when established conversation or environment context clearly identifies a datasource-managed workflow. For example, the user's alerting setup may already be known to manage rules exclusively in a writable external backend. Using an external datasource in a query or finding no Grafana-managed rules alone does not establish that intent. If context conflicts, the assistant should ask before creating anything.

The assistant must still send `managed_by: "datasource"` in the tool call. Omitting it keeps Grafana-managed creation. The server does not infer a management target from datasource discovery or switch targets after an error.

### Create a datasource-managed rule

`alerting_rules_write` supports creating alerting rules in Mimir/Cortex and Loki rulers. Standalone Prometheus does not support rule creation through this API. Enable rule management in the datasource configuration and configure the appropriate Prometheus type for Mimir/Cortex.

```json
{
  "operation": "create",
  "managed_by": "datasource",
  "datasource_uid": "mimir-rules",
  "namespace": "pmo",
  "rule_group": "availability",
  "title": "ServiceDown",
  "expression": "up{job=\"pmo-service\"} == 0",
  "for": "5m",
  "labels": {"team": "pmo", "severity": "critical"}
}
```

An explicit `datasource_uid` takes priority and is validated. If omitted, the tool discovers compatible datasources visible to the caller. It selects one only when exactly one exists. With multiple candidates, it returns their names and UIDs so the assistant can ask the user to choose. With none, it returns an error. It never falls back to Grafana-managed creation.

The tool appends the rule to the existing group and preserves its other rules and settings. New groups default to a `1m` evaluation interval. You can set `evaluation_interval` for a new group, but it must match the interval when adding to an existing group. If the existing group omits its interval and uses the ruler default, omit `evaluation_interval` to preserve it. Duplicate alert names in the same group are rejected. External creation uses `expression` and `namespace`, not Grafana-only fields such as `data`, `condition` or `folder_uid`. Update and delete remain Grafana-only.

For an explicitly selected Prometheus datasource without `jsonData.prometheusType`, the tool checks build information to select the Mimir or Cortex ruler route, then reads the group before writing. Automatic selection requires configured Mimir/Cortex metadata and does not probe every datasource.

## Manage contact points and routing

Ask the assistant to list contact points or notification policies. The server exposes tools for Grafana-managed routing; for Alertmanager datasources, it can query receivers. Use this to see where alerts are sent or to describe routing before changing it (actual routing changes may require the Grafana UI or API outside MCP).

### Create an external Alertmanager contact point

`alerting_routing_write` also defaults to **Grafana-managed**. Select `managed_by: "datasource"` when the user requests an external receiver or established context clearly identifies an external Alertmanager workflow. An external rule datasource or an empty Grafana contact point list alone is insufficient. Use a Mimir/Cortex **Alertmanager datasource**, which is selected independently from the rule datasource. Standalone Prometheus Alertmanager does not expose a writable configuration API.

```json
{
  "operation": "create_contact_point",
  "managed_by": "datasource",
  "datasource_uid": "mimir-alertmanager",
  "name": "pmo-notifications",
  "type": "webhook",
  "settings": {"url": "https://example.com/alerts"}
}
```

The same optional UID selection rules apply independently to Alertmanager datasources. Use native Alertmanager settings, such as `to` for email, rather than Grafana's `addresses`. `disable_resolve_message` controls resolved notifications. Grafana-only `uid` and `disable_provenance` are rejected for external creation.

The receiver is added without changing existing receivers, notification routes, templates, or other configuration fields in the request. A receiver name must be unique. An initial Alertmanager configuration with a root route must already exist. Creating a receiver does not connect it to a route or configure the ruler to send alerts to that Alertmanager. Configure those separately.

The result contains the receiver name, type and datasource UID, with an empty integration UID because external receivers are identified by name. It does not contain settings or secrets. If the API returns redacted secrets, the tool refuses to overwrite the configuration.

### Permissions and concurrent writes

External rule creation requires `datasources:read`, `alert.rules.external:read` and `alert.rules.external:write` for the target datasource. External receiver creation requires `datasources:read`, `alert.notifications.external:read` and `alert.notifications.external:write`. The backend must also allow the write. Both creation paths remain disabled by `--disable-write`.

These APIs replace a complete rule group or Alertmanager configuration. The server serializes its own external creation calls for the same Grafana instance, datasource UID and destination group or Alertmanager configuration, regardless of organization selector. An implicit organization may be the same as an explicit one, so organizations that reuse the same destination identifiers conservatively share a lock. Other instances, datasources and rule groups can proceed independently, and unused locks are removed. This cannot prevent concurrent edits from other MCP processes, the Grafana UI, or other clients. Coordinate such writes to avoid overwriting concurrent changes. Unsupported APIs, permission errors and ambiguous destinations fail without creating anything in Grafana instead.

## Next steps

- [Introduction](../../introduction/) for RBAC and permissions.
- [Search and inspect dashboards](../search-and-inspect-dashboards/) to find dashboards that use alerts.
