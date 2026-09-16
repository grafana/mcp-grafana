---
title: Anonymous usage statistics
menuTitle: Usage statistics
description: What the Grafana MCP server reports about its own usage, what it never reports, how the report is sent, and how to turn reporting off.
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

The Grafana MCP server can report limited usage statistics about itself to Grafana Labs. The data shows which tools are called, which of those tools return errors, which transports and tool categories are in use, and how the server is configured. This data is used to understand which tools are used most and where tools fail, so we can make the product better.

The statistics describe the *shape* of usage only. Tool arguments, resource names, dashboards, queries, log lines, error messages and credentials are never sent. The flags you set are recorded by **name** only, never by value, and the Grafana instance the server talks to is described only as `cloud` or `self_hosted` — never by URL, hostname, stack slug, organisation name or organisation ID.

Nothing an MCP client reports about itself is collected: not its name, not its version. Any other value that comes from outside the binary — a proxied tool's name, the authentication method — is first reduced to a fixed vocabulary compiled into the binary, so only known, non-identifying values can ever be sent. A value outside its vocabulary is sent as `other`, never verbatim.

{{< admonition type="note" >}}
Usage statistics reporting is **currently disabled by default**. This will soon change to enabled by default. There are details on how to opt out in [Opt out](#opt-out).
{{< /admonition >}}

## What a report describes

**One report describes one server process, not one user and not one conversation.** Every four hours, and once more as the process exits, the server sends a single event covering everything it has served since its last report.

The unit is the process rather than the MCP session deliberately. MCP protocol version `2026-07-28` removed protocol-level sessions, so for a client using it there is no session for the server to report on at all. Counting per process means the numbers mean the same thing for every client and every transport, and they do not shift as clients upgrade to that version.

## Identifiers

There is one identifier, `process_id`: a random UUID generated in memory when the server process starts.

Nothing is written to disk and nothing survives a restart. There is no device ID, no installation ID, no user ID and no account ID.

This has a consequence worth being explicit about: **install counts are not possible.** The server can be counted in processes, and in nothing else. A restart produces a new `process_id`, and a server that is restarted nightly is indistinguishable from a new install every day. Growth in event volume is therefore ambiguous between more people running the server, the same people restarting it more often, and the same people running more replicas.

## Understand which data is collected

Every event carries the following envelope:

| Field | Description | Example |
| :---- | :---- | :---- |
| `service` | Always `mcp-grafana`, identifying the reporting product. | `mcp-grafana` |
| `version` | The version of the `mcp-grafana` binary. | `1.4.2` |
| `os` | Operating system. | `linux`, `darwin`, `windows` |
| `arch` | CPU architecture. | `amd64`, `arm64` |
| `process_id` | The random per-process ID described in [Identifiers](#identifiers). | UUID |
| `report_reason` | Why this event was sent: `interval` or `shutdown`. | `interval` |
| `process_uptime_ms` | How long the process had been running when the event was built, in milliseconds. Cumulative, unlike the counters. | `1234` |
| `report_seq` | This process's reports numbered from 1. A gap in the sequence for a `process_id` is a report that was built and never arrived. | `3` |

### Tool usage fields

| Field | Description | Example |
| :---- | :---- | :---- |
| `tools_called` | The sorted, comma-joined names of the tools called since the previous event. Absent from the event when no tool was called in that window. | `list_datasources,query_prometheus` |
| `tool_calls` | A nested object keyed by tool name, each holding `calls` and `errors`. Absent from the event when no tool was called in that window, rather than sent as an empty object. | `{"query_prometheus":{"calls":4,"errors":1}}` |
| `calls` | Within `tool_calls`: how many times that tool was called since the previous event, across the whole process. An exact number. | `4` |
| `errors` | Within `tool_calls`: how many of those calls failed. An exact number. | `1` |

Only the tool's name is recorded. Its arguments, its result, the size of its result and the error it returned are all absent — there is no result-size, result-count or cardinality field on the wire at all, in any form, bucketed or otherwise.

### Grafana target fields

| Field | Description | Example |
| :---- | :---- | :---- |
| `grafana_version` | The version the Grafana instance reported, read from a response the server had already fetched for its own reasons, and truncated to 64 bytes. Absent whenever that version is not already known — nothing is fetched to find it out — and absent when the process resolved more than one. | `12.1.0` |
| `target_kind` | `cloud` or `self_hosted`. Deliberately coarse: never the URL, hostname, stack slug, organisation name or organisation ID. Absent when no Grafana target was resolved. | `cloud` |
| `auth_method` | The credential category the connection resolved, from a fixed vocabulary, never the credential itself. Absent when the process resolved more than one. | `service_account_token` |

The `auth_method` vocabulary is exactly `on_behalf_of`, `access_token`, `service_account_token`, `basic_auth` and `anonymous`, plus the `other` sentinel.

### Server configuration fields

| Field | Description | Example |
| :---- | :---- | :---- |
| `transport` | The transport the server is running: `stdio`, `sse` or `streamable-http`. | `stdio` |
| `flags` | The **names** of the command-line flags that were set, sorted. No flag value is sent in this field. | `disable-write,transport` |
| `env_set` | The **names** of the configuration environment variables that are set, sorted. Names only, never values, and only names from a fixed inventory compiled into the binary — the server does not scan its environment, so a variable outside that inventory cannot be reported. | `GRAFANA_SERVICE_ACCOUNT_TOKEN,GRAFANA_URL` |
| `enabled_tools` | The tool category names that are actually active, sorted. | `alerting,dashboard,search` |
| `disabled_tools` | The tool category names a `--disable-<category>` flag turned off, sorted. | `oncall` |
| `tls_enabled` | Whether the server is actually serving HTTPS, which only the `streamable-http` transport does. Whether, not which certificate: no path or certificate detail is sent. | `false` |
| `metrics_enabled` | Whether the Prometheus `/metrics` endpoint is actually served. Always `false` under `stdio`, which mounts no HTTP routes, even with `--metrics` set. | `true` |
| `dynamic_multi_org` | Whether per-call organisation selection is enabled. | `false` |
| `proxied_enabled` | Whether proxied tools from external MCP servers are enabled. | `true` |

## How to read these fields

These fields are easy to misread, so the following constraints are part of the contract:

- **Totals are a floor, not a count.** `calls` and `errors` are deltas since the previous event, and they are reset at the moment an event is *sent*, not when the receiver acknowledges it. A report that never arrives takes its delta with it. Any total built by summing these counts undercounts by an unknown amount, and it undercounts most in exactly the environments where delivery fails most.
- **Nothing is per user, per conversation or per client.** One process can serve many clients and many people at once, and all of their activity is summed into one set of counters. There is no way to attribute a tool call to a particular client, session or person, and no field says which clients connected — by construction, not by omission.
- **`tools_called` and `tool_calls` always agree, and both are per-event.** `tools_called` is the sorted key list of `tool_calls` for that same event, not a running list for the process. A process that called one tool in its first four hours and a different one in its second reports one name in each event, never both in either.
- **A field with nothing to say is absent, not empty.** `tools_called` and `tool_calls` for a window with no activity, and the Grafana fields when nothing resolved them, are left out of the event entirely, so they read as null rather than as an empty string or an empty object. An idle process still reports its envelope, so it remains countable.
- **`grafana_version` and `auth_method` are omitted when the process resolved more than one value.** A multi-tenant HTTP process can serve several Grafana versions or authenticate several ways. Rather than invent a "mixed" value that would sit in the same column as real ones and be counted as one, the field is dropped: absence means "not resolved" *or* "no single value", and those two cases are not distinguishable. Do not read the share of events carrying a value as a measure of anything.
- **`enabled_tools` and `disabled_tools` are not complements, and a category can be in neither.** `enabled_tools` lists the categories that actually register a tool, so a category selected by `--enabled-tools` but emptied by `--disable-write` or `--disable-query` appears in neither list: it was not turned off by a `--disable-<category>` flag, and it registers nothing. `--enabled-tools=assistant --disable-write` reports no categories at all.

- **`errors` counts calls that failed, not why they failed.** A tool that returns an error result and a tool whose handler fails outright both count as one error. Nothing describes the failure: no message, no status, no error category. You can see which tools are failing and never why. A coarse error-kind field may be added later; until then, absence of detail is by design, not an omission.
- **A failure that named no tool is not counted at all.** A `tools/call` whose payload could not be parsed, or that arrived when the tools capability was unavailable, has no tool to attribute, so it is left out of `tool_calls` entirely rather than attributed to anything. `calls` is therefore a count of calls that reached a named tool, not of everything a client attempted.
- **`proxied` is one key covering every proxied tool.** Tools proxied from an external MCP server are all recorded under the single name `proxied`, never by their real names and never by the datasource type they came from. Those names are chosen by a remote server, so this repository cannot bound them, and the datasource type would tell us which backends you run. A call to a tool this build does not have registered — a typo from the model, or a tool whose category is disabled — also lands on `proxied`, so a small `proxied` count is not by itself evidence that proxied tools were used.
- **`target_kind` misclassifies Cloud instances behind a custom domain.** It is derived from the configured URL's hostname alone, and the only positive signal available is the Grafana Cloud domain. A Grafana Cloud stack reached through your own domain reports `self_hosted`. Read `target_kind` as "looks like Cloud" and "everything else", never as an authoritative split.
- **The Grafana fields stay absent until the server talks to Grafana.** They are read from configuration and from a cache the server's own Grafana requests populate. Reporting never issues a request of its own, so a version that nothing else has fetched is simply absent — including on an instance whose `/api/frontend/settings` is unreachable or forbidden. Absent means "not resolved", never "old" or "self-hosted".
- **`grafana_version` is the instance's own string.** It has no vocabulary to clamp against — forks and internal builds set it freely — so it travels as reported, bounded only by a 64-byte truncation. Treat it as untrusted text, not as a value this server vouches for.
- **`enabled_tools` and `disabled_tools` are not complements.** A category that is neither named in `--enabled-tools` nor explicitly disabled appears in neither list. A category name in `--enabled-tools` that this build does not recognise appears in neither list either, because both are bounded by the categories compiled into the binary.
- **The boolean configuration fields record effective state, while `flags` records what was set.** `tls_enabled` and `metrics_enabled` describe what the running server actually does, so TLS material passed to an `sse` server reports `tls_enabled: false` and `--metrics` under `stdio` reports `metrics_enabled: false`. `enabled_tools` and `disabled_tools` likewise describe the categories the server registered, after the deprecated SQL dialect aliases have been resolved, so `clickhouse` never appears and `--enabled-tools=clickhouse` reports `sql` as enabled.
- **Read `flags` and `env_set` together.** `flags` comes from the parsed command line and `env_set` from a fixed inventory of environment variable names, and a setting can be supplied either way. Container and Kubernetes deployments configure almost entirely by environment variable, so `flags` alone is close to empty for them however the server is set up. Neither field carries a value, so which mode a setting was given remains unreported — only that it was set. `tls_enabled`, `metrics_enabled`, `dynamic_multi_org` and `proxied_enabled` report resolved state and are independent of both.
- **Grouping by `process_id` reveals deployment concurrency.** Consecutive events sharing a `process_id`, and how many distinct IDs report at once, describe how many replicas an operator runs and for how long. It says nothing about sessions, which are no longer reported.

## What is never reported

The following never appear in an event, in any field:

- The Grafana URL, hostname, stack slug, organisation ID or organisation name.
- The value of `--server-name`, `--instructions-append`, `--address`, `--base-path` or `--endpoint-path`.
- Anything from `GRAFANA_EXTRA_HEADERS` or `GRAFANA_FORWARD_HEADERS`, including header names.
- Service account tokens, access tokens, ID tokens, passwords or any other credential.
- Tool arguments, tool results, dashboard or datasource names and UIDs, queries, log lines, metric names.
- Error messages, stack traces or HTTP response bodies.
- The names of tools proxied from an external MCP server, or the datasource types they were discovered on.
- Any result-size, result-count or cardinality measurement. There is no such field, bucketed or otherwise.
- The name or version an MCP client reports for itself, and any per-session, per-client or per-user breakdown.
- Any MCP session identifier.
- The Loki query cost guardrail mode, or any other value of a setting supplied through an environment variable rather than a flag.
- Your IP address. See [Server-side enrichment](#server-side-enrichment) for what the receiving service derives from the connection.

## How the report is sent

A process sends one event every four hours, plus one as it exits. The server does not batch events, does not queue them for a later process, and does not start a background process that outlives it.

| Property | Value |
| --- | --- |
| Destination | `https://stats.grafana.org/mcp-grafana-usage-report` |
| Method | A single `POST` with the event as a JSON object |
| Attempts | One. A failed report is never retried and never stored. |
| Interval | Every four hours, with up to ±10% random jitter applied to the first interval so that servers started together do not report in lockstep |
| Timing | In the background for interval reports; synchronously during shutdown |
| Time limit | Five seconds for an interval report; one second for the whole shutdown flush, including the time spent building the event rather than only sending it |

Failures are logged at debug level and nowhere else. Nothing is ever written to standard output, because under the `stdio` transport standard output is the MCP protocol channel and anything written there would corrupt the conversation with your client.

Two consequences of this design:

- **`SIGKILL` loses the final report.** The shutdown flush runs on `SIGTERM` and `SIGINT`. A process killed outright, or one that dies with the machine, never builds its last event, so up to four hours of counts are lost. Short-lived and frequently-killed deployments therefore under-report more than long-running ones.
- **Blocking the destination in a firewall costs more than opting out.** A network that silently drops packets rather than refusing them makes each report wait out its time limit. Opt out instead: an opted-out server builds no event and opens no connection.

To send reports somewhere else, set `GRAFANA_USAGE_STATS_ENDPOINT` to another URL. This changes the destination only. It is not an opt-out, and the value is used as given.

## Server-side enrichment

Reports are received by Grafana's usage-stats service, the same service that receives usage reports from Grafana, Loki, Mimir and Tempo. On receipt, the service adds two pieces of information derived from the connection rather than from the event:

- A coarse **geographic region**, for example a country or subdivision, taken from headers added by the CDN edge.
- The **network organisation name** from a whois lookup of the connecting IP address, which typically resolves to your ISP, your cloud provider or your employer's network.

The connecting IP address itself is not stored in the usage event.

## Inspect what would be sent

To see exactly what the server would report, set the mode to `log`. Each event is printed to stderr and nothing is sent:

```shell
GRAFANA_USAGE_STATS=log mcp-grafana
```

The same thing as a flag, which is easier to put in an MCP client's configuration:

```shell
mcp-grafana --usage-stats=log
```

Under the `stdio` transport, stderr goes wherever your MCP client sends the server's logs, so read the printed events there rather than in a terminal.

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

3. **`DO_NOT_TRACK` environment variable**: set it to `1` or `true` to disable reporting, following the cross-tool [DO_NOT_TRACK](https://donottrack.sh/) convention.

```shell
export DO_NOT_TRACK=1
```

Any value of `--usage-stats` or `GRAFANA_USAGE_STATS` that is not one of those three disables reporting, so a typo fails toward privacy rather than toward collection. Opting out disables reporting entirely: no event is constructed and no connection is opened.

Two things about `DO_NOT_TRACK` are worth stating, because it behaves differently from the other two:

- **It can only ever disable.** `DO_NOT_TRACK=0` and `DO_NOT_TRACK=false` do not turn reporting on; they leave the decision to the settings above. Only `1` and `true` opt out, because the convention gives no meaning to any other value, and treating every non-empty value as an opt-out would make `DO_NOT_TRACK=0` disable reporting.
- **The two explicit settings override it.** It is a machine-wide preference, so a host that sets it can still opt one server back in with `--usage-stats=enabled` or `GRAFANA_USAGE_STATS=enabled`. If you want it to be final, do not also set those.

There is no configuration file. Refer to [Command-line flags](../configure/command-line-flags/) for where `--usage-stats` sits among the other flags.

## The startup notice, and who sees it

When reporting is on, the server logs one line at startup that says usage statistics are being collected, how to turn them off, and where this page is. It is logged once per process start. Nothing is persisted, so there is no notice revision to track and no file to delete: every start logs it again.

The limit worth stating plainly is that **under the `stdio` transport nobody using the server sees that line.** The server's stderr is a log file belonging to the MCP client, and the person typing into the assistant is unlikely ever to read it. Reporting is a decision made by whoever configured the MCP client, and it is that person's responsibility to tell the people whose usage is being reported. If you run the server on behalf of other people and cannot tell them, opt out.

For the HTTP transports the same line goes to the server's own logs, where an operator sees it alongside every other startup message.
