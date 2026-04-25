---
title: Run a dashboard panel query
menuTitle: Run a panel query
description: Use the MCP server to execute a dashboard panel's query with custom time range and variable overrides.
keywords:
  - panel query
  - dashboard
  - runpanelquery
  - MCP
weight: 6
aliases: []
---

# Run a dashboard panel query

Use the Grafana MCP server to run a dashboard panel’s query (the same query the panel uses) with a custom time range and variable overrides. This is useful when you want the assistant to “run what this panel runs” without writing PromQL or LogQL yourself.

## What you'll achieve

You point the assistant at a dashboard and panel; the assistant uses the server’s run-panel-query tool to execute that panel’s query and return the result (or an error if the query or datasource is invalid).

## Before you begin

- The server [set up](../../set-up/) and [configured](../../configure/authentication/) with access to Grafana.
- The run-panel-query tool must be enabled (it’s disabled by default). Add **runpanelquery** to [Enable and disable tools](../../configure/enable-and-disable-tools/).
- The service account must have `dashboards:read` and `datasources:query` with scope for the dashboard and the datasource the panel uses.

## Run the panel query

Ask the assistant to run the query for a specific panel on a dashboard. Provide (or let the assistant look up) the dashboard UID and panel ID. You can specify a time range (for example, last 1 hour) and variable overrides so the query runs with the same logic as the panel but with your chosen parameters. The assistant calls the server’s run-panel-query tool and returns the result.

## Next steps

- [Query metrics with Prometheus](../query-metrics-with-prometheus/) or [Query logs with Loki](../query-logs-with-loki/) to run custom PromQL or LogQL.
- [Search and inspect dashboards](../search-and-inspect-dashboards/) to find panel IDs and datasources.
