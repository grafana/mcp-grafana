---
title: Search and inspect dashboards
menuTitle: Search and inspect dashboards
description: Use the MCP server to search dashboards and get summaries or panel queries without loading full JSON.
keywords:
  - dashboards
  - search
  - panels
  - MCP
weight: 3
aliases: []
---

# Search and inspect dashboards

Use the Grafana MCP server so your AI assistant can search for dashboards and inspect them (summaries, panel queries, or specific properties) without pulling full dashboard JSON. That keeps context window use under control.

## What you'll achieve

You ask your assistant to find dashboards or to summarize a dashboard, list its panels and queries, or extract specific fields. The assistant uses search, get summary, get panel queries, or get property tools as appropriate.

## Before you begin

- The server [set up](../../set-up/) and [configured](../../configure/authentication/) with access to Grafana.
- The service account must have `dashboards:read` and scope for the dashboards you care about.

## Search for dashboards

Ask the assistant to search for dashboards by title or other criteria. The server’s search tool returns matching dashboards with metadata. Use this to find the UID or folder of the dashboard you want.

## Get a dashboard summary or panel queries

When you need an overview of a dashboard without the full JSON, ask for a **dashboard summary**. You get title, panel count, panel types, variables, and similar metadata. To see what each panel queries, ask for **panel queries**: the assistant uses the tool that returns panel title, query string, and datasource UID and type for every panel.

## Get specific properties with JSONPath

When you only need certain parts of a dashboard (for example, one panel’s config), ask the assistant to use **get_dashboard_property** with a JSONPath expression (for example, `$.panels[0].title`). That fetches only the requested data and avoids loading the full dashboard.

## Next steps

- [Generate deeplinks to Grafana](../generate-deeplinks-to-grafana/) to get shareable links to dashboards and panels.
- [Manage alert rules](../manage-alert-rules/) to create or update alert rules from your assistant.
