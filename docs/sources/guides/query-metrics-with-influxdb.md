---
title: Query metrics with InfluxDB
menuTitle: Query metrics with InfluxDB
description: Use the MCP server to run Flux or InfluxQL queries against an InfluxDB datasource from your AI assistant.
keywords:
  - InfluxDB
  - Flux
  - InfluxQL
  - metrics
  - time series
  - MCP
weight: 8
aliases: []
---

# Query metrics with InfluxDB

Use the Grafana MCP server so your AI assistant can run InfluxDB queries against an InfluxDB datasource in Grafana. The server supports both InfluxQL (v1.x) and Flux (v2.x); the dialect is inferred from the datasource's configured version or can be set explicitly.

## What you'll achieve

You ask your assistant to query an InfluxDB-backed metric; the assistant uses the server's InfluxDB query tool to post the query through Grafana's datasource proxy and return the points. The server is a thin shim over Grafana's `/api/ds/query` endpoint, so Grafana handles all protocol details for InfluxDB v1 and v2.

## Before you begin

- The server [set up](../../set-up/) and [configured](../../configure/authentication/) with access to Grafana.
- An InfluxDB datasource configured in Grafana. The service account must have `datasources:query` and scope for that datasource (for example, `datasources:uid:<uid>`).

## Run a query

In your MCP client, ask the assistant to query InfluxDB. For example, "What was the mean CPU usage on the host `server1` in the last hour?" or "Run this Flux: `from(bucket: \"metrics\") |> range(start: -1h) |> filter(fn: (r) => r._measurement == \"cpu\")`." The assistant uses the server's `query_influxdb` tool with the datasource UID, query string, and time range.

If the datasource is configured for InfluxDB v2 (Flux), pass a Flux query. If it's configured for v1 (InfluxQL), pass an InfluxQL query. You can override the dialect explicitly by setting `dialect` to `"flux"` or `"influxql"`.

## Run a panel's query from a dashboard

If a dashboard you care about already contains the InfluxDB panel you want, use `run_panel_query` instead of writing the query yourself. The MCP server fetches the dashboard, extracts the panel's query and `queryType`, substitutes any template variables, and executes the query against the configured datasource. This is the fastest path from "what does this panel show?" to actual data.

## Discover what's in a bucket or database

The server does not expose dedicated measurement- or tag-listing tools. Instead, run discovery queries via `query_influxdb`:

- **Flux (v2):** `from(bucket: "my-bucket") |> range(start: -1h) |> limit(n: 5)` to see available fields and tags on recent points.
- **InfluxQL (v1):** `SHOW MEASUREMENTS`, `SHOW TAG KEYS FROM "cpu"`, `SHOW FIELD KEYS FROM "cpu"`.

Use `get_query_examples` with `datasourceType: "influxdb"` to see more starter queries for both dialects.

## Next steps

- [Query metrics with Prometheus](../query-metrics-with-prometheus/) for PromQL use cases.
- [Run a dashboard panel query](../run-a-dashboard-panel-query/) to execute a panel's query from the MCP server.
