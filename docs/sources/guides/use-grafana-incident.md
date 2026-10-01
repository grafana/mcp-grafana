---
title: Use Grafana Incident
menuTitle: Incident
description: Use the MCP server to manage Grafana Incident incidents (list, create, get, add activities, update).
keywords:
  - Incident
  - MCP
weight: 7
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/guides/use-grafana-incident-and-sift/
---

# Use Grafana Incident

Use the Grafana MCP server so your AI assistant can work with Grafana Incident (list, create, get, add activities, update). These features use Grafana basic roles: Viewer for read, Editor for write.

## What you'll achieve

You ask your assistant to list or create incidents, add a note to an incident, or update an incident's status. The assistant uses the server's Incident tools.

## Before you begin

- The server [set up](../../set-up/) and [configured](../../configure/authentication/) with access to Grafana.
- Grafana Incident available on your instance. The service account must have at least the **Viewer** role for read-only operations; **Editor** role for creating or updating incidents.

## Work with incidents

Ask the assistant to list incidents (optionally filtered by status), get one incident by ID, create a new incident (title, severity, room prefix, etc.), or add an activity (note) to an incident. The assistant uses the server's Incident tools. Creating incidents or adding activities requires Editor role.

## Next steps

- [Introduction](../../introduction/) for roles and permissions.
- [Manage alert rules](../manage-alert-rules/) for alerting from the MCP server.
