# OAuth2 Testdata Guide

This directory contains files used by the local OAuth2 testing.

## Files

- `oauth2-setup.sh`: Starts local services (Keycloak, Grafana, etc.)
- `oauth2-test.sh`: Helper script for getting tokens and running end-to-end tests
- `keycloak-realm.json`: Keycloak realm, users, groups, and clients for local testing
- `.env.oauth2-forward-test`: Environment config for token forwarding mode (default)
- `.env.oauth2-test`: Environment config for token forwarding + Auth Proxy mode

## Configuration Modes

### Token Forwarding (Default) - Recommended

MCP forwards validated OAuth2 user tokens to Grafana for API authentication.

```bash
chmod +x testdata/oauth2-setup.sh testdata/oauth2-test.sh
./testdata/oauth2-setup.sh
source testdata/.env.oauth2-forward-test
go run ./cmd/mcp-grafana/main.go
```

In another terminal:

```bash
./testdata/oauth2-test.sh test-flow john.doe password123
```

**Behavior:**
- ✅ Token forwarding: ENABLED (default)
- ❌ Auth Proxy: DISABLED (default)
- MCP uses `Authorization: Bearer <user-token>` for Grafana API calls

### Token Forwarding + Auth Proxy - Optional

Both token forwarding and Auth Proxy enabled for combined user identity handling.

```bash
./testdata/oauth2-setup.sh
source testdata/.env.oauth2-test
go run ./cmd/mcp-grafana/main.go
```

**Behavior:**
- ✅ Token forwarding: ENABLED
- ✅ Auth Proxy: ENABLED
- MCP uses bearer token AND X-WEBAUTH-* headers for user session management

### Token Forwarding with Cloud Headers - For Grafana Cloud

Use Grafana Cloud-style headers instead of bearer tokens.

Edit `testdata/.env.oauth2-forward-test` and uncomment:

```bash
export OAUTH2_TOKEN_FORWARD_TO_GRAFANA_USE_CLOUD_HEADERS=true
export GRAFANA_API_KEY=<grafana-service-account-token>
```

Then start MCP:

```bash
source testdata/.env.oauth2-forward-test
go run ./cmd/mcp-grafana/main.go
```

**Behavior:**
- MCP forwards `X-Access-Token` (service token) and `X-Grafana-Id` (user token)

## Useful commands

Get user token:

```bash
./testdata/oauth2-test.sh token john.doe password123
```

List Grafana users created via Auth Proxy:

```bash
./testdata/oauth2-test.sh get-users
```

Stop local containers:

```bash
./testdata/oauth2-test.sh cleanup
```

View token claims (base64 decode):

```bash
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)
echo $TOKEN | cut -d. -f2 | tr '_-' '/+' | base64 -d | jq .
```

## Testing Scenarios

### Scenario 1: Token Forwarding Only

Use `testdata/.env.oauth2-forward-test`:
- MCP validates user token with OAuth2 provider
- MCP forwards token to Grafana as bearer token
- Grafana API calls are authenticated as the user

### Scenario 2: Token Forwarding + Auth Proxy

Use `testdata/.env.oauth2-test`:
- MCP validates user token with OAuth2 provider
- MCP forwards token to Grafana as bearer token
- MCP ALSO injects X-WEBAUTH-* headers for Grafana user session management
- Grafana creates/updates user based on headers; API calls use bearer token

### Scenario 3: Disable Token Forwarding (Auth Proxy Only)

Edit `testdata/.env.oauth2-test` and add:
```bash
export OAUTH2_TOKEN_FORWARD_TO_GRAFANA_ENABLED=false
```

Then start MCP. This would send only Auth Proxy headers, no bearer token.

## Canonical docs

Use `docs/oauth2-auth-proxy-setup.md` as the main OAuth2 documentation reference.
