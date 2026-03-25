# OAuth2 Testing Setup Guide

This directory contains all the configuration and scripts needed to test OAuth2 token validation and Auth Proxy integration with a local Keycloak instance and Grafana.

## Files Overview

### Configuration Files

| File | Purpose |
|------|---------|
| `keycloak-realm.json` | Keycloak realm configuration (users, groups, OAuth2 clients) |
| `../env.oauth2-test` | Environment variables for MCP server (auto-populated by setup script) |

### Setup Scripts

| Script | Purpose | Runtime |
|--------|---------|---------|
| `oauth2-setup.sh` | Initialize Docker services, get secrets, create service accounts | 30-60s |
| `oauth2-test.sh` | Test utilities: get tokens, run flows, troubleshoot | On-demand |

## Quick Start

### Prerequisites
- Docker & Docker Compose
- curl, jq
- Go 1.21+

### 1. Initialize Environment (One Time)

```bash
# Make script executable
chmod +x testdata/oauth2-setup.sh testdata/oauth2-test.sh

# Run setup - starts services and configures everything
cd /workspaces/mcp-grafana
./testdata/oauth2-setup.sh
```

This script will:
- Start Keycloak (port 8082)
- Start Grafana with Auth Proxy (port 3001)
- Retrieve OAuth2 client secret from Keycloak
- Create Grafana service account
- Update `.env.oauth2-test` with all credentials

### 2. Start MCP Server

```bash
# Source environment
source .env.oauth2-test

# Start server in SSE mode with OAuth2 enabled
go run ./cmd/mcp-grafana/main.go
```

Expected output:
```
OAuth2 authentication enabled
Provider: http://keycloak:8080/auth/realms/mcp-grafana
Auth Proxy to Grafana: http://grafana-oauth:3000
Listening on HTTP with SSE transport...
Starting MCP server on port 8080
```

### 3. Test the OAuth2 Flow

In another terminal:

```bash
# Run built-in test flow
./testdata/oauth2-test.sh test-flow john.doe password123
```

## Service Credentials

### Keycloak (OAuth2 Provider)

```
URL:      http://localhost:8082
Admin:    admin / admin123
Realm:    mcp-grafana
Console:  http://localhost:8082/admin/
```

### Test Users (in mcp-grafana realm)

```
john.doe       password123  (editor)
jane.smith     password123  (viewer)
admin          admin123     (admin)
```

### Grafana (with Auth Proxy)

```
URL:               http://localhost:3001
Admin Credentials: admin / admin
Auth Proxy:        Enabled
Auto Sign-up:      Enabled
```

### MCP Server

```
URL:      http://localhost:8080
Mode:     SSE (Server-Sent Events)
Auth:     OAuth2 bearer tokens required
```

## Test Scenarios

### Get OAuth2 Token

```bash
# User token (interactive client)
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)
echo $TOKEN

# Service account token (client credentials)
CLIENT_TOKEN=$(./testdata/oauth2-test.sh client-token)
echo $CLIENT_TOKEN
```

### Call MCP with Token

```bash
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)

# Get MCP tools
curl -H "Authorization: Bearer $TOKEN" \
     http://localhost:8080/tools | jq .

# Get MCP resources
curl -H "Authorization: Bearer $TOKEN" \
     http://localhost:8080/resources | jq .
```

### Verify Auth Proxy User Creation

```bash
# Get Grafana token
GRAFANA_TOKEN=$(grep GRAFANA_SERVICE_ACCOUNT_TOKEN .env.oauth2-test | cut -d= -f2)

# Check if user was created in Grafana
curl -s "http://localhost:3001/api/users?loginOrEmail=john.doe" \
     -H "Authorization: Bearer $GRAFANA_TOKEN" | jq '.[] | {id, login, email, name}'
```

## Keycloak Realm Configuration

The `keycloak-realm.json` includes:

### Users
- **admin**: Admin role, part of ldap-admins group
- **john.doe**: Editor role, part of ldap-editors group  
- **jane.smith**: Viewer role, part of ldap-users group

### Groups
- **ldap-admins**: Has admin role
- **ldap-editors**: Has editor role
- **ldap-users**: Has viewer role

### OAuth2 Clients
- **mcp-server**: Confidential client for service-to-service (client credentials flow)
- **grafana-ui**: Public client for browser/UI access

### Protocol Mappers
- Groups claim (SAML/OIDC): User groups included in token
- preferred_username: User's login name
- email: User's email
- name: User's display name

## How It Works

