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
aliases: []
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

## Manage contact points and routing

Ask the assistant to list contact points or notification policies. The server exposes tools for Grafana-managed routing; for Alertmanager datasources, it can query receivers. Use this to see where alerts are sent or to describe routing before changing it (actual routing changes may require the Grafana UI or API outside MCP).

## Next steps

- [Introduction](../../introduction/) for RBAC and permissions.
- [Search and inspect dashboards](../search-and-inspect-dashboards/) to find dashboards that use alerts.
