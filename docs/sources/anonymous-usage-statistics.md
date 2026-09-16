---
title: Anonymous usage statistics
menuTitle: Usage statistics
description: Information about the opt-out anonymous usage statistics events emitted by the Grafana MCP server.
keywords:
  - usage statistics
  - telemetry
  - privacy
  - MCP
  - Model Context Protocol
weight: 45
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/anonymous-usage-statistics/
---

# Understand Grafana MCP server usage statistics

The Grafana MCP server can report limited usage statistics about itself to Grafana Labs. This data is used to understand which tools are used most and where tools fail, so we can make the product better.

To preserve anonymity, the emitted data describes the *shape* of usage only. Things like tool arguments, resource names, dashboards, queries, log lines, error messages and credentials are never sent, and neither is the Grafana instance's URL, hostname, stack slug, organisation name or organisation ID.

Nothing an MCP client reports about itself - things like name and version - is collected. Any other value that comes from outside the binary is reduced to a fixed vocabulary before being emitted, so we don't emit a potentially-identifying value.

{{< admonition type="note" >}}
Usage statistics reporting is **currently disabled by default**. This will soon change to enabled by default. There are details on how to opt out in [Opt out](#opt-out).
{{< /admonition >}}

## Identifiers

There is one identifier, `process_id`: a random UUID generated in memory when the server process starts.

## Understand which data is collected

Every event carries the following data:

| Field | Description | Example |
| :---- | :---- | :---- |
| `service` | Always `mcp-grafana`. | `mcp-grafana` |
| `version` | The version of the `mcp-grafana` binary. | `1.4.2` |
| `os` | Operating system. | `linux`, `darwin`, `windows` |
| `arch` | CPU architecture. | `amd64`, `arm64` |
| `process_id` | A random per-process UID. | UUID |
| `report_reason` | Why this event was sent: `interval` (once every 4h) or `shutdown`. | `interval` |
| `process_uptime_ms` | How long the process had been running when the event was built, in milliseconds.| `1234` |
| `report_seq` | This process's reports numbered from 1. A gap in the sequence for a `process_id` is a report that was built and never arrived. | `3` |
| `tools_called` | Comma-separated names of the tools called since the previous event. | `list_datasources,query_prometheus` |
| `tool_calls` | A nested object keyed by tool name, each holding `calls` and `errors`. | `{"query_prometheus":{"calls":4,"errors":1}}` |
| `grafana_version` | The version the Grafana instance reported. This is read from a response the server had already fetched for its own reasons, and truncated to 64 bytes. Absent whenever that version is not already known. | `12.1.0` |
| `target_kind` | `cloud` or `self_hosted`. | `cloud` |
| `auth_method` | The credential category the connection resolved, from a fixed vocabulary. | `service_account_token` |
| `transport` | The transport the server is running: `stdio`, `sse` or `streamable-http`. | `stdio` |
| `flags` | The **names** of the command-line flags that were set. No flag values are sent. | `disable-write,transport` |
| `env_set` | The **names** of the configuration environment variables that are set. No free-form values are sent. | `GRAFANA_SERVICE_ACCOUNT_TOKEN,GRAFANA_URL` |
| `enabled_tools` | The tool category names that are active. | `alerting,dashboard,search` |
| `disabled_tools` | The tool category names a `--disable-<category>` flag turned off. | `oncall` |
| `tls_enabled` | Whether the server is serving HTTPS (only the `streamable-http` transport does). | `false` |
| `metrics_enabled` | Whether the Prometheus `/metrics` endpoint is served. | `true` |
| `dynamic_multi_org` | Whether per-call organisation selection is enabled. | `false` |
| `proxied_enabled` | Whether proxied tools from external MCP servers are enabled. | `true` |


## Server-side enrichment

Reports are received by Grafana's usage-stats service, the same service that receives usage reports from Grafana, Loki, Mimir and Tempo. On receipt, the service adds two pieces of information derived from the connection rather than from the event:

- A coarse **geographic region**, for example a country or subdivision, taken from headers added by the CDN edge.
- The **network organisation name** from a whois lookup of the connecting IP address, which typically resolves to your ISP, your cloud provider or your employer's network.

## Inspect what would be sent

To see exactly what the server would report, set either the `--usage-stats=log` flag or the `GRAFANA_USAGE_STATS=log` environment variable. In this mode each event is printed to stderr and nothing is sent:

```shell
GRAFANA_USAGE_STATS=log mcp-grafana
```

Under the `stdio` transport, stderr goes wherever your MCP client sends the server's logs, so read the printed events there rather than in a terminal.

## How the report is sent

| Property | Value |
| --- | --- |
| Destination | `https://stats.grafana.org/mcp-grafana-usage-report` |
| Method | A single `POST` with the event as a JSON object |
| Attempts | One. A failed report is never retried and never stored. |
| Timing | Every four hours, and once more as the process exits |
| Time limit | One second for the shutdown report, so exit is never delayed longer than that |

Because a failed report is never retried, and the counters reset when a report is built rather than when it arrives, a total summed across a `process_id` is a floor rather than an exact count. `report_seq` is how a missing report is identified.

To send reports somewhere else, set `GRAFANA_USAGE_STATS_ENDPOINT` to another URL. This changes the destination only; it is not an opt-out.

## Opt out

There are three controls. Precedence is highest first:

1. **`--usage-stats` flag**: set it to `enabled`, `disabled` or `log`.

```shell
mcp-grafana --usage-stats=disabled
```

2. **`GRAFANA_USAGE_STATS` environment variable**: set it to `enabled`, `disabled` or `log`.

```shell
export GRAFANA_USAGE_STATS=disabled
```

3. **`DO_NOT_TRACK` environment variable**: set it to `1` to disable reporting, following the cross-tool [DO_NOT_TRACK](https://donottrack.sh/) convention.

```shell
export DO_NOT_TRACK=1
```
