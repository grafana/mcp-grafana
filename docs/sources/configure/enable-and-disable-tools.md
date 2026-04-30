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
aliases: []
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
- **clickhouse** – ClickHouse datasource tools.
- **cloudwatch** – CloudWatch tools.
- **elasticsearch** – Elasticsearch query tool.
- **influxdb** – InfluxDB query tool (Flux and InfluxQL).
- **admin** – Admin tools (teams, users, roles, permissions).

`--enabled-tools` **replaces** the default list, so to add an optional category you must pass the full set. For example, to keep the defaults and also enable `runpanelquery` and `examples`:

```bash
mcp-grafana --enabled-tools search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,pyroscope,navigation,proxied,annotations,rendering,runpanelquery,examples
```

Refer to [Command-line flags](../command-line-flags/) for the default list.

## Disable tool categories

Use `--disable-<category>` to turn off a whole category (for example, `--disable-oncall`, `--disable-alerting`, `--disable-dashboard`). For every flag, read-only behavior, and TLS-related flags, refer to [Command-line flags](../command-line-flags/).

For tools that come from external MCP servers through Grafana (for example from Grafana Tempo), refer to [Proxied tools](../proxied-tools/).

## Run in read-only mode

Use `--disable-write` to disable all write operations. The server can still read dashboards, run queries, and list resources, but it cannot create or update dashboards, incidents, alert rules, annotations, or investigations.

## Next steps

- [Introduction](../../introduction/) for an overview of tools and RBAC.
- [Configure authentication](../authentication/) if you have not set credentials yet.
