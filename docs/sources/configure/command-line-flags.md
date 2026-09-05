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
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/configure/command-line-flags/
---

# Command-line flags

The `mcp-grafana` binary accepts flags for transports, tools, TLS, and observability. Run `mcp-grafana --help` for the exact list in your installed build.

## What you'll achieve

You can look up defaults, choose `--disable-*` flags, or configure TLS without reading the source.

## Before you begin

- You need a way to run `mcp-grafana` on your machine—for example, a [release binary](../../set-up/install-the-binary/), [`uvx`](../../set-up/install-with-uvx/), or a [container](../../set-up/install-with-docker/).

## Configure transport and HTTP options

- `-t` / `--transport`: Transport type (`stdio`, `sse`, or `streamable-http`). Default: `stdio`.
- `--address`: Host and port for the SSE or streamable-http server. Default: `localhost:8000`.
- `--base-path`: Base path for the SSE or streamable-http server.
- `--endpoint-path`: HTTP path for the streamable-http MCP endpoint. Default: `/mcp`.
- `--session-idle-timeout-minutes`: Idle timeout for streamable-http sessions, in minutes. Sessions with no activity for this duration are automatically reaped. Set to `0` to disable. Default: `30`.

## Configure HTTP transport security

The SSE and streamable-http transports validate `Host` and `Origin` headers on every route on the MCP listener (`/sse`, `/mcp`, and `/healthz` / `/metrics` when they stay on that listener) to block DNS-rebinding attacks. Stdio transport is unaffected. Side listeners started by `--healthz-address` or `--metrics-address` are not wrapped.

- `--allowed-hosts`: Comma-separated allowlist of `Host` header values. When unset (or when the parsed value is empty — for example, `,,,`), it falls back to loopback variants of `--address` (for example, `localhost:8000`, `127.0.0.1:8000`, `[::1]:8000`). Pass `*` to disable the check — only safe behind a trusted reverse proxy that rewrites `Host`.
- `--allowed-origins`: Comma-separated allowlist of `Origin` header values. Empty by default — any request that carries an `Origin` header is rejected (browsers always send `Origin` for cross-origin requests, and no browser should be calling this server directly). Pass an explicit list to permit browser clients, or `*` to disable the check.

When deploying behind an ingress or reverse proxy that forwards the original `Host`, set `--allowed-hosts` to the expected hostname (or `*` if the proxy is fully trusted). Kubernetes `httpGet` liveness/readiness probes send `Host: <pod-ip>:<port>` by default — either set `--allowed-hosts '*'`, override the probe's `host:` field, use a `tcpSocket` probe, or bind `/healthz` on `--healthz-address` (and `/metrics` on `--metrics-address`). Those side listeners are not wrapped by Host/Origin validation.

## Configure caller authentication

The SSE and streamable-http transports can authenticate the **caller** (the MCP client connecting to `mcp-grafana`). This is separate from the credentials the server uses to reach Grafana: it controls _who may invoke the server_, so an unauthenticated client can't borrow the server's Grafana identity or run tools. Stdio is a local pipe and is never affected.

- `--server-auth-token`: Bearer token that callers must present in the `Authorization: Bearer <token>` header. Falls back to the `MCP_GRAFANA_SERVER_TOKEN` environment variable. When set, requests without a valid token are rejected with `401` before any tool runs. Prefer the environment variable over the flag so the secret doesn't appear in the process argument list.

### Bind policy

Caller authentication is enforced only when `--server-auth-token` is set. When it isn't, the network transports warn (but still start) on an externally reachable address:

