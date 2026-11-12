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
- `--base-path`: Base path for the SSE or streamable-http server. With `--base-path /my-base`, SSE is at `/my-base/sse` and streamable-http at `/my-base/mcp`. `/healthz` and `/metrics` are internal-only endpoints for probes and scrapers and always stay at the server root, never under this prefix.
- `--endpoint-path`: HTTP path for the streamable-http MCP endpoint, appended to `--base-path`. Default: `/mcp`.
- `--sse-keepalive`: Interval between ping requests the server sends to each SSE client, so a reverse proxy or load balancer doesn't close an idle event stream and end the session. Set it below the proxy's idle timeout, for example, `30s` behind ingress-nginx's default 60-second `proxy-read-timeout`. A client is disconnected after 3 pings in a row go unanswered for half the interval each. Accepts Go duration strings, minimum `1s`. Default: `0` (disabled). Applies only to `--transport sse`.
- `--instructions-append`: Text appended to the server instructions returned to MCP clients on initialize, so every connecting agent sees it.

When `--address` uses port `0`, the operating system allocates a free port and the server atomically writes the bound address to `mcp-grafana-address` in its working directory. The file contains the address followed by a newline and is created with mode `0600`.

## Configure HTTP transport security

The SSE and streamable-http transports validate `Host` and `Origin` headers on every route on the MCP listener (`/sse`, `/mcp`, and `/healthz` / `/metrics` when they stay on that listener) to block DNS-rebinding attacks. Stdio transport is unaffected. Side listeners started by `--healthz-address` or `--metrics-address` are not wrapped.

- `--allowed-hosts`: Comma-separated allowlist of `Host` header values. When unset (or when the parsed value is empty — for example, `,,,`), it falls back to loopback variants of `--address` (for example, `localhost:8000`, `127.0.0.1:8000`, `[::1]:8000`). Pass `*` to disable `Host` validation — only safe when a trusted reverse proxy validates `Host`.
- `--allowed-origins`: Comma-separated allowlist of `Origin` header values. Empty by default — any request that carries an `Origin` header is rejected (browsers always send `Origin` for cross-origin requests, and no browser should be calling this server directly). Pass an explicit list to permit browser clients, or `*` to disable the check.

When deploying behind an ingress or reverse proxy that forwards the original `Host`, set `--allowed-hosts` to the expected hostname (for example, `--allowed-hosts mcp.example.com`). Kubernetes `httpGet` liveness/readiness probes send `Host: <pod-ip>:<port>` by default — either set `--allowed-hosts '*'`, override the probe's `host:` field, use a `tcpSocket` probe, or bind `/healthz` on `--healthz-address` (and `/metrics` on `--metrics-address`). Those side listeners are not wrapped by Host/Origin validation.

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

## Select a Grafana URL per request

{{< admonition type="warning" >}}
URL overrides let MCP callers select outbound HTTP(S) destinations. An allowlist limits URLs but does not authenticate callers, bind tokens to targets, or enforce network egress policy.

Deploy behind an authenticating proxy that authorizes each caller's target, removes client-supplied `X-Grafana-URL` and Grafana token headers, and supplies the approved URL with its matching token. Restrict outbound access to approved destinations with a network policy, firewall, or egress proxy. These controls matter even with an allowlist.

Without a URL allowlist, a fake request token can cause requests to any reachable HTTP(S) service, including internal and metadata services.
{{< /admonition >}}

On the streamable-http transport, callers can select a Grafana instance for each request. This is disabled by default.

- `--allow-grafana-url-override`: Enable selection through `X-Grafana-URL`. Falls back to `GRAFANA_ALLOW_URL_OVERRIDE` when the flag is not set.
- `--allowed-grafana-urls`: Optional comma-separated list of exact Grafana base URLs that callers may select. Falls back to `GRAFANA_ALLOWED_URLS` when the flag is not set. It requires the enable switch; an explicitly empty flag clears an inherited list.

