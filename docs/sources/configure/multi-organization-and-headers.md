---
title: Multi-organization and headers
menuTitle: Multi-organization and headers
description: Target a specific Grafana organization and send custom HTTP headers from the MCP server.
keywords:
  - organization
  - org ID
  - headers
  - MCP
weight: 6
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/configure/multi-organization-and-headers/
---

# Multi-organization and headers

You can point the server at a specific Grafana organization and add custom HTTP headers to every request to Grafana.

## What you'll achieve

All Grafana API calls use the chosen organization context, and any extra headers you need (for example, tenant or custom auth) are sent automatically.

## Before you begin

- A Grafana instance with multiple organizations (or a need for custom headers).
- The server configured with [Authentication](../authentication/).

## Set the organization

Set **GRAFANA_ORG_ID** to the numeric organization ID. The server sends `X-Grafana-Org-Id` on all requests to Grafana.

When using streamable-http, you can also send **X-Grafana-Org-Id** from the client; the header takes precedence over the environment variable so you can override the default org per request.

## Select an organization per tool call

The settings above fix the organization for the whole connection. To let a single connection target different organizations per call, start the server with the `--dynamic-multi-org` flag. This is off by default.

When enabled:

- Every tool accepts an optional `orgId` argument that overrides the connection's organization for that call, setting both the `X-Grafana-Org-Id` header and, for app-platform APIs, the resolved Kubernetes namespace. Calls that omit `orgId` use the connection's default organization.
- Tempo tools resolve the datasource in the calling organization, so you can target a Tempo datasource in any org with `orgId` plus its `datasourceUid`.

This only works for credentials that belong to more than one organization (for example a user or on-behalf-of identity); a service-account token stays bound to its single organization. Call the `user_info` tool to discover which `orgId` values are valid.

## Send custom headers

Set **GRAFANA_EXTRA_HEADERS** to a JSON object mapping header names to values. These headers are added to every Grafana API request.

Example:

```json
"GRAFANA_EXTRA_HEADERS": "{\"X-Custom-Header\": \"value\", \"X-Tenant-ID\": \"tenant-123\"}"
```

A `Host` entry sets the Host header of the request to Grafana. Use it when the server reaches Grafana through an internal address but Grafana's `enforce_domain` setting expects the public domain, for example `{"Host": "grafana.example.com"}`. `Host` is only read from `GRAFANA_EXTRA_HEADERS`, never from `GRAFANA_FORWARD_HEADERS`. TLS certificate verification still uses the host from `GRAFANA_URL`, so a certificate issued only for the public domain fails verification when you connect through an internal name.

## Next steps

- [Authentication](../authentication/) for Grafana credentials.
- [Enable and disable tools](../enable-and-disable-tools/) to limit which tools are available.
