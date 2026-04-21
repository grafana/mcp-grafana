---
title: Introduction to the Grafana MCP server
menuTitle: Introduction
description: "Key concepts: Model Context Protocol, tools and capabilities, and authentication with Grafana."
keywords:
  - MCP
  - Model Context Protocol
  - tools
  - RBAC
  - Grafana
weight: 5
aliases: []
---

# Introduction to the Grafana MCP server

This article outlines what the Grafana MCP server is, what it can do, and how authentication and permissions work.

## What you'll achieve

You understand how the server fits into the Model Context Protocol ecosystem, what kinds of tools it exposes (metrics, logs, traces, dashboards, alerting, and more), and how Grafana RBAC applies.

## Before you begin

You will need a Grafana instance (Grafana 9.0 or later) and an MCP-compatible client (for example, Claude Desktop, Cursor, or VS Code with Copilot).

## Model Context Protocol (MCP)

The [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) is a standard way for AI assistants and LLM clients to connect to external systems. The Grafana MCP server implements MCP so your client can talk to Grafana without you writing custom integrations. The server exposes tools that map to actions in Grafana such as querying metrics and logs, searching dashboards, managing alert rules, working with incidents and OnCall, and generating deeplinks. Your AI assistant calls these tools on your behalf when you ask questions or give instructions that involve Grafana.

## Tools and capabilities

The server exposes many tools, grouped by area: dashboards (search, get summary, get panel queries, update, patch), folders (search, create), datasources (list and query Prometheus, Loki, ClickHouse, CloudWatch, Elasticsearch, Pyroscope), alerting (rules and routing), incidents (Grafana Incident), Sift (investigations, error patterns, slow requests), OnCall (schedules, alert groups), navigation (deeplinks), annotations, and rendering (panel or dashboard images). It can also expose [proxied tools](../configure/proxied-tools/) from external MCP servers reachable through Grafana (for example from Grafana Tempo). Some tool categories are disabled by default to save context window; you enable them with [Enable and disable tools](../configure/enable-and-disable-tools/). For dashboards, prefer `get_dashboard_summary` and `get_dashboard_property` over `get_dashboard_by_uid` when you do not need the full JSON, to manage context window use.

## Authentication and RBAC

The server uses a Grafana [service account token](https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana) (or username and password) to call the Grafana API. Each tool requires the right Grafana RBAC permissions and scopes. For example, reading dashboards needs `dashboards:read` and a scope like `dashboards:*` or `dashboards:uid:xyz`. Creating or updating dashboards needs `dashboards:create` and `dashboards:write` and appropriate folder scopes. You can assign the built-in Editor role to the service account for broad access, or use fine-grained permissions for least-privilege. Grafana Incident and Sift use basic roles (Viewer for read, Editor for write). For full RBAC details, refer to [Grafana RBAC](https://grafana.com/docs/grafana/latest/administration/roles-and-permissions/access-control/).

## Next steps

- [Clients](../clients/), [Set up](../set-up/), and [Configure](../configure/).
- [MCP tools reference](../reference/mcp-tools-table/) for tools, permissions, scopes, and RBAC guidance.
- [Client configuration examples](../set-up/client-configuration-examples/).
- [Guides](../guides/).
