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
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/guides/run-a-dashboard-panel-query/
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

## Inspect the prepared query

Call `get_dashboard_panel_queries` with the same `variables`, `start`, and `end` as `run_panel_query`. Pass `variables: {}` to use saved selections. Both tools use the same preparation for classic v1 and schema v2 dashboards, including datasource variables and frontend time macros. Omitted time bounds default independently to `now-1h` and `now`. Use absolute bounds when comparing separate calls exactly.

SQL `${variable:sqlstring}` preserves multiple selected values and escapes quotes. For a multi-value variable, an override can contain a JSON string array such as `"[\"east\",\"west\"]"` or a quoted SQL list such as `"'east','west'"`. Ordinary saved values are treated as data, including commas and quotes within a value.

For an All selection, a custom `allValue` is used literally. Other saved selections use the variable's options. Query variables resolve All by querying their datasource with the requested time range. PostgreSQL, MySQL, MSSQL, and BigQuery option queries are supported, using the `__value` column when present or the first column otherwise. This requires datasource read/query permission and raw SQL query execution to be enabled. Under `--disable-query` or `--disable-write`, inspection remains available but warns when it cannot query options. `--enable-query` can opt back into raw SQL queries under `--disable-write`.

Failed option queries produce actionable `warnings` and omit `processedQuery` during inspection. Execution reports a panel error without submitting the unresolved panel query. Supply explicit values or fix the variable query before retrying.

### Interpolation limits

This is frontend preparation, not a complete implementation of every datasource plugin's interpolation. SQL plugin macros such as `$__timeFilter(column)` remain in the prepared query and are expanded by Grafana's backend with the supplied time bounds. Dynamic option queries for other datasource types and variable regex filtering are unsupported and produce warnings for All selections. Existing formatter fallback behavior is retained. Native plugin-specific formatting and visual query builders can still require their datasource's own tools.

## Next steps

- [Query metrics with Prometheus](../query-metrics-with-prometheus/) or [Query logs with Loki](../query-logs-with-loki/) to run custom PromQL or LogQL.
- [Search and inspect dashboards](../search-and-inspect-dashboards/) to find panel IDs and datasources.
