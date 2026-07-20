---
title: Set up the Grafana MCP server
menuTitle: Set up
description: Install and run the Grafana MCP server using uvx, Docker, a binary, or Helm.
keywords:
  - MCP
  - install
  - uvx
  - Docker
  - Helm
weight: 10
aliases: []
---

# Set up the Grafana MCP server

Choose how you install and run the open source Grafana MCP server. Start with `uvx` for the least setup, or use Docker, a downloaded binary, or Helm when that fits your environment. Refer to [Clients](../clients/) for client-specific steps and [Client configuration examples](client-configuration-examples/) for copy-paste MCP JSON (debug, TLS, and more).

{{< admonition type="note" >}}
These instructions cover the open source server, which you install and run yourself. If you want to connect an external AI agent to Grafana Cloud without running your own server, refer to [Grafana Cloud MCP server](https://grafana.com/docs/grafana-cloud/machine-learning/assistant/configure/cloud-mcp/) instead.
{{< /admonition >}}

{{< section withDescriptions="true" >}}
