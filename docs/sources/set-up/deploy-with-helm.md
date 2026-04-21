---
title: Deploy with Helm
menuTitle: Deploy with Helm
description: Deploy the Grafana MCP server to Kubernetes using the Helm chart.
keywords:
  - Helm
  - Kubernetes
  - MCP
weight: 4
aliases: []
---

# Deploy with Helm

Deploy the Grafana MCP server on Kubernetes using the Helm chart from the Grafana helm-charts repository.

## What you'll achieve

The server runs in your cluster and can be used by MCP clients that connect to it (for example, via SSE or streamable-http and an Ingress or LoadBalancer).

## Before you begin

- `kubectl` and Helm installed.
- A Grafana URL and API key (or service account token) for the server to use.

## Install the chart

Add the Grafana Helm repo and install the chart. Set `grafana.url` and `grafana.apiKey` (or the equivalent for your chart version) to your Grafana instance and token.

```bash
helm repo add grafana-community https://grafana-community.github.io/helm-charts
helm install --set grafana.apiKey=<Grafana_ApiKey> --set grafana.url=<GrafanaUrl> my-release grafana-community/grafana-mcp
```

For full chart options and defaults, refer to the [grafana-mcp chart](https://github.com/grafana-community/helm-charts/tree/main/charts/grafana-mcp) in the grafana-community helm-charts repository.

## Next steps

- [Configure authentication](../../configure/authentication/) for service accounts and tokens.
- [Configure transports and addresses](../../configure/transports-and-addresses/) for SSE or streamable-http in-cluster.
