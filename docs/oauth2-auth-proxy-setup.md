# OAuth2 + Auth Proxy Setup

This is the single reference for running MCP Grafana with OAuth2 token validation and Grafana Auth Proxy user impersonation.

## What this flow does

- Client sends `Authorization: Bearer <user-token>` to MCP.
- MCP validates the user token by calling the OAuth2 `userinfo` endpoint (the response also includes user identity fields).
- MCP forwards the validated user token to Grafana for use in downstream API calls (enabled by default).
- Optionally, when `GRAFANA_PROXY_AUTH_ENABLED=true`, MCP forwards user identity (username, email, groups) to Grafana via Auth Proxy headers (`X-WEBAUTH-*`).

## How it works: Step-by-step

1. **Client authenticates with OAuth2 provider** (e.g., Keycloak)
   - Client obtains a bearer token from the OAuth2 provider

2. **Client sends token to MCP in Authorization header**
   ```
   Authorization: Bearer <user-token>
   ```

3. **MCP validates token**
   - Calls OAuth2 provider's userinfo endpoint with the token
   - The userinfo response also returns user fields (`preferred_username`, `email`, `name`, `groups`, `roles`)
   - Caches validation result for `OAUTH2_TOKEN_CACHE_TTL` seconds
   - Returns 401 if validation fails

4. **MCP forwards validated token to Grafana** (enabled by default)
   - **Bearer mode** (default): Forwards `Authorization: Bearer <user-token>`
   - **Cloud mode** (when `OAUTH2_TOKEN_FORWARD_TO_GRAFANA_USE_CLOUD_HEADERS=true`):
     - Forwards `X-Access-Token: <grafana-service-token>` (from `GRAFANA_API_KEY`)
     - Forwards `X-Grafana-Id: <user-token>`

5. **MCP optionally forwards user identity to Grafana** (only when `GRAFANA_PROXY_AUTH_ENABLED=true`)
   - Sends Auth Proxy headers using fields from the userinfo response: `X-WEBAUTH-USER`, `X-WEBAUTH-EMAIL`, etc.
   - Grafana creates/updates user session based on these headers

## Authentication flow diagram

```
┌─────────┐          ┌──────────────┐          ┌───────────┐          ┌─────────┐
│ Client  │          │  MCP Server  │          │  OAuth2   │          │ Grafana │
│         │          │              │          │ Provider  │          │         │
└────┬────┘          └──────┬───────┘          └─────┬─────┘          └────┬────┘
     │                      │                        │                      │
     │  get token from provider (out of scope)       │                      │
     │◄─────────────────────────────────────────────►│                      │
     │                      │                        │                      │
     │ Authorization: Bearer <token>                 │                      │
     ├─────────────────────►│                        │                      │
     │                      │ validate token (userinfo endpoint)            │
     │                      ├───────────────────────►│                      │
     │                      │◄───────────────────────┤                      │
     │                      │ user info: username, email, groups            │
     │                      │ (cached for TTL seconds)                       │
     │                      │                        │                      │
     │                      │ Authorization: Bearer <token> (default)        │
     │                      │ [X-WEBAUTH-USER, X-WEBAUTH-EMAIL if proxy on] │
     │                      ├──────────────────────────────────────────────►│
     │                      │                        │                      │
     │                      │◄──────────────────────────────────────────────┤
     │                      │ (tools/dashboards/metrics response)           │
     │◄─────────────────────┤                        │                      │
```

## Minimal environment

### OAuth2 Settings (MCP) - Required

```bash
OAUTH2_ENABLED=true
OAUTH2_PROVIDER_URL=http://localhost:8082/auth/realms/mcp-grafana
OAUTH2_USERINFO_ENDPOINT=/protocol/openid-connect/userinfo
OAUTH2_TOKEN_CACHE_TTL=300
```

With these settings alone, MCP will:
- ✅ Validate incoming user tokens against the OAuth2 provider
- ✅ Forward validated user tokens to Grafana for downstream API calls (bearer mode by default)
- ✅ Extract user identity (username, email, groups) for use in tools and logging

### Token Forwarding to Grafana (Optional)

Token forwarding is **enabled by default** when OAuth2 is enabled. You can customize the forwarding mode:

**Bearer token forwarding (default, self-hosted Grafana)**
```bash
# Default behavior - no env vars needed. MCP forwards: Authorization: Bearer <user-token>
```

