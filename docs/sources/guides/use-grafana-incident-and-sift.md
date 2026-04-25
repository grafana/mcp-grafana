---
title: Use Grafana Incident and Sift
menuTitle: Incident and Sift
description: Use the MCP server to manage Grafana Incident incidents and run Sift investigations (error patterns, slow requests).
keywords:
  - Incident
  - Sift
  - investigations
  - MCP
weight: 7
aliases: []
---

# Use Grafana Incident and Sift

Use the Grafana MCP server so your AI assistant can work with Grafana Incident (list, create, get, add activities) and Sift (list investigations, get analyses, find error patterns in logs, find slow requests). These features use Grafana basic roles: Viewer for read, Editor for write.

## What you'll achieve

You ask your assistant to list or create incidents, add a note to an incident, list Sift investigations, or run a Sift analysis (for example, find elevated error patterns in Loki or slow requests in Tempo). The assistant uses the server’s Incident and Sift tools.

## Before you begin

- The server [set up](../../set-up/) and [configured](../../configure/authentication/) with access to Grafana.
- Grafana Incident (and Sift, if used) available on your instance. The service account must have at least the **Viewer** role for read-only operations; **Editor** role for creating incidents or running Sift analyses that create investigations.

## Work with incidents

Ask the assistant to list incidents (optionally filtered by status), get one incident by ID, create a new incident (title, severity, room prefix, etc.), or add an activity (note) to an incident. The assistant uses the server’s Incident tools. Creating incidents or adding activities requires Editor role.

## Work with Sift investigations

Ask the assistant to list Sift investigations, get one by UUID, or get a specific analysis from an investigation. For proactive analysis, ask to **find error patterns in logs** (Loki) or **find slow requests** (Tempo); the server starts a Sift investigation and returns the results. These “find” operations create investigations and require Editor role.

## Next steps

- [Introduction](../../introduction/) for roles and permissions.
- [Manage alert rules](../manage-alert-rules/) for alerting from the MCP server.