```
┌─────────────────────────────────────────────────────────────┐
│ Client (e.g., CLI, UI, MCP Client)                         │
└──────────────────┬──────────────────────────────────────────┘
                   │ 1. Get OAuth2 token
                   │    (username/password or client credentials)
                   ▼
         ┌─────────────────────┐
         │     Keycloak        │
         │  OAuth2 Provider    │
         │ (port 8082)         │
         └──────────┬──────────┘
                    │ 2. Return access token
                    │    (JWT with user info, groups, roles)
                    ▼
         ┌─────────────────────────────────────────┐
         │  MCP Server (port 8080)                 │
         │  - Validates token against Keycloak     │
         │  - Caches token (5 min TTL)             │
         │  - Extracts user info (username, groups)│
         └─────────────┬───────────────────────────┘
                       │ 3. Adds Auth Proxy headers
                       │    X-WEBAUTH-USER: john.doe
                       │    X-WEBAUTH-EMAIL: john.doe@...
                       │    X-WEBAUTH-ROLE: editor
                       ▼
         ┌─────────────────────────────────────────┐
         │  Grafana (port 3001)                    │
         │  Auth Proxy Enabled                     │
         │  - Trusts Auth Proxy headers            │
         │  - Creates/updates user \"john.doe\"     │
         │  - Applies user's role (editor)         │
         └─────────────────────────────────────────┘
```

## Debugging

### Check Service Health

```bash
# Keycloak
curl http://localhost:8082/health/ready

# Grafana
curl http://localhost:3001/health

# MCP (requires token)
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/tools
```

### View Service Logs

```bash
# Keycloak logs
docker logs mcp-grafana-keycloak-1

# Grafana logs
docker logs mcp-grafana-grafana-oauth-1

# MCP logs (in terminal where running)
# Look for: "OAuth2", "Token cache", "Auth Proxy", "Setting headers"
```

### Token Validation Issues

```bash
# Decode token to see claims
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)
echo "$TOKEN" | cut -d. -f2 | base64 -d | jq .

# Check token endpoint directly
curl -X POST "http://localhost:8082/auth/realms/mcp-grafana/protocol/openid-connect/token" \
  -d "client_id=mcp-server" \
  -d "client_secret=<secret>" \
  -d "grant_type=client_credentials" | jq .
```

### Auth Proxy Not Working

```bash
# Check Grafana's auth proxy config
GRAFANA_TOKEN=$(grep GRAFANA_SERVICE_ACCOUNT_TOKEN .env.oauth2-test | cut -d= -f2)
curl -s "http://localhost:3001/api/admin/settings" \
     -H "Authorization: Bearer $GRAFANA_TOKEN" | jq '.auth'

# Should show:
# {
#   "auth": {
#     "proxy_enabled": true,
#     "proxy_header_name": "X-WEBAUTH-USER",
#     ...
#   }
# }
```

## Files Reference

### oauth2-setup.sh

Main setup script that:
1. Starts Docker Compose services
2. Waits for Keycloak and Grafana to be healthy
3. Authenticates with Keycloak admin
4. Retrieves mcp-server OAuth2 client secret
5. Creates Grafana service account and token
6. Updates `.env.oauth2-test` with credentials

**Variables retrieved**:
- `OAUTH2_CLIENT_SECRET`: From Keycloak mcp-server client
- `GRAFANA_SERVICE_ACCOUNT_TOKEN`: From Grafana service account API

### oauth2-test.sh

Testing utility script with commands:

| Command | Purpose |
|---------|---------|
| `token <user> [password]` | Get user OAuth2 token |
| `client-token` | Get service account token |
| `get-users [token]` | List Grafana users |
| `test-flow <user> [password]` | Complete flow test |
| `cleanup` | Stop docker-compose |

### keycloak-realm.json

Pre-configured Keycloak realm (`mcp-grafana`) with:
- 3 test users with different roles
- 3 groups with LDAP-like naming
- 2 OAuth2 clients (mcp-server, grafana-ui)
- Protocol mappers for claims
- Role mappings

Value of this approach:
- Reproducible test environment
- Easy onboarding for new developers
- Matches production LDAP scenario
- Can be version controlled
- Rapid iteration on OAuth2 flows

## Cleanup

```bash
# Stop all services
./testdata/oauth2-test.sh cleanup

# Or manually
docker-compose down
```

## Next Steps After Local Testing

1. **Verify complete flow**:
   - Token validation ✓
   - User group extraction ✓
   - Auth Proxy headers ✓
   - User sync to Grafana ✓

2. **Test with production Keycloak**:
   - Update `OAUTH2_PROVIDER_URL` to production instance
   - Verify same groups/roles work
   - Test LDAP federation if configured

3. **Deploy to staging**:
   - Set environment variables in deployment
   - Use production Keycloak realm
   - Enable audit logging in Grafana

4. **Monitor and maintain**:
   - Token validation times
   - Cache hit rates
   - Auth Proxy user sync errors
   - Audit trail of API calls

## Additional Resources

- [Keycloak OAuth2/OIDC Documentation](https://www.keycloak.org/)
- [Grafana Auth Proxy Documentation](https://grafana.com/docs/grafana/latest/auth/auth-proxy/)
- [MCP Protocol Specification](../../README.md)
- Main testing docs: [docs/OAUTH2_LOCAL_TESTING.md](../docs/OAUTH2_LOCAL_TESTING.md)
- Quick reference: [docs/OAUTH2_QUICK_REFERENCE.md](../docs/OAUTH2_QUICK_REFERENCE.md)