**Cloud-header forwarding (Grafana Cloud)**
```bash
OAUTH2_TOKEN_FORWARD_TO_GRAFANA_USE_CLOUD_HEADERS=true
# Requires GRAFANA_API_KEY for the service token
GRAFANA_API_KEY=<grafana-service-account-token>
# MCP forwards: X-Access-Token: <service-token> and X-Grafana-Id: <user-token>
```

**Disable token forwarding (if not needed)**
```bash
OAUTH2_TOKEN_FORWARD_TO_GRAFANA_ENABLED=false
```

### Auth Proxy Configuration (Optional)

Auth Proxy is **disabled by default**. Enable it only if you want Grafana to also manage user sessions:

```bash
GRAFANA_PROXY_AUTH_ENABLED=true
GRAFANA_URL=http://localhost:3000

# Optional: customize Auth Proxy header names (defaults shown)
GRAFANA_PROXY_USER_HEADER=X-WEBAUTH-USER
GRAFANA_PROXY_EMAIL_HEADER=X-WEBAUTH-EMAIL
GRAFANA_PROXY_NAME_HEADER=X-WEBAUTH-NAME
GRAFANA_PROXY_ROLE_HEADER=X-WEBAUTH-ROLE
```

When enabled, MCP also sends Auth Proxy headers to Grafana:
- `X-WEBAUTH-USER`: Authenticated username
- `X-WEBAUTH-EMAIL`: User email address
- `X-WEBAUTH-NAME`: User full name
- `X-WEBAUTH-ROLE`: User role/groups

### Configuration Notes

- If `OAUTH2_ENABLED=true` and `OAUTH2_PROVIDER_URL` is empty, OAuth2 is disabled.
- Token forwarding is enabled automatically when OAuth2 is configured. Set `OAUTH2_TOKEN_FORWARD_TO_GRAFANA_ENABLED=false` to disable it.
- Auth Proxy is disabled by default and must be explicitly enabled via `GRAFANA_PROXY_AUTH_ENABLED=true`.
- MCP logs token forwarding configuration at startup in SSE and streamable-http modes (showing which forwarding mode is active).

## Token types and roles

The system uses different tokens for different purposes:

| Token | Source | Purpose | Default Enabled? | Used in Path |
|-------|--------|---------|------------------|--------------|
| **User Token** | OAuth2 provider (Keycloak) | Proves the client's identity to MCP | — | Client→MCP |
| **Forwarded User Token** | Validated by MCP from OAuth2 | Enables MCP to make API calls to Grafana on behalf of user | ✅ Yes | MCP→Grafana |
| **Service Token** | Grafana | Provides MCP's identity to Grafana for API access | Optional | MCP→Grafana (only in cloud-header mode) |

### Bearer mode token flow (default)
- ✅ **Enabled by default** when OAuth2 is configured
- Client sends user token to MCP
- MCP validates user token with OAuth2 provider
- MCP forwards user token to Grafana in `Authorization: Bearer <user-token>`
- Grafana authenticates requests as the end-user

