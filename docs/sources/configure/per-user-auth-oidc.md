---
title: Per-user authentication (OIDC, Mode C)
menuTitle: Per-user auth (OIDC)
description: Run mcp-grafana behind an OIDC IdP and have each user authenticate as themselves to Grafana via a personal service-account token.
keywords:
  - authentication
  - OAuth
  - OIDC
  - per-user
weight: 20
---

# Per-user authentication with OIDC + service-account-token bootstrap

When `mcp-grafana` runs as a shared HTTP/SSE service, you can require each
user to authenticate as themselves before tool calls reach Grafana.

This page covers **Mode C** (`--auth-mode=oauth-oidc`): users sign in
through a generic OIDC IdP (Auth0, Keycloak, Okta, etc.), then on first
login paste a personal Grafana service-account token. The token is
encrypted at rest and used for all subsequent calls.

## What you need

- A public HTTPS URL for `mcp-grafana` (`--public-url`).
- A 32-byte AES-GCM key (`--token-encryption-key`).
- An OIDC IdP that supports the authorization-code flow with PKCE.
- Each user has (or can create) a personal Grafana service-account token.

## Running it

```bash
mcp-grafana \
  -t streamable-http \
  --address :8000 \
  --auth-mode=oauth-oidc \
  --public-url=https://mcp.example.com \
  --token-encryption-key="$(openssl rand -base64 32)" \
  --auth-state-dir=/var/lib/mcp-grafana \
  --oidc-issuer-url=https://idp.example.com \
  --oidc-client-id=mcp-grafana \
  --oidc-client-secret=...
```

Set `GRAFANA_URL` to the Grafana instance to authenticate against.

## What users see

1. The user opens an MCP client (Claude Desktop, Cursor, etc.) and points it
   at `https://mcp.example.com/mcp`.
2. The client discovers OAuth and opens a browser to your IdP login page.
3. After login, on first connection only, the user is shown a one-time form
   asking them to paste a personal Grafana service-account token.
4. Subsequent connections skip the bootstrap step and go straight to tool calls.

## Token rotation

Restarting `mcp-grafana` with `--token-encryption-key=NEW
--token-encryption-key-previous=OLD` accepts old ciphertext for decryption
while encrypting new data with the new key. Drain the old key over time and
remove `--token-encryption-key-previous` once all sessions have rotated.

## RBAC tool gating

When per-user auth is enabled, `mcp-grafana` can filter the list of tools
each user sees based on their Grafana RBAC permissions.

`--rbac-gating` controls the mode (default `auto`):

| Value        | Behavior                                                                 |
| ------------ | ------------------------------------------------------------------------ |
| `auto`       | Detect edition at runtime. Non-empty permission sets → enterprise mode; empty → basic-role mode. |
| `enterprise` | Filter by fine-grained RBAC permissions from `/api/access-control/user/permissions`. |
| `basic`      | Filter by built-in role only (Viewer / Editor / Admin).                  |
| `off`        | No filtering — every authenticated user sees the full tool list.         |

`--rbac-cache-ttl` sets how long a user's permission snapshot is cached before
re-fetching from Grafana (default `5m`).

Grafana always enforces RBAC on the underlying API call; tool-list gating is
an additional UX layer that hides tools the user cannot use.

## Known limitations

- Single-replica only when using the file store. Multi-replica deployments
  require all replicas to share the state directory on a network filesystem.
