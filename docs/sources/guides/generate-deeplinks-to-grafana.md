---
title: Generate deeplinks to Grafana
menuTitle: Generate deeplinks
description: Use the MCP server to create accurate dashboard, panel, and Explore links instead of guessing URLs.
keywords:
  - deeplink
  - dashboard
  - Explore
  - MCP
weight: 5
aliases: []
---

# Generate deeplinks to Grafana

Use the Grafana MCP server so your AI assistant can generate correct deeplink URLs to dashboards, panels, and Grafana Explore. The server uses the Grafana API to build URLs so you get working links instead of guessed paths.

## What you'll achieve

You ask your assistant for a link to a dashboard, a specific panel, or Explore with a given datasource (and optional time range). The assistant uses the server’s deeplink tool and returns a URL you can open or share.

## Before you begin

- The server [set up](../../set-up/) and [configured](../../configure/authentication/) with access to Grafana.
- No extra RBAC is required for generating links (read-only URL generation).

## Get dashboard and panel links

Ask the assistant to generate a link to a dashboard by UID, or to a specific panel (dashboard UID and panel ID). You can request a time range (for example, last hour) so the link opens with that range. The assistant uses the server tool and returns a URL like `http://localhost:3000/d/<uid>?viewPanel=<id>&from=now-1h&to=now`.

## Get Explore links

Ask for a link to Grafana Explore with a specific datasource (by UID). Optionally specify time range or other query parameters. The assistant returns a URL that opens Explore with that datasource and options pre-filled.

## Next steps

- [Search and inspect dashboards](../search-and-inspect-dashboards/) to find dashboard and panel IDs.
- [Query metrics with Prometheus](../query-metrics-with-prometheus/) or [Query logs with Loki](../query-logs-with-loki/) to work with datasources.