### Cloud-header mode token flow (Grafana Cloud)
- ❌ Disabled by default (requires explicit opt-in: `OAUTH2_TOKEN_FORWARD_TO_GRAFANA_USE_CLOUD_HEADERS=true`)
- Client sends user token to MCP
- MCP validates user token with OAuth2 provider
- MCP forwards BOTH tokens to Grafana:
  - `X-Access-Token: <service-token>` (from `GRAFANA_API_KEY`, used for MCP's API identity)
  - `X-Grafana-Id: <user-token>` (for request context/impersonation)
- Grafana uses service token for API access, applies user context for RBAC

### Auth Proxy headers (optional)
- ❌ Disabled by default (requires explicit opt-in: `GRAFANA_PROXY_AUTH_ENABLED=true`)
- When enabled, MCP sends additional identity headers to Grafana for user session management
- Headers: `X-WEBAUTH-USER`, `X-WEBAUTH-EMAIL`, `X-WEBAUTH-NAME`, `X-WEBAUTH-ROLE`
- Use this for Grafana UI-based user impersonation and session management

## Local test setup (recommended)

### Prerequisites

Prepare local infrastructure and test environment file:

```bash
./testdata/oauth2-setup.sh
```

This script sets up:
- **Keycloak** at `http://localhost:8082` (OAuth2 provider)
- **Grafana** at `http://localhost:3000` (with auth proxy configured)

Default test users:
- `john.doe` / `password123` (editor role)
- `jane.smith` / `password123` (viewer role)
- `admin` / `admin123` (admin role)

### Starting MCP with OAuth2

1. Load test environment:
```bash
source testdata/.env.oauth2-test
```

2. Start MCP server:
```bash
go run ./cmd/mcp-grafana/main.go
```

On startup, you should see output similar to:
```
msg=Using Grafana configuration oauth2_enabled=true oauth2_token_forward_to_grafana_enabled=true oauth2_token_forward_use_cloud_headers=false
```

For SSE/HTTP modes, startup logs also include token forwarding configuration.

### Running end-to-end validation

Execute the test flow:

```bash
./testdata/oauth2-test.sh test-flow john.doe password123
```

This verifies:
- Token validation succeeds
- User identity extracted correctly
- Auth Proxy headers sent to Grafana
- Downstream requests use forwarded token (if enabled)

## Manual verification

### Step 1: Obtain a user token from OAuth2 provider

```bash
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)
echo $TOKEN
```

### Step 2: Call MCP with the user token

```bash
curl -s http://localhost:8000/tools \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### Step 3: Verify expected behavior

**MCP should:**
1. Validate the token against the userinfo endpoint
2. Extract user information (username, email, groups, etc.)
3. Log successful token validation

**Grafana calls from MCP should include:**
- `X-WEBAUTH-USER: john.doe` (or configured header name)
- `X-WEBAUTH-EMAIL: john.doe@example.com`
- `X-WEBAUTH-ROLE: editor` (if role extraction enabled)
- If token forwarding is enabled: `Authorization: Bearer $TOKEN` (bearer mode) or `X-Access-Token` + `X-Grafana-Id` headers (cloud mode)

## Grafana UI login via Keycloak

The local compose setup also enables Grafana Generic OAuth against Keycloak.

1. Open `http://localhost:3000/login`.
2. Click `Sign in with Keycloak`.
3. Use a test user from the realm (for example `john.doe` / `password123`).

## Troubleshooting

### Health checks

Verify local services are running:

```bash
# OAuth2 provider (Keycloak)
curl -s http://localhost:8082/health/ready | jq .

# Grafana
curl -s http://localhost:3000/api/health | jq .

# MCP server
curl -s http://localhost:8000/health 2>/dev/null || echo "MCP health endpoint not available"
```

### Common issues and solutions

| Issue | Cause | Solution |
|-------|-------|----------|
| **401 Unauthorized from MCP** | Token validation failed | Check provider URL, userinfo endpoint, and token validity |
| **OAuth2 appears disabled** | `OAUTH2_ENABLED=true` not set or provider URL empty | Verify env vars and restart MCP |
| **Grafana shows "No user" or guest** | Auth Proxy not enabled or headers not sent | Check `GRAFANA_PROXY_AUTH_ENABLED=true` and header names match Grafana config |
| **Token forwarding not working** | Token forwarding flags not set or service token missing | For cloud headers, ensure `GRAFANA_API_KEY` is set |
| **User roles/groups not applied** | Role extraction not configured | Ensure OAuth2 provider includes groups/roles in userinfo response |

### Debug mode

Enable debug logging in MCP to see detailed info about token validation and header processing:

```bash
export OTEL_LOG_LEVEL=debug
go run ./cmd/mcp-grafana/main.go
```

Look for:
- `OAuth2 token validated` - successful token validation
- `OAuth2 token validation failed` - token rejected
- `token forwarding to Grafana enabled` - startup log showing forwarding mode

## Understanding the logs

When MCP starts with OAuth2 enabled, you'll see startup logs like:

```
time=... level=INFO msg="Using Grafana configuration" 
  url=http://... 
  oauth2_enabled=true 
  oauth2_token_forward_to_grafana_enabled=true 
  oauth2_token_forward_use_cloud_headers=false 
  proxy_auth_enabled=true
```

This tells you:
- ✅ OAuth2 is enabled
- ✅ Token forwarding is enabled (requests to Grafana will include user token)
- ✅ Bearer mode is active (user token forwarded as `Authorization: Bearer <token>`)
- ✅ Auth Proxy is enabled (will send `X-WEBAUTH-*` headers to Grafana)

For each request with a user token, you should see:

```
time=... level=DEBUG msg="OAuth2 token validated" user=john.doe
```

If a request fails validation:

```
time=... level=WARN msg="OAuth2 token validation failed" error="failed to validate token: ..."
```

In cloud-header mode, the startup log would show:
```
oauth2_token_forward_use_cloud_headers=true
```

And requests would use `X-Access-Token` + `X-Grafana-Id` headers instead of bearer tokens.

## Related files

- Setup script: `testdata/oauth2-setup.sh`
- Test helper: `testdata/oauth2-test.sh`
- Token forwarding env: `testdata/.env.oauth2-forward-test`
- Auth Proxy env: `testdata/.env.oauth2-test`
