---
title: Command-line flags
menuTitle: Command-line flags
description: CLI flags for the mcp-grafana binary, including transports, tools, TLS, and read-only mode.
keywords:
  - CLI
  - flags
  - MCP
  - TLS
weight: 7
aliases: []
---

# Command-line flags

The `mcp-grafana` binary accepts flags for transports, tools, TLS, and observability. Run `mcp-grafana --help` for the exact list in your installed build.

## What you'll achieve

You can look up defaults, choose `--disable-*` flags, or configure TLS without reading the source.

## Before you begin

- You need a way to run `mcp-grafana` on your machine—for example, a [release binary](../set-up/install-the-binary/), [`uvx`](../set-up/install-with-uvx/), or a [container](../set-up/install-with-docker/).

## Configure transport and HTTP options

- `-t` / `--transport`: Transport type (`stdio`, `sse`, or `streamable-http`). Default: `stdio`.
- `--address`: Host and port for the SSE or streamable-http server. Default: `localhost:8000`.
- `--base-path`: Base path for the SSE or streamable-http server.
- `--endpoint-path`: HTTP path for the streamable-http MCP endpoint. Default: `/mcp`.
- `--session-idle-timeout-minutes`: Idle timeout for streamable-http sessions, in minutes. Sessions with no activity for this duration are automatically reaped. Set to `0` to disable. Default: `30`.

## Configure debug and logging

- `--debug`: Enable debug mode for detailed HTTP request and response logging to and from the Grafana API.
- `--log-level`: Log level (`debug`, `info`, `warn`, `error`). Default: `info`.

## Configure observability endpoints

- `--metrics`: Expose a Prometheus metrics endpoint at `/metrics` (SSE and streamable-http only).
- `--metrics-address`: Optional separate listen address for metrics (for example, `:9090`). If empty, metrics are served on the main HTTP server.

## Configure tool categories

- `--enabled-tools`: Comma-separated list of enabled tool **categories**. The default is exactly:

  `search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,pyroscope,navigation,proxied,annotations,rendering`

  Categories **not** in that default string are off until you add them, including: `admin`, `elasticsearch`, `cloudwatch`, `examples`, `clickhouse`, `influxdb`, and `runpanelquery`. Pass a full comma-separated list to replace the default entirely, or use `--disable-*` flags to turn off pieces of the default set.

- `--disable-search`: Disable search tools.
- `--disable-datasource`: Disable datasource tools.
- `--disable-incident`: Disable incident tools.
- `--disable-prometheus`: Disable Prometheus tools.
- `--disable-write`: Disable write tools (read-only mode; refer to the following section).
- `--disable-loki`: Disable Loki tools.
- `--disable-elasticsearch`: Disable Elasticsearch tools.
- `--disable-influxdb`: Disable InfluxDB tools.
- `--disable-alerting`: Disable alerting tools.
- `--disable-dashboard`: Disable dashboard tools.
- `--disable-folder`: Disable folder tools.
- `--disable-oncall`: Disable OnCall tools.
- `--disable-asserts`: Disable Asserts tools.
- `--disable-sift`: Disable Sift tools.
- `--disable-admin`: Disable admin tools.
- `--disable-pyroscope`: Disable Pyroscope tools.
- `--disable-navigation`: Disable navigation (deeplink) tools.
- `--disable-rendering`: Disable rendering tools (panel or dashboard image export).
- `--disable-cloudwatch`: Disable CloudWatch tools.
- `--disable-examples`: Disable query examples tools.
- `--disable-clickhouse`: Disable ClickHouse tools.
- `--disable-runpanelquery`: Disable run panel query tools.
- `--disable-annotations`: Disable annotation tools.
- `--disable-proxied`: Disable proxied tools (tools from external MCP servers).

## Configure tool limits

- `--max-loki-log-limit`: Maximum number of log lines returned per `query_loki_logs` call.

## Run in read-only mode

`--disable-write` prevents write operations to Grafana. Use it with read-only service accounts, safer production assistants, or to avoid accidental changes.

When enabled, the following writes are disabled:

**Dashboard tools**

- `update_dashboard`

**Folder tools**

- `create_folder`

**Incident tools**

- `create_incident`
- `add_activity_to_incident`

**Alerting tools**

- `alerting_manage_rules` (create, update, delete)

**Annotation tools**

- `create_annotation`
- `update_annotation`

**Sift tools**

- `find_error_pattern_logs` (creates investigations)
- `find_slow_requests` (creates investigations)

Read operations (queries, lists, searches) stay available.

## Configure client TLS for Grafana

- `--tls-cert-file`: Client certificate for mTLS to Grafana.
- `--tls-key-file`: Client private key.
- `--tls-ca-file`: CA certificate for verifying Grafana’s server certificate.
- `--tls-skip-verify`: Skip TLS verification (insecure; testing only).

## Configure server TLS for streamable-http

These flags secure the MCP HTTP server (between your MCP client and `mcp-grafana`), not the connection from `mcp-grafana` to Grafana:

- `--server.tls-cert-file`: Server certificate for HTTPS.
- `--server.tls-key-file`: Server private key.

## Print version information

- `--version`: Print the version and exit.

## Next steps

- [Enable and disable tools](../enable-and-disable-tools/)
- [Client TLS (Grafana connection)](../client-tls-grafana-connection/)
- [Server TLS (streamable-http)](../server-tls-streamable-http/)
- [Transports and addresses](../transports-and-addresses/)
