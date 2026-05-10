---
title: Per-user authentication (SAML, Mode S)
menuTitle: Per-user auth (SAML)
description: Run mcp-grafana behind a SAML 2.0 IdP for per-user authentication.
keywords:
  - authentication
  - SAML
  - per-user
weight: 30
---

# Per-user authentication with SAML 2.0

When `mcp-grafana` runs as a shared HTTP/SSE service, you can have each
user authenticate against your SAML 2.0 IdP (Okta, Azure AD, Keycloak,
Grafana Enterprise SAML, etc.).

This page covers **Mode S** (`--auth-mode=saml`): users sign in via the
configured SAML IdP, then on first login paste a personal Grafana
service-account token (the same bootstrap flow used by Mode C OIDC). The
token is encrypted and used as the per-user credential thereafter.

For **Mode C** (generic OIDC IdP), see [Per-user auth (OIDC)](../per-user-auth-oidc/).
For **Mode A** (Grafana's own OAuth2 server), see [Per-user auth (Grafana)](../per-user-auth-grafana/).

## What you need

- A SAML 2.0 IdP. Export its metadata XML (URL or file).
- A SP X.509 cert + RSA key for assertion signing and validation.
  Generate with:
  ```bash
  openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout sp.key -out sp.crt -days 365 \
    -subj "/CN=mcp-grafana-sp"
  ```
- A public HTTPS URL for `mcp-grafana` (`--public-url`).
- A 32-byte AES-GCM key (`--token-encryption-key`).
- Each user has (or can create) a personal Grafana service-account token.

## Running it

```bash
mcp-grafana \
  -t streamable-http \
  --address :8000 \
  --auth-mode=saml \
  --public-url=https://mcp.example.com \
  --token-encryption-key="$(openssl rand -base64 32)" \
  --auth-state-dir=/var/lib/mcp-grafana \
  --saml-idp-metadata-url=https://idp.example.com/metadata \
  --saml-sp-cert-file=/etc/mcp/sp.crt \
  --saml-sp-key-file=/etc/mcp/sp.key
```

Set `GRAFANA_URL` to the Grafana instance to authenticate against.

## Registering the SP with your IdP

1. Start mcp-grafana. The SP metadata is served at
   `https://mcp.example.com/saml/metadata`.
2. Import that metadata into your IdP (or copy the EntityDescriptor XML).
3. Configure the IdP to release the user's email and group attributes
   (default attribute names: `email` and `groups`; override with
   `--saml-attribute-email` and `--saml-attribute-groups`).
4. The SP's ACS URL is `https://mcp.example.com/saml/acs`.

## What users see

1. The user opens an MCP client (Claude Desktop, Cursor, etc.) and points
   it at `https://mcp.example.com/mcp`.
2. The client discovers OAuth on the MCP server and opens a browser.
3. The browser is redirected to the SAML IdP, where the user signs in.
4. The IdP POSTs a SAMLResponse to `/saml/acs`; mcp-grafana validates it.
5. **First login only:** the user is prompted to paste a personal Grafana
   service-account token at `/bootstrap`.
6. Subsequent connections skip the bootstrap step.

## Single Logout

Optional. Add `--saml-enable-slo` and configure your IdP to send
LogoutRequests to `https://mcp.example.com/saml/sls`. mcp-grafana deletes
the user's session and returns a LogoutResponse.

> **Security note:** Phase 4 ships without IdP-signature validation on
> inbound LogoutRequests. Until that's implemented, restrict `/saml/sls`
> via mTLS, IP allowlist, or similar defense-in-depth. SLO is disabled by
> default for this reason.

## IdP-initiated SSO

Disabled by default. Enable with `--saml-allow-idp-initiated`. SP-initiated
flows are safer (the relay state is freshly minted by mcp-grafana for each
flow); IdP-initiated flows accept any inbound assertion without that pin.

## Comparison with other modes

|                              | Mode C (OIDC)  | Mode A (Grafana)                  | Mode S (SAML)  |
|------------------------------|----------------|-----------------------------------|----------------|
| Identity provider            | Generic OIDC   | Grafana itself                    | SAML 2.0 IdP   |
| First-login bootstrap        | Required       | Not required                      | Required       |
| Token rotation               | None           | Auto-refresh                      | None           |
| Server requirement           | None           | Grafana 11+ with `oauth2_server`  | None           |
| Single Logout (SLO)          | N/A            | N/A                               | Optional       |

See [Per-user auth (OIDC)](../per-user-auth-oidc/) and
[Per-user auth (Grafana)](../per-user-auth-grafana/) for the alternatives.
