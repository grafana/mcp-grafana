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

The Grafana MCP server can report limited usage statistics about itself to Grafana Labs. The data shows which tools MCP clients actually call, which of those tools return errors, which transports and tool categories are in use, and how the server is configured. It exists so the team can tell which tools are worth investing in, which are failing in the field, and which configurations need to keep working.

The statistics describe the *shape* of usage only. Tool arguments, resource names, dashboards, queries, log lines, error messages and credentials are never sent. The flags you set are recorded by **name** only, never by value, and the Grafana instance the server talks to is described only as `cloud` or `self_hosted` — never by URL, hostname, stack slug, organisation name or organisation ID.

Everything derived from a string somebody else chose — an MCP client's reported name, a proxied tool's name, the authentication method — is first reduced to a fixed vocabulary compiled into the binary, so only known, non-identifying values can ever be sent. A value outside its vocabulary is sent as `other`, never verbatim.

{{< admonition type="note" >}}
Usage statistics reporting is **disabled by default in this release**. The receiving endpoint is not live yet. A later release will change the default to enabled, with the same opt-out described in [Opt out](#opt-out). Until then, reporting happens only if you turn it on explicitly.
{{< /admonition >}}

## Identifiers, and what they cannot tell you

There are two identifiers, and both are random UUIDs generated in memory:

- `process_id` is generated when the server process starts.
- `session_id` is generated when an MCP session is registered.

Nothing is written to disk and nothing survives a restart. There is no device ID, no installation ID, no user ID and no account ID.

This has a consequence worth being explicit about: **install counts are not possible.** The server can be counted in sessions and in processes, and in nothing else. A restart produces a new `process_id`, and a client that reconnects produces a new `session_id`. Growth in event volume is therefore ambiguous between more people using the server and the same people opening more sessions.

`session_id` is *not* the MCP transport's session ID. The transport's session ID is chosen by, and visible to, the client, and under horizontal scaling the same one is seen by several server processes. The reported `session_id` is minted independently for reporting and never leaves the process except in these events.

## Understand which data is collected

One event is built per MCP session and sent when that session ends, and again every four hours for a session that is still open. Every event carries the following envelope:

| Field | Description | Example |
| :---- | :---- | :---- |
| `service` | Always `mcp-grafana`, identifying the reporting product. | `mcp-grafana` |
| `version` | The version of the `mcp-grafana` binary. | `1.4.2` |
| `os` | Operating system. | `linux`, `darwin`, `windows` |
| `arch` | CPU architecture. | `amd64`, `arm64` |
| `process_id` | The random per-process ID described in [Identifiers](#identifiers-and-what-they-cannot-tell-you). | UUID |
| `session_id` | The random per-session ID described in [Identifiers](#identifiers-and-what-they-cannot-tell-you). Never the MCP transport's session ID. | UUID |
| `report_reason` | Why this event was sent: `session_end` or `interval`. | `session_end` |
| `session_duration_ms` | How long the session had been open when the event was built, in milliseconds. Cumulative, unlike the counters. | `1234` |

### Client fields

The MCP client identifies itself in its `initialize` request. That is free text chosen by the client, so it is clamped:

| Field | Description | Example |
| :---- | :---- | :---- |
| `client_name` | The client's reported name, matched case-insensitively against a fixed list of the clients with a setup page under [Clients](../clients/), plus `mcpb` for the desktop extension bundle. Anything else is sent as `other`. | `claude-code` |
| `client_version` | The version the client reported, truncated to 64 bytes. Sent **only** when `client_name` matched the list; the field is absent from the event otherwise, rather than sent empty. | `2.0.1` |

The list is exactly `claude-code`, `claude-desktop`, `codex`, `cursor`, `gemini-cli`, `mcpb`, `vscode-copilot`, `windsurf` and `zed`.

### Tool usage fields

| Field | Description | Example |
| :---- | :---- | :---- |
| `tools_called` | The sorted, comma-joined names of the tools called since the previous event. Absent from the event when no tool was called in that window. | `list_datasources,query_prometheus` |
| `tool_calls` | A nested object keyed by tool name, each holding `calls` and `errors`. Absent from the event when no tool was called in that window, rather than sent as an empty object. | `{"query_prometheus":{"calls":4,"errors":1}}` |
| `calls` | Within `tool_calls`: how many times that tool was called since the previous event. An exact number. | `4` |
| `errors` | Within `tool_calls`: how many of those calls failed. An exact number. | `1` |

Only the tool's name is recorded. Its arguments, its result, the size of its result and the error it returned are all absent — there is no result-size, result-count or cardinality field on the wire at all, in any form, bucketed or otherwise.

### Grafana target fields

| Field | Description | Example |
| :---- | :---- | :---- |
| `grafana_version` | The version the Grafana instance reported, as read from a response the session had already fetched. Empty when the session never reached Grafana. | `12.1.0` |
| `target_kind` | `cloud` or `self_hosted`. Deliberately coarse: never the URL, hostname, stack slug, organisation name or organisation ID. Empty when no Grafana target was resolved. | `cloud` |
| `org_id_set` | Whether a Grafana organisation was selected for the connection. Whether, not which: the organisation ID is never sent. | `true` |
| `auth_method` | The credential category the connection resolved, from a fixed vocabulary, never the credential itself. | `service_account_token` |

The `auth_method` vocabulary is exactly `on_behalf_of`, `access_token`, `service_account_token`, `basic_auth` and `anonymous`, plus the `other` sentinel.

### Server configuration fields

| Field | Description | Example |
| :---- | :---- | :---- |
| `transport` | The transport the server is running: `stdio`, `sse` or `streamable-http`. | `stdio` |
| `flags` | The **names** of the command-line flags that were set, sorted. No flag value is sent in this field. | `disable-write,transport` |
| `enabled_tools` | The tool category names that are actually active, sorted. | `alerting,dashboard,search` |
| `disabled_tools` | The tool category names a `--disable-*` flag turned off, sorted. | `oncall` |
| `loki_guardrail_mode` | The resolved Loki query cost guardrail mode: `off`, `shadow` or `enforce`. | `off` |
| `tls_enabled` | Whether the server is serving HTTPS. Whether, not which certificate: no path or certificate detail is sent. | `false` |
| `metrics_enabled` | Whether the Prometheus metrics endpoint is enabled. | `true` |
| `dynamic_multi_org` | Whether per-call organisation selection is enabled. | `false` |
| `proxied_enabled` | Whether proxied tools from external MCP servers are enabled. | `true` |

## How to read these fields

These fields are easy to misread, so the following constraints are part of the contract:

- **Totals are a floor, not a count.** `calls` and `errors` are deltas since the previous event, and they are reset at the moment an event is *sent*, not when the receiver acknowledges it. A report that never arrives takes its delta with it. Any total built by summing these counts undercounts by an unknown amount, and it undercounts most in exactly the environments where delivery fails most.
- **`tools_called` and `tool_calls` always agree, and both are per-event.** `tools_called` is the sorted key list of `tool_calls` for that same event, not a running list for the session. A session that called one tool in its first four hours and a different one in its second reports one name in each event, never both in either.
- **A field with nothing to say is absent, not empty.** `client_version` for an unrecognised client, and `tools_called` and `tool_calls` for a window in which no tool was called, are left out of the event entirely, so they read as null rather than as an empty string or an empty object. A session that opened and closed without calling anything is still reported — it just carries no tool fields.
- **`errors` counts calls that failed, not why they failed.** A tool that returns an error result and a tool whose handler fails outright both count as one error. Nothing describes the failure: no message, no status, no error category. You can see which tools are failing and never why. A coarse error-kind field may be added later; until then, absence of detail is by design, not an omission.
- **`proxied` is one key covering every proxied tool.** Tools proxied from an external MCP server are all recorded under the single name `proxied`, never by their real names and never by the datasource type they came from. Those names are chosen by a remote server, so this repository cannot bound them, and the datasource type would tell us which backends you run. A call to a tool this build does not have registered — a typo from the model, or a tool whose category is disabled — also lands on `proxied`, so a small `proxied` count is not by itself evidence that proxied tools were used.
- **`target_kind` misclassifies Cloud instances behind a custom domain.** It is derived from the configured URL's hostname alone, and the only positive signal available is the Grafana Cloud domain. A Grafana Cloud stack reached through your own domain reports `self_hosted`. Read `target_kind` as "looks like Cloud" and "everything else", never as an authoritative split.
- **`grafana_version` and the target fields are empty until the session talks to Grafana.** They are read from configuration and from a cache the session's own Grafana requests populate; nothing is fetched to fill them in. A session that opened and closed without calling a tool reports them empty. Empty means "not resolved", never "old" or "self-hosted".
- **`enabled_tools` and `disabled_tools` are not complements.** A category that is neither named in `--enabled-tools` nor explicitly disabled appears in neither list. A category name in `--enabled-tools` that this build does not recognise appears in neither list either, because both are bounded by the categories compiled into the binary.
- **`client_name: other` is not a rare case.** The list is the documented client slugs, and there is no alias table mapping a client's display name onto its slug. A client that reports a display name rather than its slug is reported as `other`, along with anything genuinely unrecognised.
- **`client_version` is the client's own string.** There is no vocabulary to clamp a version against, so when the name matched, the version travels as the client wrote it, bounded only by a 64-byte truncation. Treat it as untrusted text from the connecting client, not as a value this server vouches for.
- **`flags` records what was set, not what is in effect.** A setting supplied through an environment variable rather than a flag does not appear in `flags`, even though it changes the server's behaviour. Read the resolved-state fields — `loki_guardrail_mode`, `tls_enabled`, `metrics_enabled`, `dynamic_multi_org`, `proxied_enabled` — for what the server is actually doing.
- **Grouping by `process_id` reveals deployment concurrency.** One process can host many sessions, so the number of distinct `session_id` values sharing a `process_id`, and their overlap in time, describes how heavily a single deployment is used. That is a property of the data, not a mistake, but it means events are less atomised than a per-session identifier suggests.

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
- Your IP address. See [Server-side enrichment](#server-side-enrichment) for what the receiving service derives from the connection.

## How the report is sent

One MCP session produces one event when it ends, plus one every four hours while it stays open. The server does not batch events, does not queue them for a later process, and does not start a background process that outlives it.

| Property | Value |
| --- | --- |
| Destination | `https://stats.grafana.org/mcp-grafana-usage-report` |
| Method | A single `POST` with the event as a JSON object |
| Attempts | One. A failed report is never retried and never stored. |
| Interval | Every four hours per open session, with up to ±10% random jitter applied to the first interval so that servers started together do not report in lockstep |
| Timing | In the background for interval reports and for a session that ends while the server keeps running; synchronously during shutdown |
| Time limit | Five seconds for a background report; one second for the whole shutdown flush, however many sessions it covers |

Failures are logged at debug level and nowhere else. Nothing is ever written to standard output, because under the `stdio` transport standard output is the MCP protocol channel and anything written there would corrupt the conversation with your client.

Two consequences of this design:

- **`SIGKILL` loses the final report.** The shutdown flush runs on `SIGTERM` and `SIGINT`. A process killed outright, or one that dies with the machine, never builds its last event, so the work in that session's final unreported window is lost.
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

There are two controls, and the flag wins over the environment variable:

1. **`--usage-stats` flag**: set it to `enabled`, `disabled` or `log`.

```shell
mcp-grafana --usage-stats=disabled
```

2. **`GRAFANA_USAGE_STATS` environment variable**: set it to `enabled`, `disabled` or `log`.

```shell
export GRAFANA_USAGE_STATS=disabled
```

Any value that is not one of those three disables reporting, so a typo fails toward privacy rather than toward collection. Opting out disables reporting entirely: no event is constructed and no connection is opened.

There is no configuration file and no `DO_NOT_TRACK` support. Refer to [Command-line flags](../configure/command-line-flags/) for where `--usage-stats` sits among the other flags.

## The startup notice, and who sees it

When reporting is on, the server logs one line at startup that says usage statistics are being collected, how to turn them off, and where this page is. It is logged once per process start. Nothing is persisted, so there is no notice revision to track and no file to delete: every start logs it again.

The limit worth stating plainly is that **under the `stdio` transport nobody using the server sees that line.** The server's stderr is a log file belonging to the MCP client, and the person typing into the assistant is unlikely ever to read it. Reporting is a decision made by whoever configured the MCP client, and it is that person's responsibility to tell the people whose sessions are being reported. If you run the server on behalf of other people and cannot tell them, opt out.

For the HTTP transports the same line goes to the server's own logs, where an operator sees it alongside every other startup message.
