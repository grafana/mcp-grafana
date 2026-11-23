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
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/guides/query-metrics-with-prometheus/
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

`list_prometheus_metric_names` defaults to 10 results per page. The product of `page` and `limit` must not exceed 10,000. If it does and you need to explore > 10,000 results, narrow the regex to get fewer results. For Prometheus-compatible backends, regex filtering and the fetch limit are sent to the datasource, which must support the `limit` parameter to bound the response. Cloud Monitoring continues to filter metric descriptors locally.

## Empty results

When a query returns no data, the response includes `hints`. If the server can tell why, `hints.reason` names the cause and `hints.suggestedActions` holds one fix:

- `metric_not_found`: a metric in the query has no series in the time range. If the name with `_total` added or removed exists, the hint names it.
- `native_histogram`: the query asks for `foo_count`, `foo_sum` or `foo_bucket`, but `foo` is a native histogram. Use `histogram_count()`, `histogram_sum()` or `histogram_quantile()` on `foo`.
- `label_not_found`: the metric does not have a label the selector requires. The hint lists the labels it has.
- `no_matching_series`: every metric exists, but the label values or vector matching removed all series.
- `shifted`: the query uses `offset` or the `@` modifier, so it reads a different window than the one requested. The server does not diagnose these, because its lookups would check the wrong time range. Run the query without the modifier or check the shifted range.

To find the cause, the server makes at most two label lookups, and only for empty results. When it cannot tell, `reason` is left out and the hint lists the common causes.

## Response size limit

Prometheus API responses are limited to 10 MiB after decompression. Responses above this limit return an error instead of partial results. If a query exceeds the limit, select fewer series, shorten the time range, or increase the step for a range query. For metric and label discovery, narrow the filters or requested time range.

## Next steps

- [Query logs with Loki](../query-logs-with-loki/) for log and LogQL use cases.
- [Run a dashboard panel query](../run-a-dashboard-panel-query/) to execute a panel’s query from the MCP server.
