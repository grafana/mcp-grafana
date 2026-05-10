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

## Limitations in Phase 1

- RBAC tool gating is not yet implemented; every authenticated user sees the
  full tool list. Grafana still enforces RBAC on each tool call.
- Single-replica only when using the file store. Multi-replica deployments
  require all replicas to share the state directory on a network filesystem.
- See the design spec for upcoming phases (Mode A: Grafana OAuth2 server;
  Mode S: SAML).