For a large fleet selected by a proxy, `GRAFANA_ALLOW_URL_OVERRIDE=true` enables selection without listing every instance in `GRAFANA_ALLOWED_URLS`. The deployment controls in the warning above still apply. The proxy must send both `X-Grafana-URL: <target base URL>` and `X-Grafana-Service-Account-Token: <token for that target>` on each MCP request. The deprecated `X-Grafana-API-Key` header also works. The server uses the token from that request and does not send its environment Grafana credentials to a selected target. If caller authentication is configured, the `Authorization` header carries the separate MCP caller token.

Without `--allowed-grafana-urls`, the server logs a security error at startup. Outbound Grafana requests are pinned to the selected base URL, including across redirects. Selection does not work over SSE, which does not pass per-request headers to tool calls; a selected URL there is used without the caller's token, so the calls fail.

## Configure debug and logging

- `--debug`: Enable debug mode for detailed HTTP request and response logging to and from the Grafana API.
- `--log-level`: Log level (`debug`, `info`, `warn`, `error`). Default: `info`.

## Configure observability endpoints

- `--metrics`: Expose a Prometheus metrics endpoint at `/metrics` (SSE and streamable-http only).
- `--metrics-address`: Optional separate listen address for metrics (for example, `:9090`). If empty, metrics are served on the main HTTP server.
- `--healthz-address`: Optional separate listen address for `/healthz` (for example, `:8080`). If empty, `/healthz` is served on the main HTTP server. If this matches `--metrics-address`, both routes share one extra listener. The side listener is not wrapped by Host/Origin validation, so Kubernetes probes can reach it while `--address` stays on loopback.

## Configure anonymous usage statistics

- `--usage-stats`: Anonymous usage statistics reporting: `enabled`, `disabled`, or `log` to print the report that would be sent to stderr and send nothing. Overrides the `GRAFANA_USAGE_STATS` environment variable. Default: `enabled`.

`GRAFANA_USAGE_STATS_ENDPOINT` changes where reports are sent.

Refer to [Anonymous usage statistics](../../anonymous-usage-statistics/) for the full list of fields, what is never sent, and how to read the data.

## Configure tool categories

- `--enabled-tools`: Comma-separated list of enabled tool **categories**. The default is exactly:

  `search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,pyroscope,navigation,tempo,annotations,rendering,snapshot,docs`

  Categories **not** in that default string are off until you add them, including: `admin`, `agento11y`, `assistant`, `elasticsearch`, `cloudwatch`, `cloudlogging`, `examples`, `sql`, `influxdb`, `quickwit`, `runpanelquery`, and `dsquery`. Pass a full comma-separated list to replace the default entirely, or use `--disable-*` flags to turn off pieces of the default set. Back-compat aliases `clickhouse`, `snowflake`, and `athena` map to `sql`; `proxied` maps to `tempo`.

- `--disable-search`: Disable search tools.
- `--disable-datasource`: Disable datasource tools.
- `--disable-incident`: Disable incident tools.
- `--disable-prometheus`: Disable Prometheus tools.
- `--disable-write`: Disable write tools (read-only mode; refer to the following section).
- `--disable-query`: Disable query tools (tools that execute a query against a datasource; refer to the following section).
- `--enable-query`: Keep the raw-SQL query tools, and `query_datasource` (a source of `render_grafana_app`), available under `--disable-write` (refer to the following section).
- `--enable-write-tools`: Comma-separated list of individual tool names to keep registered under `--disable-write`, for example, `query_sql`, `query_influxdb`, `generate_deeplink`, or the alerting write tools `alerting_rules_write`, `alerting_routing_write`, and `alerting_silences_write`. Has no effect on a tool whose whole category is disabled.
- `--disable-loki`: Disable Loki tools.
- `--disable-elasticsearch`: Disable Elasticsearch tools.
- `--disable-quickwit`: Disable Quickwit tools.
- `--disable-influxdb`: Disable InfluxDB tools.
- `--disable-alerting`: Disable alerting tools.
- `--disable-dashboard`: Disable dashboard tools.
- `--disable-folder`: Disable folder tools.
- `--disable-oncall`: Disable OnCall tools.
- `--disable-asserts`: Disable Asserts tools.
- `--disable-admin`: Disable admin tools.
- `--disable-pyroscope`: Disable Pyroscope tools.
- `--disable-navigation`: Disable navigation (deeplink) tools.
- `--disable-rendering`: Disable rendering tools (panel or dashboard image export).
- `--disable-snapshot`: Disable snapshot tools.
- `--disable-cloudwatch`: Disable CloudWatch tools.
- `--disable-cloudlogging`: Disable Google Cloud Logging tools.
- `--disable-examples`: Disable query examples tools.
- `--disable-sql`: Disable SQL datasource tools (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL, BigQuery). Aliases `--disable-clickhouse`, `--disable-snowflake`, `--disable-athena` also work.
- `--disable-runpanelquery`: Disable run panel query tools.
- `--disable-dsquery`: Disable the `query_datasource` source of `render_grafana_app`.
- `--disable-annotations`: Disable annotation tools.
- `--disable-tempo`: Disable Tempo tracing tools.
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