| Transport / bind | Behavior |
| --- | --- |
| `stdio` | No caller authentication (local pipe). |
| SSE / streamable-http on a loopback address (`localhost`, `127.0.0.1`, `::1`) | Caller token optional. |
| SSE / streamable-http on any other address | Starts and logs a **security error** (at the `error` log level, so it isn't suppressed by `--log-level`) unless `--server-auth-token` is set. |

{{< admonition type="note" >}}
The permissive default preserves backward compatibility for existing deployments (such as the container's `0.0.0.0` bind). A future major release will make an unauthenticated non-loopback bind a startup error. Set `--server-auth-token` now to require caller authentication and prepare for that change.
{{< /admonition >}}

Bearer authentication only protects the token in transit if the connection is encrypted. Terminate TLS in front of the server, or use [server TLS for streamable-http](../server-tls-streamable-http/), whenever you set `--server-auth-token` on a non-loopback address.

When caller authentication is enabled, the `Authorization` header is reserved for the caller token and is stripped after validation, so it is never forwarded to Grafana. Setting `--server-auth-token` together with `GRAFANA_FORWARD_HEADERS=Authorization` is contradictory and the server refuses to start; remove `Authorization` from `GRAFANA_FORWARD_HEADERS`, or unset the caller token to run in proxy-forwarding mode.

## Configure debug and logging

- `--debug`: Enable debug mode for detailed HTTP request and response logging to and from the Grafana API.
- `--log-level`: Log level (`debug`, `info`, `warn`, `error`). Default: `info`.

## Configure observability endpoints

- `--metrics`: Expose a Prometheus metrics endpoint at `/metrics` (SSE and streamable-http only).
- `--metrics-address`: Optional separate listen address for metrics (for example, `:9090`). If empty, metrics are served on the main HTTP server.
- `--healthz-address`: Optional separate listen address for `/healthz` (for example, `:8080`). If empty, `/healthz` is served on the main HTTP server. If this matches `--metrics-address`, both routes share one extra listener. The side listener is not wrapped by Host/Origin validation, so Kubernetes probes can reach it while `--address` stays on loopback.

## Configure tool categories

- `--enabled-tools`: Comma-separated list of enabled tool **categories**. The default is exactly:

  `search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,pyroscope,navigation,proxied,annotations,rendering,snapshot,docs`

  Categories **not** in that default string are off until you add them, including: `admin`, `agento11y`, `assistant`, `elasticsearch`, `cloudwatch`, `examples`, `clickhouse`, `snowflake`, `influxdb`, `quickwit`, and `runpanelquery`. Pass a full comma-separated list to replace the default entirely, or use `--disable-*` flags to turn off pieces of the default set.

- `--disable-search`: Disable search tools.
- `--disable-datasource`: Disable datasource tools.
- `--disable-incident`: Disable incident tools.
- `--disable-prometheus`: Disable Prometheus tools.
- `--disable-write`: Disable write tools (read-only mode; refer to the following section).
- `--disable-query`: Disable query tools (tools that execute a query against a datasource; refer to the following section).
- `--enable-query`: Keep the raw-SQL query tools registered under `--disable-write` (refer to the following section).
- `--disable-loki`: Disable Loki tools.
- `--disable-elasticsearch`: Disable Elasticsearch tools.
- `--disable-quickwit`: Disable Quickwit tools.
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
- `--disable-snapshot`: Disable snapshot tools.
- `--disable-cloudwatch`: Disable CloudWatch tools.
- `--disable-examples`: Disable query examples tools.
- `--disable-clickhouse`: Disable ClickHouse tools.
- `--disable-snowflake`: Disable Snowflake tools.
- `--disable-runpanelquery`: Disable run panel query tools.
- `--disable-annotations`: Disable annotation tools.
- `--disable-proxied`: Disable proxied tools (tools from external MCP servers).
- `--disable-provisioning`: Disable provisioning tools.
- `--disable-agento11y`: Disable Agent Observability tools.
- `--disable-assistant`: Disable Grafana Assistant tools.
- `--disable-docs`: Disable documentation tools.
- `--disable-user`: Disable user info tools.

## Configure tool limits

- `--max-loki-log-limit`: Maximum number of log lines returned per `query_loki_logs` call.
- `--loki-guardrail-mode`: Loki query cost guardrail for `query_loki_logs`: `off` (default), `shadow` (log queries that would be blocked, but let them run), or `enforce` (reject them with rewrite guidance). The guardrail requires a selective stream selector, caps the effective time range (including range-vector durations like `[30d]`), and pre-checks Loki's index/stats byte estimate before running the query. On VictoriaLogs it applies only to selector-shaped (`{...}`) queries — brace-less LogsQL passes through entirely and the byte-budget check never applies. Falls back to the `GRAFANA_LOKI_GUARDRAIL_MODE` environment variable.
- `--loki-guardrail-max-bytes`: Maximum bytes a single `query_loki_logs` call may scan, estimated via Loki's index/stats API. Defaults to 100 GiB; `0` disables the byte-budget check. Falls back to `GRAFANA_LOKI_GUARDRAIL_MAX_BYTES`.
- `--loki-guardrail-max-range`: Maximum effective time range for a single `query_loki_logs` call, including range-vector durations. Defaults to `24h`; `0` disables the range check. Falls back to `GRAFANA_LOKI_GUARDRAIL_MAX_RANGE`.

The guardrail's decisions are also exported as OTel counters (`mcp_loki_guardrail_admitted_total`, `_would_block_total`, `_blocked_total`, `_fail_open_total`), which is the recommended way to size the affected population before promoting from `shadow` to `enforce`. See [Observability](../../developer/observability-metrics-and-tracing/#loki-cost-guardrail-metrics).
- `--dynamic-multi-org`: Allow tool calls to select a Grafana organization per call via an optional `orgId` argument. Off by default. See [Multi-organization support](../multi-organization-and-headers/).

## Run without query execution

`--disable-query` removes every tool that executes a query against a datasource, and leaves the metadata and discovery tools in place.

Use it when the assistant should be able to explore what exists — datasources, dashboards, metric names, labels, table schemas — without running potentially expensive or data-revealing queries; for example, with a service account that has `datasources:read` but not `datasources:query`.

The following tools are not registered when the flag is set:

- Prometheus: `query_prometheus`, `query_prometheus_histogram`
- Loki: `query_loki_logs`, `query_loki_patterns` (`query_loki_stats` and `analyze_loki_labels` read the index rather than returning log content, so they stay registered)
- Elasticsearch and OpenSearch, Quickwit: `query_elasticsearch`, `query_quickwit`
- SQL datasources: `query_clickhouse`, `query_snowflake`, `query_athena`, `query_influxdb`
- Graphite: `query_graphite`, `query_graphite_density`
- CloudWatch: `query_cloudwatch`
- Pyroscope: `query_pyroscope`
- Panels: `run_panel_query`

The `elasticsearch`, `quickwit`, `influxdb`, and `runpanelquery` categories contain nothing else, so they expose no tools at all when queries are disabled. Sibling tools such as `list_prometheus_metric_names`, `list_loki_label_values`, `describe_clickhouse_table`, and `list_cloudwatch_metrics` remain available.

The flag gates the query tools and the `grafana_api_request` POST-to-`/api/ds/query` path, but doesn't police every route to a datasource. In read-only mode, `grafana_api_request` allows POST to `/api/ds/query` only when query tools are enabled (same gate as the raw-SQL tools — blocked by `--disable-write` unless `--enable-query` overrides). `get_panel_image`, which renders a panel server-side, is unaffected.

### Query execution and read-only mode

The raw-SQL query tools — `query_clickhouse`, `query_snowflake`, `query_athena`, and `query_influxdb` — send the query to the datasource unfiltered, so they write whenever the datasource credentials permit it. Read-only mode removes them along with the other write tools.

`--enable-query` puts them back. Use it when the datasource credentials are known to be read-only and you want query execution in an otherwise read-only server. It doesn't re-enable any other write tool, and it has no effect alongside `--disable-query`, which always wins.

| Flags | Safe query tools | Raw-SQL query tools |
| --- | --- | --- |
| _(none)_ | Registered | Registered |
| `--disable-write` | Registered | Not registered |
| `--disable-write --enable-query` | Registered | Registered |
| `--disable-query` | Not registered | Not registered |
| `--disable-query --enable-query` | Not registered | Not registered |

## Run in read-only mode

`--disable-write` prevents write operations to Grafana. Use it with read-only service accounts, safer production assistants, or to avoid accidental changes. It also removes the raw-SQL query tools, which can write through the datasource; refer to the preceding section for `--enable-query`, which keeps them.

When enabled, the following writes are disabled:

**Dashboard tools**

- `update_dashboard`

**Folder tools**

- `create_folder`

**Incident tools**

- `create_incident`
- `add_activity_to_incident`
- `update_incident`

**Alerting tools**

- `alerting_manage_rules` (create, update, delete)

**OnCall tools**

- `update_alert_group`

**Annotation tools**

- `create_annotation`
- `update_annotation`
- `delete_annotation`

**Sift tools**

- `find_error_pattern_logs` (creates investigations)
- `find_slow_requests` (creates investigations)

**Snapshot tools**

- `create_snapshot`
- `delete_snapshot`

**Agent Observability tools**

- `agento11y_manage_evaluators` (upsert, delete, fork, and test evaluators)
- `agento11y_manage_eval_rules` (create, update, delete, and preview eval rules and guards)
- `agento11y_manage_eval_collections` (save and delete saved conversations; create, update, and delete collections; add and remove collection members)
- `agento11y_manage_experiments` (update and cancel experiments)
- `agento11y_manage_test_suites` (create and update test suites; create and publish versions; upsert and delete test cases)

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
