# OAuth2 + Auth Proxy Setup

This is the single reference for running MCP Grafana with OAuth2 token validation and Grafana Auth Proxy user impersonation.

## What this flow does

- Client sends `Authorization: Bearer <token>` to MCP.
- MCP validates token by calling the OAuth2 `userinfo` endpoint.
- MCP forwards user identity to Grafana using `X-WEBAUTH-*` headers.
- Grafana applies permissions as the authenticated user.

## Minimal environment

MCP OAuth2 settings:

```bash
OAUTH2_ENABLED=true
OAUTH2_PROVIDER_URL=http://localhost:8082/auth/realms/mcp-grafana
OAUTH2_USERINFO_ENDPOINT=/protocol/openid-connect/userinfo
OAUTH2_TOKEN_CACHE_TTL=300

# Optional: enable forwarding validated OAuth2 user tokens to Grafana
OAUTH2_TOKEN_FORWARD_TO_GRAFANA_ENABLED=true

# Optional: only for Grafana Cloud style token-forwarding headers
# When false (default), MCP forwards Authorization: Bearer <user token>
OAUTH2_TOKEN_FORWARD_TO_GRAFANA_USE_CLOUD_HEADERS=false

# Inactive alternative (Grafana Cloud-style forwarding):
# OAUTH2_TOKEN_FORWARD_TO_GRAFANA_USE_CLOUD_HEADERS=true
# GRAFANA_API_KEY=<grafana-service-token>

# Inactive legacy aliases:
# OAUTH2_OBO_ENABLED=true
# OAUTH2_OBO_USE_GRAFANA_HEADERS=false
```

Grafana settings for MCP:

```bash
GRAFANA_URL=http://localhost:3000
GRAFANA_PROXY_AUTH_ENABLED=true

# Optional header overrides
GRAFANA_PROXY_USER_HEADER=X-WEBAUTH-USER
GRAFANA_PROXY_EMAIL_HEADER=X-WEBAUTH-EMAIL
GRAFANA_PROXY_NAME_HEADER=X-WEBAUTH-NAME
GRAFANA_PROXY_ROLE_HEADER=X-WEBAUTH-ROLE
```

Notes:

- If `OAUTH2_ENABLED=true` and `OAUTH2_PROVIDER_URL` is empty, OAuth2 is disabled.
- When `OAUTH2_TOKEN_FORWARD_TO_GRAFANA_ENABLED=true`, MCP forwards authenticated requests to Grafana using:
  - `Authorization: Bearer <validated incoming bearer token>` by default.
  - `X-Access-Token: <GRAFANA_SERVICE_ACCOUNT_TOKEN|GRAFANA_API_KEY>` and `X-Grafana-Id: <validated incoming bearer token>` only when `OAUTH2_TOKEN_FORWARD_TO_GRAFANA_USE_CLOUD_HEADERS=true`.
- Legacy env vars `OAUTH2_OBO_ENABLED` and `OAUTH2_OBO_USE_GRAFANA_HEADERS` are still supported.

## Local test setup (recommended)

1. Prepare local infra and `.env.oauth2-test`:

```bash
./testdata/oauth2-setup.sh
```

2. Start MCP:

```bash
source .env.oauth2-test
go run ./cmd/mcp-grafana/main.go
```

3. Run end-to-end check:

```bash
./testdata/oauth2-test.sh test-flow john.doe password123
```

Local services started by setup script:

- Keycloak: `http://localhost:8082`
- Grafana (auth proxy demo): `http://localhost:3000`

Default test users:

- `john.doe` / `password123` (editor)
- `jane.smith` / `password123` (viewer)
- `admin` / `admin123` (admin)

## Manual verification

Get a user token:

```bash
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)
```

Call MCP with token:

```bash
curl -s http://localhost:8080/tools \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Expected outcome:

- MCP accepts the request.
- MCP logs token validation and user extraction.
- Grafana calls include `X-WEBAUTH-*` identity headers.

## Grafana UI login via Keycloak

The local compose setup also enables Grafana Generic OAuth against Keycloak.

1. Open `http://localhost:3000/login`.
2. Click `Sign in with Keycloak`.
3. Use a test user from the realm (for example `john.doe` / `password123`).

## Troubleshooting

Health checks:

```bash
curl -s http://localhost:8082/health/ready
curl -s http://localhost:3000/health
```

Common failures:

- `401 Unauthorized` from MCP:
  - token is invalid/expired, or provider URL is wrong.
- OAuth2 appears disabled:
  - `OAUTH2_ENABLED=true` not set, or provider URL missing.
- Grafana user context not applied:
  - `GRAFANA_PROXY_AUTH_ENABLED=true` missing, or Grafana auth proxy not configured.

## Related files

- Setup script: `testdata/oauth2-setup.sh`
- Test helper: `testdata/oauth2-test.sh`
- Local env file: `.env.oauth2-test`
