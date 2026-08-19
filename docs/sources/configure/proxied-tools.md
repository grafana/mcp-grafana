---
title: Proxied tools
menuTitle: Proxied tools
description: Additional MCP tools, resources, and prompts loaded through Grafana’s datasource proxy; today only Grafana Tempo.
keywords:
  - MCP
  - proxied
  - Tempo
  - datasource
weight: 4
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/configure/proxied-tools/
---

# Proxied tools

Proxied tools are additional MCP tools that this server does not implement itself. It loads them from an MCP server that sits behind a Grafana datasource, using Grafana’s datasource proxy. Your client still talks only to this MCP server; the extra tools show up alongside the built-in ones. The upstream server's resources, resource templates, and prompts are re-exposed the same way — see [Proxied resources and prompts](#proxied-resources-and-prompts).

Today only the [Grafana Tempo MCP server](https://grafana.com/docs/tempo/latest/api_docs/mcp-server/) is supported as a proxied source. Adding another datasource type for proxied tools requires a change to this server, not Grafana configuration alone.

## What you'll achieve

Enable the MCP server in Grafana Tempo and use `--disable-proxied` when you want proxied tools disabled.

## Proxy the Grafana Tempo MCP Server

Complete [authentication](../authentication/) to Grafana (`GRAFANA_URL` and credentials). Do not pass `--disable-proxied` if you want proxied tools loaded.

Enable Tempo’s MCP server so the proxy path responds (for example `query_frontend.mcp_server.enabled` in YAML or flag `query-frontend.mcp-server.enabled`). Refer to the [Tempo MCP server](https://grafana.com/docs/tempo/latest/api_docs/mcp-server/#configuration) documentation.

Add a Tempo datasource in Grafana if you do not already have one.

Tools appear as `tempo_<remote-tool-name>`. They are not listed in the static [MCP tools reference](../../reference/mcp-tools-table/). Use your MCP client to list tools from the server.

## Proxied resources and prompts

An upstream MCP server can expose more than tools, and everything it exposes is re-exposed here. Tempo, for example, publishes its TraceQL documentation as resources.

Each name or URI is namespaced with the datasource type and UID, so several datasources of the same type can expose the same names without colliding:

| Upstream capability | How it appears | Selecting the datasource |
|---------------------|----------------|--------------------------|
| Tool | `tempo_<remote-tool-name>` | a required `datasourceUid` argument added to the tool |
| Resource, resource template | `urn:mcp-grafana:tempo:<datasourceUid>:<original-uri>` | encoded in the URI |
| Prompt (stdio only) | `tempo_<remote-prompt-name>` | a required `datasourceUid` argument added to the prompt |

Reading a namespaced resource URI recovers the original upstream URI and forwards the read through the datasource proxy, so your client never needs to know the upstream URI. Resource templates work the same way: expand the advertised template as usual and send the result back — for Tempo, `urn:mcp-grafana:tempo:<datasourceUid>:docs://traceql/{section}` expands to a URI this server routes for you.

Permissions are unchanged from tools: every proxied read is forwarded with the caller's credentials, so it needs `datasources:query` on that datasource UID, and Grafana attaches the datasource's own saved authentication when it forwards upstream.

Prompts are only proxied on the **stdio** transport. On SSE and streamable-http they are skipped and the prompt capability is not advertised, because a prompt cannot be scoped to a single MCP session and a server-wide registration would expose one caller's prompts to all others. Tempo publishes no prompts today, so this affects no current deployment.

## Disable proxied tools

Proxied tools are enabled by default on this server. Pass `--disable-proxied` to disable them. The `proxied` token in `--enabled-tools` does not gate proxied tools; only `--disable-proxied` does. Omitting `proxied` from `--enabled-tools` does not disable them.

With stdio transport, proxied tools are discovered once at startup. With SSE or streamable-http, discovery runs the first time a session lists or calls anything proxied, and the result is shared by every session presenting the same credentials, so a busy server discovers once rather than once per session.

## Next steps

- [Enable and disable tools](../enable-and-disable-tools/)
- [Command-line flags](../command-line-flags/)
- [Transports and addresses](../transports-and-addresses/)
