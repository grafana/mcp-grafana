---
title: Query metrics with Prometheus
menuTitle: Query metrics with Prometheus
description: Use the MCP server to run PromQL queries against a Prometheus datasource from your AI assistant.
keywords:
  - Prometheus
  - PromQL
  - metrics
  - MCP
weight: 1
aliases: []
---

# Query metrics with Prometheus

Use the Grafana MCP server so your AI assistant can run PromQL queries against a Prometheus datasource in Grafana. You can run instant or range queries and discover metric and label names and values.

## What you'll achieve

You ask your assistant to check a metric or run a PromQL expression; the assistant uses the server’s Prometheus tools to run the query and return results (or describe available metrics and labels).

## Before you begin

- The server [set up](../../set-up/) and [configured](../../configure/authentication/) with access to Grafana.
- A Prometheus datasource configured in Grafana. The service account must have `datasources:query` and scope for that datasource (for example, `datasources:uid:<uid>`).

## Run a PromQL query

In your MCP client, ask the assistant to query Prometheus. For example, you might say “What is the current value of up for the Prometheus datasource?” or “Run this PromQL: rate(http_requests_total[5m]).” The assistant uses the server’s Prometheus query tool with the datasource UID and your time range. You can request instant queries (single value) or range queries (series over time).

## Discover metrics and labels

Ask the assistant to list metric names, label names, or label values for a given selector. The server exposes tools that call the Prometheus API (metric metadata, label names, label values). Use these to explore what’s available before writing PromQL.

## Next steps

- [Query logs with Loki](../query-logs-with-loki/) for log and LogQL use cases.
- [Run a dashboard panel query](../run-a-dashboard-panel-query/) to execute a panel’s query from the MCP server.
