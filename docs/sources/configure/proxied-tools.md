---
title: Proxied tools
menuTitle: Proxied tools
description: Additional MCP tools loaded through Grafana’s datasource proxy; today only Grafana Tempo.
keywords:
  - MCP
  - proxied
  - Tempo
  - datasource
weight: 4
aliases: []
---

# Proxied tools

Proxied tools are additional MCP tools that this server does not implement itself. It loads them from an MCP server that sits behind a Grafana datasource, using Grafana’s datasource proxy. Your client still talks only to this MCP server; the extra tools show up alongside the built-in ones.

Today only the [Grafana Tempo MCP server](https://grafana.com/docs/tempo/latest/api_docs/mcp-server/) is supported as a proxied source. Adding another datasource type for proxied tools requires a change to this server, not Grafana configuration alone.

## What you'll achieve

Enable the MCP server in Grafana Tempo and use `--disable-proxied` when you want proxied tools disabled.

## Proxy the Grafana Tempo MCP Server

Complete [authentication](../authentication/) to Grafana (`GRAFANA_URL` and credentials). Do not pass `--disable-proxied` if you want proxied tools loaded.

Enable Tempo’s MCP server so the proxy path responds (for example `query_frontend.mcp_server.enabled` in YAML or flag `query-frontend.mcp-server.enabled`). Refer to the [Tempo MCP server](https://grafana.com/docs/tempo/latest/api_docs/mcp-server/#configuration) documentation.

Add a Tempo datasource in Grafana if you do not already have one.

Tools appear as `tempo_<remote-tool-name>`. They are not listed in the static [MCP tools reference](../reference/mcp-tools-table/). Use your MCP client to list tools from the server.

## Disable proxied tools

Proxied tools are enabled by default on this server. Pass `--disable-proxied` to disable them. The `proxied` token in `--enabled-tools` does not gate proxied tools; only `--disable-proxied` does. Omitting `proxied` from `--enabled-tools` does not disable them.

With stdio transport, proxied tools are discovered once at startup. With SSE or streamable-http, discovery runs per MCP session when tools are listed or called.

## Next steps

- [Enable and disable tools](../enable-and-disable-tools/)
- [Command-line flags](../command-line-flags/)
- [Transports and addresses](../transports-and-addresses/)
