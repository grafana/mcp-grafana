---
title: Enable and disable tools
menuTitle: Enable and disable tools
description: Control which MCP tools the Grafana MCP server exposes and use read-only mode.
keywords:
  - tools
  - disable
  - read-only
  - MCP
weight: 3
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/configure/enable-and-disable-tools/
---

# Enable and disable tools

You can limit which tools the server exposes (to reduce context window use or lock down capabilities) and run the server in read-only mode.

## What you'll achieve

You enable only the tool categories you need, or disable write operations globally with `--disable-write`.

## Before you begin

- The server installed and configured ([Set up](../../set-up/) and [Authentication](../authentication/)).

## Enable optional tool categories

Some tool categories are disabled by default:

- **runpanelquery** – Run dashboard panel queries.
- **examples** – Query examples for datasource types.
- **sql** – SQL datasource tools (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL). Back-compat aliases `clickhouse`, `snowflake`, `athena` also work.
- **cloudwatch** – CloudWatch tools.
- **elasticsearch** – Elasticsearch query tool.
- **quickwit** – Quickwit query tool.
- **influxdb** – InfluxDB query tool (Flux and InfluxQL).
- **agento11y** – Agent Observability tools for LLM conversations, generations, evaluation scores, agents, evaluators, eval rules, guards, saved conversations, collections, offline experiments, and the test suites those experiments run against.
- **assistant** – Grafana Assistant tool (`ask_assistant`); requires the Grafana Assistant plugin and is write-gated.
- **admin** – Admin tools (teams, users, roles, permissions).

`--enabled-tools` **replaces** the default list, so to add an optional category you must pass the full set. For example, to keep the defaults and also enable `runpanelquery` and `examples`:

```bash
mcp-grafana --enabled-tools search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,pyroscope,navigation,proxied,annotations,rendering,snapshot,runpanelquery,examples
```

Refer to [Command-line flags](../command-line-flags/) for the default list.

## Disable tool categories

Use `--disable-<category>` to turn off a whole category (for example, `--disable-oncall`, `--disable-alerting`, `--disable-dashboard`). For every flag, read-only behavior, and TLS-related flags, refer to [Command-line flags](../command-line-flags/).

For tools that come from external MCP servers through Grafana (for example from Grafana Tempo), refer to [Proxied tools](../proxied-tools/).

## Run in read-only mode

Use `--disable-write` to disable all write operations. The server can still read dashboards, run queries, and list resources, but it cannot create or update dashboards, incidents, alert groups, alert rules, annotations, snapshots, investigations, or Agent Observability evaluators, eval rules, guards, saved conversations, and collections.

Read-only mode also removes `query_sql` and `query_influxdb`: they pass the query to the datasource unfiltered, so they can write whenever the datasource credentials permit it. Add `--enable-query` to keep them when those credentials are known to be read-only.

## Run without query execution

Use `--disable-query` to remove every tool that executes a query against a datasource, such as `query_prometheus`, `query_loki_logs`, `query_sql`, and `run_panel_query`. The server can still list datasources and dashboards, explore metric names, labels, and table schemas, and generate deeplinks, but it can't run the queries themselves.

`--disable-query` is the strongest of the three query settings: it wins over `--enable-query`, and it removes the query tools that `--disable-write` leaves in place. Refer to [Command-line flags](../command-line-flags/) for the full list of affected tools.

## Next steps

- [Introduction](../../introduction/) for an overview of tools and RBAC.
- [Configure authentication](../authentication/) if you have not set credentials yet.
