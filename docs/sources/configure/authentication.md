---
title: Authentication
menuTitle: Authentication
description: Authenticate the Grafana MCP server to Grafana using a service account token or username and password.
keywords:
  - authentication
  - service account
  - token
  - MCP
weight: 1
aliases: []
---

# Authentication

The Grafana MCP server needs credentials to call the Grafana API. Use a service account token (recommended) or a username and password.

## What you'll achieve

You set the environment variables (or equivalent) so the server can authenticate to your Grafana instance. The same credentials apply whether you run the server with uvx, Docker, a binary, or Helm.

## Before you begin

- A Grafana instance (Grafana 9.0 or later).
- For token auth: a [service account](https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana) with a token and the RBAC permissions needed for the tools you use.

## Use a service account token

Set `GRAFANA_URL` and `GRAFANA_SERVICE_ACCOUNT_TOKEN` in the environment passed to the server.

- **GRAFANA_URL:** Your Grafana base URL (for example, `http://localhost:3000` or `https://myinstance.grafana.net`).
- **GRAFANA_SERVICE_ACCOUNT_TOKEN:** The token you created for the service account.

The deprecated `GRAFANA_API_KEY` is still supported but will be removed in a future version; use `GRAFANA_SERVICE_ACCOUNT_TOKEN` instead.

## Use username and password

You can use basic auth by setting `GRAFANA_USERNAME` and `GRAFANA_PASSWORD` instead of a token. This is less suitable for automation; prefer a service account token when possible.

## Next steps

- [Enable and disable tools](../enable-and-disable-tools/) to control which MCP tools are available.
- [Transports and addresses](../transports-and-addresses/) for stdio, SSE, and streamable-http.
