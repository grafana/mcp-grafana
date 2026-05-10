---
title: Per-user authentication (Grafana oauth2_server, Mode A)
menuTitle: Per-user auth (Grafana)
description: Run mcp-grafana behind Grafana's experimental oauth2_server so each user authenticates as themselves with their own Grafana credentials.
keywords:
  - authentication
  - OAuth
  - Grafana
  - per-user
weight: 25
---

# Per-user authentication with Grafana's oauth2_server

When `mcp-grafana` runs as a shared HTTP/SSE service, you can have each user
authenticate against your Grafana instance directly using Grafana's
experimental [external service authentication / oauth2_server feature][gf-oauth2].

This page covers **Mode A** (`--auth-mode=oauth-grafana`): users sign in via
Grafana itself (which routes through whatever SSO Grafana is configured for),
and the Grafana-issued bearer token becomes the per-user credential
`mcp-grafana` uses on every API call. There's no service-account-token paste
step — credentials come from Grafana directly.

## What you need

- Grafana 11+ with `oauth2_server` enabled in `grafana.ini` and an external
  service registered for `mcp-grafana`. See the [Grafana docs][gf-oauth2] for
  registration steps.
- A public HTTPS URL for `mcp-grafana` (`--public-url`).
- A 32-byte AES-GCM key (`--token-encryption-key`).

## Running it

```bash
mcp-grafana \
  -t streamable-http \
  --address :8000 \
  --auth-mode=oauth-grafana \
  --public-url=https://mcp.example.com \
  --token-encryption-key="$(openssl rand -base64 32)" \
  --auth-state-dir=/var/lib/mcp-grafana \
  --grafana-oauth2-issuer-url=https://grafana.example.com \
  --grafana-oauth2-client-id=mcp-grafana \
  --grafana-oauth2-client-secret=...
```

The issuer URL defaults to `${GRAFANA_URL}` when not explicitly set. Set
`GRAFANA_URL` to the same Grafana instance.

## What users see

1. The user opens an MCP client (Claude Desktop, Cursor, etc.) and points it
   at `https://mcp.example.com/mcp`.
2. The client discovers OAuth and opens a browser to your Grafana login.
3. Grafana authenticates the user (via whatever SSO it's configured for).
4. The user is redirected back to `mcp-grafana`, which receives a Grafana
   bearer + refresh token.
5. Subsequent tool calls use the Grafana bearer; near-expiry, `mcp-grafana`
   transparently refreshes via the stored refresh token.

## Token rotation

`mcp-grafana` automatically refreshes the upstream Grafana token when it's
within 60 seconds of expiry. Failed refresh (e.g., revoked refresh token)
returns 401 to the MCP client, which re-runs the OAuth flow.

## Key rotation

Same as Mode C: restart with `--token-encryption-key=NEW
--token-encryption-key-previous=OLD` to re-wrap stored credentials under a
new encryption key.

## Comparison with Mode C

|                          | Mode C (oauth-oidc)            | Mode A (oauth-grafana)         |
|--------------------------|--------------------------------|--------------------------------|
| User authenticates via   | Generic OIDC IdP               | Grafana directly               |
| Grafana credential       | User-supplied SA token         | Grafana-issued bearer          |
| First-login bootstrap    | Required (paste SA token)      | None — Grafana issues bearer   |
| Token expiry             | None (until user revokes)      | Bounded; auto-refresh handled  |
| Grafana server requirement | None special                  | Grafana 11+ with `oauth2_server` enabled |

[gf-oauth2]: https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/configure-authentication/oauth2-server/
