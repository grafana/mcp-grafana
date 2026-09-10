---
title: Tempo tools
menuTitle: Tempo tools
description: Trace search, metrics, and attribute exploration tools for Grafana Tempo datasources.
keywords:
  - MCP
  - Tempo
  - TraceQL
  - traces
  - datasource
weight: 4
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/configure/proxied-tools/
---

# Tempo tools

Tempo tools let you search traces, compute trace-derived metrics, fetch and diff traces, and explore trace attributes through any Tempo datasource configured in Grafana. The tools call Tempo's REST API through the Grafana datasource proxy.

All Tempo tool names are prefixed with `tempo_` and require a `datasourceUid` parameter to identify which Tempo datasource to query.

## Prerequisites

Complete [authentication](../authentication/) to Grafana (`GRAFANA_URL` and credentials).

Add a Tempo datasource in Grafana if you do not already have one.

## Available tools

| Tool | Description |
|------|-------------|
| `tempo_traceql-search` | Search for traces using TraceQL queries |
| `tempo_traceql-metrics-instant` | Compute a single metric value from trace data |
| `tempo_traceql-metrics-range` | Compute a metric time series from trace data |
| `tempo_get-trace` | Fetch a complete trace by ID |
| `tempo_trace-diff` | Compare two traces |
| `tempo_get-attribute-names` | List available trace attribute names |
| `tempo_get-attribute-values` | Get values for a specific attribute |

## Disable Tempo tools

Tempo tools are enabled by default. Pass `--disable-tempo` to disable them, or remove `tempo` from `--enabled-tools`.

Tempo tools respect `--disable-query` — when query tools are disabled, no Tempo tools are registered.

## Next steps

- [Enable and disable tools](../enable-and-disable-tools/)
- [Command-line flags](../command-line-flags/)
- [Transports and addresses](../transports-and-addresses/)