- `--prometheus-guardrail-mode`: Prometheus query guardrail for `query_prometheus`, `query_prometheus_histogram`, and the Prometheus queries of `run_panel_query`: `off` (default), `shadow` (log queries that would be blocked, but let them run), or `enforce` (reject them with rewrite guidance). Falls back to the `GRAFANA_PROMETHEUS_GUARDRAIL_MODE` environment variable.
- `--prometheus-guardrail-require-selective-labels`: Require each vector selector to include a non-empty positive label constraint beyond the metric name. Default: `false`; independent of the range and points budgets, including when both are `0`. Only applies when the guardrail mode is not `off`. Env fallback: `GRAFANA_PROMETHEUS_GUARDRAIL_REQUIRE_SELECTIVE_LABELS`. Explicit CLI values, including `false`, override the environment.
- `--prometheus-guardrail-max-range`: Maximum effective time range for a single PromQL query: its own time range (zero for instant queries) plus the longest range-vector or subquery window in the expression, with nested subquery ranges added up. Defaults to `192h`. Takes a Go duration, so write `192h`, not `8d`. `0` disables the range check. Falls back to `GRAFANA_PROMETHEUS_GUARDRAIL_MAX_RANGE`.
- `--prometheus-guardrail-max-points`: Maximum points per series a range query may request, `floor(range / step) + 1`. Defaults to `11000`; `0` disables the points check. Falls back to `GRAFANA_PROMETHEUS_GUARDRAIL_MAX_POINTS`.
- `--prometheus-guardrail-max-metric-cardinality`: Opt-in TSDB scope policy. Default: `0` (disabled). An exact metric name reported above this series-count threshold requires a non-empty exact matcher on a configured scope label. Only active when the mode is not `off`. Env fallback: `GRAFANA_PROMETHEUS_GUARDRAIL_MAX_METRIC_CARDINALITY`.
- `--prometheus-guardrail-cardinality-cache-ttl`: Lifetime of cached TSDB snapshots. Default: `10m`; `0` also uses `10m`. Env fallback: `GRAFANA_PROMETHEUS_GUARDRAIL_CARDINALITY_CACHE_TTL`.
- `--prometheus-guardrail-cardinality-scope-labels`: Comma-separated labels whose non-empty exact matchers satisfy the cardinality policy. Default: `job,instance,namespace,pod,service,cluster`; empty uses these defaults. `__name__` is not allowed. Env fallback: `GRAFANA_PROMETHEUS_GUARDRAIL_CARDINALITY_SCOPE_LABELS`. Explicit CLI settings override the corresponding environment variables.

