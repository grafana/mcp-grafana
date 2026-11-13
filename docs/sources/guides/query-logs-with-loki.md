---
title: Query logs with Loki
menuTitle: Query logs with Loki
description: Use the MCP server to run LogQL queries against a Loki datasource from your AI assistant.
keywords:
  - Loki
  - LogQL
  - logs
  - MCP
weight: 2
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/guides/query-logs-with-loki/
---

# Query logs with Loki

Use the Grafana MCP server so your AI assistant can run LogQL log and metric queries against a Loki datasource in Grafana. You can also list label names and values and retrieve log patterns.

## What you'll achieve

You ask your assistant to search logs or run a LogQL expression; the assistant uses the server’s Loki tools to run the query and return log lines or metric values. You can also explore labels and patterns.

## Before you begin

- The server [set up](../../set-up/) and [configured](../../configure/authentication/) with access to Grafana.
- A Loki datasource configured in Grafana. The service account must have `datasources:query` and scope for that datasource.

## Run a LogQL query

In your MCP client, ask the assistant to query Loki. For example, “Show me recent error logs from the Loki datasource” or “Run this LogQL: {job=\"myapp\"} |= \"error\".” The assistant uses the server’s Loki query tool with the datasource UID, time range, and limit. You can run log queries (streams of log lines) or metric queries (counts, rates, etc.).

## Read results in Loki's format

Set `format` to `loki` and `query_loki_logs` returns its text output in the shape of Loki's `/loki/api/v1/query_range` response: `{"status":"success","data":{"resultType":...,"result":[...]}}`, with the tool's `metadata` and `hints` fields next to `data`. Scripts written for the Loki HTTP API can read it without converting. Log results are `streams`, and each `stream` holds the stream labels, structured metadata and parsed labels together, as Loki returns them without the `categorize-labels` encoding flag. Metric results are `matrix` for range queries and `vector` for instant queries. The log limit applies as in the other formats, and `metadata.resultsTruncated` shows whether lines were cut. Loki's `stats` object is not included.

## Explore labels and patterns

Ask the assistant to list label names or label values for a selector, or to retrieve detected log patterns. These tools help you discover what’s in your logs before writing LogQL.

## Next steps

- [Query metrics with Prometheus](../query-metrics-with-prometheus/) for PromQL use cases.
- [Search and inspect dashboards](../search-and-inspect-dashboards/) to find and explore dashboards.
