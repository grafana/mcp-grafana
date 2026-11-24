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
- **sql** – SQL datasource tools (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL, BigQuery). Back-compat aliases `clickhouse`, `snowflake`, `athena` also work.
- **cloudwatch** – CloudWatch tools.
- **xray** – AWS X-Ray trace search and trace tools (requires the `grafana-x-ray-datasource` plugin).
- **cloudlogging** – Google Cloud Logging tools (requires the `googlecloud-logging-datasource` plugin, version 1.8.0 or later).
- **azuremonitor** – Azure Monitor tools for Log Analytics and Resource Graph KQL queries.
- **elasticsearch** – Elasticsearch query tool.
- **quickwit** – Quickwit query tool.
- **influxdb** – InfluxDB query tool (Flux and InfluxQL).
- **agento11y** – Agent Observability tools for LLM conversations, generations, evaluation scores, agents, evaluators, eval rules, guards, saved conversations, collections, offline experiments, and the test suites those experiments run against.
- **assistant** – Grafana Assistant tool (`ask_assistant`); requires the Grafana Assistant plugin and is write-gated.
- **dbo11y** – Database Observability tools for monitored MySQL and PostgreSQL instances: instances and their sessions and wait events, top queries, per-query samples, explain plans, wait events, schema and timelines, and the recommendations raised about them. They respect `--disable-query`.
- **admin** – Admin tools (teams, users, roles, permissions).

`--enabled-tools` **replaces** the default list, so to add an optional category you must pass the full set. For example, to keep the defaults and also enable `runpanelquery` and `examples`:

```bash
mcp-grafana --enabled-tools search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,pyroscope,navigation,tempo,annotations,rendering,snapshot,runpanelquery,examples
```

Refer to [Command-line flags](../command-line-flags/) for the default list.

## Disable tool categories

Use `--disable-<category>` to turn off a whole category (for example, `--disable-oncall`, `--disable-alerting`, `--disable-dashboard`). For every flag, read-only behavior, and TLS-related flags, refer to [Command-line flags](../command-line-flags/).

Tempo tools can be disabled with `--disable-tempo` or removed from `--enabled-tools`. They also respect `--disable-query`.

## Run in read-only mode

Use `--disable-write` to disable all write operations. The server can still read dashboards, run queries, and list resources, but it cannot create or update dashboards, incidents, alert groups, alert rules, annotations, snapshots, or Agent Observability evaluators, eval rules, guards, saved conversations, and collections.

Read-only mode also removes `query_sql` and `query_influxdb`: they pass the query to the datasource unfiltered, so they can write whenever the datasource credentials permit it. Add `--enable-query` to keep them when those credentials are known to be read-only.

In read-only mode, `generate_deeplink` ignores `shorten=true` and returns the full URL, because Grafana creates short URLs through a `POST` request. Add `--enable-write-tools=generate_deeplink` to keep URL shortening without re-enabling any other write tool.

Every other write tool can be opted back in individually the same way. For example, `--enable-write-tools=alerting_rules_write` allows alert rule changes while silences, contact points, and every other write tool stay disabled, and `--enable-write-tools=update_dashboard` keeps dashboard edits in an otherwise read-only server.

## Run without query execution

Use `--disable-query` to remove every tool that executes a query against a datasource, such as `query_prometheus`, `query_loki_logs`, `query_sql`, and `run_panel_query`. The server can still list datasources and dashboards, explore metric names, labels, and table schemas, and generate deeplinks, but it can't run the queries themselves.

`--disable-query` is the strongest of the three query settings: it wins over `--enable-query`, and it removes the query tools that `--disable-write` leaves in place. Refer to [Command-line flags](../command-line-flags/) for the full list of affected tools.

## Select tools with a configuration file

Instead of tool flags, you can select tools in a YAML file and pass its path with `--config`, or set `GRAFANA_MCP_CONFIG`. For example, this file enables five categories, runs them read-only, keeps dashboard edits, and removes the SQL query tool:

```yaml
tools:
  categories: [search, dashboard, prometheus, loki, sql]
  mode: read-only
  query: true
  allow: [update_dashboard]
  deny: [query_sql]
```

Every key is optional:

- `categories` is the exact list of enabled categories, like `--enabled-tools`. If you leave it out, the default categories are enabled. Only current category names work, so use `sql` and `tempo`, not `clickhouse` or `proxied`.
- `mode` is `read-write` (the default) or `read-only`. `read-only` is the same as `--disable-write`.
- `query` is `true` (the default) or `false`. `false` is the same as `--disable-query`.
- `allow` keeps tools that `mode: read-only` removes, like `--enable-write-tools`. For a tool with a read-only variant, such as `grafana_api_request` or `generate_deeplink`, it keeps the full variant. It doesn't bring back tools that `query: false` removes.
- `deny` removes tools even when their category is enabled. It removes every variant of the tool.

The server doesn't start if the file has an unknown key, an unknown category or tool name, a tool in both `allow` and `deny`, or a tool in `allow` whose own category isn't enabled. The error names each problem.

Tool flags that you set explicitly override the file for the setting they map to, even when you set them to their default, such as `--disable-write=false`:

- `--enabled-tools` replaces `categories`.
- `--disable-<category>` removes that category.
- `--disable-write` replaces `mode`.
- `--disable-query` replaces `query`.

`--enable-write-tools` and `--enable-query` work differently. They **add** to `allow` instead of replacing it, and a tool they name is kept even when the file lists it in `deny`. Unlike the file, these two flags silently skip a tool whose category isn't enabled.

## Next steps

- [Introduction](../../introduction/) for an overview of tools and RBAC.
- [Configure authentication](../authentication/) if you have not set credentials yet.