The points check budgets the requested resolution, not the number of series, samples, or subquery evaluations. The range check ignores `offset` and `@`. Grafana macros such as `$__rate_interval` and `$__range` are filled in before the expression is measured. An expression the guardrail still cannot parse, such as one with a dashboard variable like `$window`, is checked against its time range and step only. Decisions are exported as `mcp_prometheus_guardrail_admitted_total`, `_would_block_total`, `_blocked_total`, and `_fail_open_total`. See [Observability](../../developer/observability-metrics-and-tracing/#prometheus-guardrail-metrics).

- `--dashboard-schema`: Set to `v2` to make `update_dashboard` reject a classic v1 (`panels[]`) body that would create a new dashboard when Grafana serves dashboard schema v2. The error carries a minimal v2 body to retry with. Existing v1 dashboards stay editable as v1, and a Grafana that does not serve v2 is unaffected. Off (empty) by default. Falls back to `GRAFANA_DASHBOARD_SCHEMA`.
- `--dynamic-multi-org`: Allow tool calls to select a Grafana organization per call via an optional `orgId` argument. Off by default. See [Multi-organization support](../multi-organization-and-headers/).

#### Prometheus selector policy

Enable `--prometheus-guardrail-require-selective-labels` alongside `--prometheus-guardrail-mode=shadow` to measure the impact before enforcing it. Existing range and points policies are unchanged when this new flag is false (the default).

Each vector selector must have a positive exact or regex matcher on a label other than `__name__` that does not match the empty string and is not recognized as a blanket regex. An exact metric name alone is not sufficient. Matchers on another operand of a binary expression cannot provide scope for this selector.

| Query | Selector policy |
|-------|-----------------|
| `up` or `up{job=~".*"}` | Block: neither constrains a label beyond the metric name |
| `{__name__=~"up\|http_requests_total"}` | Block: metric-name constraints alone are insufficient |
| `up{job=~".+"}` or `up{job!=""}` | Block: an existence check alone is insufficient |
| `up{job!~".+"}` or `up{job=""}` | Block: an absence/empty-value check alone is insufficient |
| `up{job="api",instance=~".*"}` | Allow: a non-empty positive exact constraint provides scope |
| `up{job=~"api\|worker",instance=~".+"}` | Allow: a constrained positive regex provides scope |
| `up{job="api",instance!~".+"}` | Allow: the absence check is scoped by `job` |
| `up{job!~".*"}` | Allow: the negative universal regex proves this selector empty |
| `up{job="api"} or up` | Block: the second selector is unscoped |

The regex recognizer handles common universal and non-empty-universal forms, including outer anchors, capture/non-capture groups, duplicate alternatives, and unbounded dot repeats such as `.{0,}`. It respects Prometheus' full anchoring and dot/newline behavior. Whitespace inside the regex is literal: `".* "` is not equivalent to `".*"`. Negative matchers never count as positive scope; a recognized negative universal matcher exempts only its own empty selector.

This is a static heuristic, not a query-cost or cardinality bound and not an exhaustive regex-equivalence checker. Unrecognized non-empty positive regexes can satisfy the policy. Even `job="api"` can select many series. Blanket regexes do not inherently disable the inverted index: Prometheus can skip `=~".*"` and short-circuit `!~".*"`. Backend limits remain necessary. Removing a wildcard alone does not reduce the selected series; use actual label values that narrow the intended query scope. The guardrail returns guidance and does not rewrite query semantics automatically.

The check applies to instant and range queries, including selectors inside range vectors and subqueries, across all three guarded tool paths. If parsing fails, selector and expression-window checks are unavailable; request time range and points are still checked. If neither blocks, the query fails open with `reason="unparseable"`.

#### Prometheus cardinality policy

Enable `--prometheus-guardrail-mode=shadow --prometheus-guardrail-max-metric-cardinality=100000` to observe the policy before enforcing it. It uses the top 10 metric counts from Prometheus' `/api/v1/status/tsdb`, fetched through Grafana with the request's authentication. This endpoint describes the current TSDB head, not the selected series for the query or all historical data. The check is independent of the static selector, range and points policies.

A selector naming a metric whose reported count exceeds the threshold needs a non-empty **exact** matcher on one of the configured scope labels. The default scope labels are `job`, `instance`, `namespace`, `pod`, `service`, and `cluster`; customize them for your deployment. Each selector is checked independently, including inside functions, range vectors, subqueries and binary operands. Selectors with contradictory metric-name matchers select no series and skip the cardinality check.

For a known `http_requests_total` count of 200,000 and a threshold of 100,000:

| Selector | Cardinality policy |
|----------|--------------------|
| `http_requests_total{status="200"}` | Block: `status` is not a configured scope label |
| `http_requests_total{status="200",namespace="payments"}` | Allow: an exact scope matcher is present |
| `http_requests_total{namespace=~"payments.*"}` | Block: regex scope is not accepted by this policy |
| `http_requests_total{namespace!=""}` | Block: negative and existence matchers are insufficient |

This is a scope heuristic, not a series-count limit: `namespace="payments"` or `job="api"` can still match many series. The guardrail does not claim that the narrowed query is cheap and does not rewrite it automatically. A metric missing from the top 10, or one that cannot be identified by an exact metric-name matcher, is unknown rather than proven safe. Unsupported backends, unavailable metadata and malformed responses fail open for this policy; existing static checks still apply. Cloud Monitoring does not expose this Prometheus endpoint.

Metadata uses a cache of at most 256 entries, isolated by the request's Grafana target and authentication/tenant context. Concurrent refreshes are coalesced, with at most 8 fetches running at once. Failed fetches are cached for the smaller of the configured TTL and 30 seconds. A cache miss while all fetch slots are busy fails open. A later query can retry as soon as a slot is available. Fetches have a 2-second timeout and a 1 MiB response limit. Custom `BaseTransport` and TLS client-certificate configurations bypass the shared cache because their caller identity can change independently. Server-only TLS settings (a custom CA or skipping server-certificate verification) retain caching. A client timeout does not guarantee that the backend stops computing statistics. `off` mode or a zero cardinality threshold adds no TSDB requests. Selectors already carrying an exact scope matcher need no metadata lookup; queries with a static violation also skip metadata, including in shadow mode. Start in `shadow` mode: it records `reason="cardinality"` for would-be blocks, but still executes the query. Enforce mode rejects with guidance to add a real scope matcher. Unknown assessments use `reason="cardinality_unknown"` on the fail-open counter.

## Run without query execution

`--disable-query` removes every tool that executes a query against a datasource, and leaves the metadata and discovery tools in place.

Use it when the assistant should be able to explore what exists — datasources, dashboards, metric names, labels, table schemas — without running potentially expensive or data-revealing queries; for example, with a service account that has `datasources:read` but not `datasources:query`.

The following tools are not registered when the flag is set:

- Prometheus: `query_prometheus`, `query_prometheus_histogram`
- Rendering: `render_grafana_app`
- Loki: `query_loki_logs`, `query_loki_patterns` (`query_loki_stats` and `analyze_loki_labels` read the index rather than returning log content, so they stay registered)
- Elasticsearch and OpenSearch, Quickwit: `query_elasticsearch`, `query_quickwit`
- SQL datasources: `query_sql`, `query_influxdb`
- Graphite: `query_graphite`, `query_graphite_density`
- CloudWatch: `query_cloudwatch`, `query_cloudwatch_logs`
- Google Cloud Logging: `query_cloud_logging`
- Pyroscope: `query_pyroscope`
- Panels: `run_panel_query`

The `elasticsearch`, `quickwit`, `influxdb`, and `runpanelquery` categories contain nothing else, so they expose no tools at all when queries are disabled. Sibling tools such as `list_prometheus_metric_names`, `list_loki_label_values`, `describe_sql_table`, `list_cloudwatch_metrics`, and `list_cloud_logging_projects` remain available.

The flag gates the query tools and the `grafana_api_request` POST-to-`/api/ds/query` path, but doesn't police every route to a datasource. In read-only mode, `grafana_api_request` allows POST to `/api/ds/query` only when query tools are enabled (same gate as the raw-SQL tools — blocked by `--disable-write` unless `--enable-query` overrides). `get_panel_image`, which renders a panel server-side, is unaffected.

### Query execution and read-only mode

The raw-SQL query tools — `query_sql` and `query_influxdb` — send the query to the datasource unfiltered, so they write whenever the datasource credentials permit it. So does `query_datasource`, a source of `render_grafana_app` for the `dsquery` category, which is off by default. Read-only mode removes them along with the other write tools.

`--enable-query` puts them back. For `query_datasource`, `--enable-write-tools=query_datasource` also works. Use it when the datasource credentials are known to be read-only and you want query execution in an otherwise read-only server. It doesn't re-enable any other write tool, and it has no effect alongside `--disable-query`, which always wins.

| Flags | Safe query tools | Raw-SQL query tools |
| --- | --- | --- |
| _(none)_ | Registered | Registered |
| `--disable-write` | Registered | Not registered |
| `--disable-write --enable-query` | Registered | Registered |
| `--disable-query` | Not registered | Not registered |
| `--disable-query --enable-query` | Not registered | Not registered |

## Restrict which Prometheus series can be read

- `--prometheus-enforced-matchers`: PromQL label matchers AND-ed into every native-Prometheus query to restrict which series can be read (e.g. `environment=~"prod|staging"`). Unparseable queries are rejected.

{{< admonition type="warning" >}}
Enforcement applies only to the Prometheus query tools. Other tools can reach Prometheus metric data through paths that never touch the enforced backend, so you must also disable them for the restriction to hold:

- `--disable-api`: `grafana_api_request` can query the Prometheus datasource proxy directly (full bypass).
- `--disable-rendering`: `get_panel_image` can render unrestricted metric data. `render_grafana_app` can also use `query_datasource` to submit Prometheus queries without these matchers. `--disable-dsquery` closes the latter path only.
- `--disable-assistant`: `ask_assistant` delegates to Grafana Assistant, which reads Prometheus server-side across all series. It is only registered when write tools are enabled, so `--disable-write` closes it too.

The server logs a warning at startup naming each of these that is still enabled. `run_panel_query` is safe: it routes on the datasource's real type resolved from its UID, so a Prometheus datasource always runs through the enforced query path even if the panel or the caller declares a different `datasourceType`. Dashboard snapshots (`--disable-snapshot`) can embed panel data captured outside enforcement.
{{< /admonition >}}

## Restrict which Loki streams can be read

- `--loki-enforced-matchers`: LogQL label matchers AND-ed into every native-Loki query to restrict which log streams can be read (e.g. `environment=~"prod|staging"`). Unparseable queries are rejected; VictoriaLogs datasources are refused while set.
- `--loki-label-enumeration-fallback`: What the label-enumeration tools do when negative enforced matchers can't scope them: `reject` (default) or `unfiltered`.

{{< admonition type="warning" >}}
Enforcement applies only to the Loki query tools. Other tools can reach Loki log data through paths that never touch the enforced backend, so you must also disable them for the restriction to hold:

- `--disable-api`: `grafana_api_request` can query the Loki datasource proxy directly (full bypass).
- `--disable-rendering`: `get_panel_image` renders Loki panels server-side, producing images with unrestricted log lines.
- `--disable-assistant`: `ask_assistant` delegates to Grafana Assistant, which reads Loki server-side across all streams. It is only registered when write tools are enabled, so `--disable-write` closes it too.

The server logs a warning at startup naming each of these that is still enabled. `run_panel_query` is safe: it routes on the datasource's real type resolved from its UID, so a Loki datasource always runs through the enforced query path even if the panel or the caller declares a different `datasourceType`. Tempo tools expose only traces, not Loki logs, so they are not a bypass. Dashboard snapshots (`--disable-snapshot`) can embed log-panel data captured outside enforcement.
{{< /admonition >}}

## Run in read-only mode

`--disable-write` prevents write operations to Grafana. Use it with read-only service accounts, safer production assistants, or to avoid accidental changes. It also removes the raw-SQL query tools, which can write through the datasource; refer to the preceding section for `--enable-query`, which keeps them.

In read-only mode, `generate_deeplink` ignores `shorten=true` and returns the full URL, because Grafana creates short URLs through `POST /api/short-urls`. Add `--enable-write-tools=generate_deeplink` to keep URL shortening without re-enabling any other write tool.

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

- `alerting_rules_write` (create, update, delete)
- `alerting_silences_write` (create, update, delete)
- `alerting_routing_write` (create_contact_point)

Each alerting write tool can be kept individually with `--enable-write-tools=<name>`.

**OnCall tools**

- `update_alert_group`

**Annotation tools**

- `create_annotation`
- `update_annotation`
- `delete_annotation`

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

## Configure Grafana HTTP redirects

Outbound Grafana clients block redirects to a different scheme, host, or port by default. Use `--allow-cross-origin-redirects` or set `GRAFANA_ALLOW_CROSS_ORIGIN_REDIRECTS=true` to allow them. The flag takes precedence over the environment variable. Allowing these redirects can send Grafana credentials to the redirect target. Request-selected Grafana URLs remain pinned to their selected target.

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
