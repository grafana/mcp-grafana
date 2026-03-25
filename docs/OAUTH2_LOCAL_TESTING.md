# OAuth2 + Auth Proxy Local Testing Guide

This guide walks through testing the OAuth2 token validation and Grafana Auth Proxy integration locally using Docker Compose, Keycloak, and the MCP Grafana Server.

## Architecture Overview

```
┌─────────────────┐        ┌──────────────────┐        ┌──────────────────┐
│   Keycloak      │        │   MCP Server     │        │     Grafana      │
│ OAuth2 Provider │◄──────►│  (Token Validate)│◄──────►│  (Auth Proxy)    │
│  (port 8082)    │        │   (port 8080)    │        │   (port 3001)    │
└─────────────────┘        └──────────────────┘        └──────────────────┘
     │                            │                           │
     │ Realm: mcp-grafana        │ Config: .env.oauth2-test  │ Trusts headers
     │ Users: 3                  │ Mode: SSE                 │ X-WEBAUTH-*
     │ Clients: 2                │ Transport: HTTP           │
```

## Prerequisites

- Docker and Docker Compose
- curl (for testing)
- jq (for JSON parsing)
- Go 1.21+ (for running MCP server)

## Quick Start (5 minutes)

### 1. Start Infrastructure

```bash
# Make setup script executable
chmod +x testdata/oauth2-setup.sh

# Run setup script (automatically starts services, gets secrets, creates service account)
./testdata/oauth2-setup.sh
```

The script will:
- Start Docker Compose services (Keycloak, Grafana with Auth Proxy)
- Wait for services to be healthy
- Retrieve OAuth2 client secret from Keycloak
- Create Grafana service account for MCP
- Generate `.env.oauth2-test` with all credentials

### 2. Check Configuration

```bash
cat .env.oauth2-test
```

Should contain:
```bash
OAUTH2_ENABLED=true
OAUTH2_PROVIDER_URL=http://localhost:8082/auth/realms/mcp-grafana
OAUTH2_CLIENT_ID=mcp-server
OAUTH2_CLIENT_SECRET=<actual-secret>
OAUTH2_USER_INFO_ENDPOINT=/protocol/openid-connect/userinfo
GRAFANA_URL=http://localhost:3000
GRAFANA_PROXY_AUTH_ENABLED=true
GRAFANA_SERVICE_ACCOUNT_TOKEN=<actual-token>
MCP_SERVER_MODE=sse
```

### 3. Start MCP Server in SSE Mode

```bash
# Source environment configuration
source .env.oauth2-test

# Start MCP server with OAuth2 enabled
go run ./cmd/mcp-grafana/main.go
```

Server should output:
```
OAuth2 authentication enabled
Provider: http://localhost:8082/auth/realms/mcp-grafana
Auth Proxy to Grafana: http://localhost:3000
Listening on HTTP with SSE transport...
```

### 4. Test OAuth2 Flow

In a new terminal:

```bash
# Make test script executable
chmod +x testdata/oauth2-test.sh

# Get OAuth2 token for test user
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)

# Test complete flow (token validation, Auth Proxy headers, user creation)
./testdata/oauth2-test.sh test-flow john.doe password123
```

## Test Scenarios

### Scenario 1: User Token Validation

**Objective**: Verify MCP validates OAuth2 tokens correctly

```bash
# Get token for test user
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)

# Call MCP health endpoint with token
curl -X GET "http://localhost:8080/tools" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Expected response: 200 OK with list of MCP tools
```

**Expected behavior**:
- MCP validates token against Keycloak
- User info extracted: username=john.doe, email=john.doe@example.com, groups=[ldap-editors]
- Token is cached for 5 minutes
- Subsequent calls with same token use cache

### Scenario 2: Invalid Token Rejection

**Objective**: Verify MCP rejects invalid tokens

```bash
# Use invalid/malformed token
curl -X GET "http://localhost:8080/tools" \
  -H "Authorization: Bearer invalid-token-123" | jq .

# Expected response: 401 Unauthorized
```

**Expected behavior**:
- MCP rejects with 401 status
- No Auth Proxy headers sent to Grafana
- Error message logged

### Scenario 3: Auth Proxy Header Injection

**Objective**: Verify Auth Proxy headers are correctly added to Grafana calls

```bash
# Get token
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)

# Call MCP with verbose output (internal logs would show headers)
# Check MCP server logs to see:
# INFO: Setting Auth Proxy headers for user john.doe
# X-WEBAUTH-USER: john.doe
# X-WEBAUTH-EMAIL: john.doe@example.com
# X-WEBAUTH-NAME: John Doe
# X-WEBAUTH-ROLE: editor
```

### Scenario 4: User Syncing to Grafana

**Objective**: Verify users are created in Grafana via Auth Proxy

```bash
# Get Grafana service account token
GRAFANA_TOKEN=$(grep GRAFANA_SERVICE_ACCOUNT_TOKEN .env.oauth2-test | cut -d= -f2)

# Call MCP with john.doe token (triggers Auth Proxy header injection)
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)
curl -X GET "http://localhost:8080/tools" \
  -H "Authorization: Bearer $TOKEN"

# Check if user was created in Grafana
curl -s "http://localhost:3001/api/users?loginOrEmail=john.doe" \
  -H "Authorization: Bearer $GRAFANA_TOKEN" | jq '.[0]'

# Expected response:
# {
#   "id": 2,
#   "login": "john.doe",
#   "email": "john.doe@example.com",
#   "name": "John Doe",
#   "isAdmin": false,
#   "isGrafanaAdmin": false,
#   "isDisabled": false,
#   "lastSeenAt": "2024-01-15T10:30:00Z",
#   "lastSeenAtUtc": "2024-01-15T10:30:00Z",
#   "authLabels": ["OAuth"]
# }
```

### Scenario 5: Group/Role Mapping

**Objective**: Verify user groups are extracted from OAuth2 token

```bash
# Test users with different groups:
# admin      → ldap-admins    → role: admin
# john.doe   → ldap-editors   → role: editor
# jane.smith → ldap-users     → role: viewer

# Get token for admin user
ADMIN_TOKEN=$(./testdata/oauth2-test.sh token admin admin123)

# MCP logs should show: groups=[admin,ldap-admins]

# Get token for john.doe
JOHN_TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)

# MCP logs should show: groups=[editor,ldap-editors]
```

### Scenario 6: Token Caching

**Objective**: Verify token caching reduces SSL/provider load

```bash
# Get token
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)

# Make rapid requests (observe in MCP server logs)
for i in {1..5}; do
  curl -s -X GET "http://localhost:8080/tools" \
    -H "Authorization: Bearer $TOKEN" > /dev/null
  echo "Request $i"
done

# Expected behavior:
# Request 1: "Cache miss, validating token..."
# Request 2-5: "Using cached token..." (no validation calls to Keycloak)
# After 300 seconds: Cache cleared, next request validates again
```

### Scenario 7: Token Expiry Handling

**Objective**: Verify expired tokens are rejected

```bash
# Get short-lived token (if available)
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)

# Wait for token to expire (or request new one)
sleep 65  # tokens valid for 60s in test

# Attempt call with expired token
curl -X GET "http://localhost:8080/tools" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Expected response: 401 Unauthorized
# Message: Token has expired
```

### Scenario 8: Client Credentials Flow (Service-to-Service)

**Objective**: Verify MCP can use client credentials for server-to-server calls

```bash
# Get token using client credentials
CLIENT_TOKEN=$(./testdata/oauth2-test.sh client-token)

# Use token to call MCP
curl -X GET "http://localhost:8080/tools" \
  -H "Authorization: Bearer $CLIENT_TOKEN" | jq .

# Expected response: 200 OK (service account has full access)
```

## Test Users and Credentials

### Keycloak Admin

- URL: http://localhost:8082
- Username: `admin`
- Password: `admin123`
- Realm: `master`

### Test Users (Realm: mcp-grafana)

| Username  | Password     | Groups / Roles        | Email                 |
|-----------|-------------|----------------------|----------------------|
| admin     | admin123    | ldap-admins (admin)   | admin@example.com    |
| john.doe  | password123 | ldap-editors (editor) | john.doe@example.com |
| jane.smith| password123 | ldap-users (viewer)   | jane.smith@example.com|

### Grafana Access

- URL (with Auth Proxy): http://localhost:3001
- Default Admin: admin / admin
- Auth Proxy Enabled: YES
- Auto sign-up: YES

## Debugging and Troubleshooting

### Service Won't Start

```bash
# Check if ports are already in use
lsof -i :8082  # Keycloak
lsof -i :3001  # Grafana
lsof -i :8080  # MCP

# If ports in use, stop existing containers
docker-compose down
docker-compose ps  # verify all stopped
```

### Token Validation Failures

```bash
# Check MCP logs for token validation errors
# Look for: "Failed to validate token", "Invalid token format", "Token expired"

# Verify Keycloak is reachable
curl -s http://localhost:8082/health/ready | jq .

# Verify token endpoint is accessible
curl -s -X POST "http://localhost:8082/auth/realms/mcp-grafana/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "client_id=mcp-server" \
  -d "client_secret=<secret>" \
  -d "grant_type=client_credentials" | jq .
```

### Grafana Auth Proxy Not Working

```bash
# Check Grafana logs
docker logs mcp-grafana-grafana-oauth-1

# Look for: "Auth proxy enabled", "Valid auth proxy header"

# Verify Auth Proxy is enabled
curl -s http://localhost:3001/api/admin/settings | jq '.auth.proxy_enabled'

# Should return: true
```

### User Not Showing in Grafana

```bash
# Make sure you have:
# 1. Called MCP API with valid OAuth2 token
# 2. Grafana Auth Proxy is enabled
# 3. Grafana service account created

# Check Grafana logs for Auth Proxy user creation
docker logs mcp-grafana-grafana-oauth-1 | grep -i "auth proxy"

# Check if user exists
GRAFANA_TOKEN=$(grep GRAFANA_SERVICE_ACCOUNT_TOKEN .env.oauth2-test | cut -d= -f2)
curl -s "http://localhost:3001/api/users" \
  -H "Authorization: Bearer $GRAFANA_TOKEN" | jq '.[] | select(.login == "john.doe")'
```

## Performance Testing

### Load Test

```bash
# Generate 100 requests with token caching
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)

time for i in {1..100}; do
  curl -s -X GET "http://localhost:8080/tools" \
    -H "Authorization: Bearer $TOKEN" > /dev/null
done

# First request: ~150-200ms (token validation call to Keycloak)
# Requests 2-100: ~5-10ms (from cache)
# Total time: should be < 2 seconds (mostly latency, cache is working)
```

### Cache Effectiveness

```bash
# Monitor MCP logs during load test
# Should see pattern like:
# Cache miss: validating token
# Cache hit: using cached user info
# Cache hit: using cached user info
# Cache hit: using cached user info
# ...
# After 5 minutes: Cache miss: validating token (refresh)
```

## Cleanup

```bash
# Stop and remove all containers
./testdata/oauth2-test.sh cleanup

# Or manually
docker-compose down

# Remove environment file if needed
rm .env.oauth2-test
```

## Environment Variables Reference

All OAuth2 configuration is via environment variables. Key ones:

| Variable | Purpose | Example |
|----------|---------|---------|
| `OAUTH2_ENABLED` | Enable OAuth2 validation | `true` |
| `OAUTH2_PROVIDER_URL` | OAuth2 provider URL | `http://localhost:8082/auth/realms/mcp-grafana` |
| `OAUTH2_CLIENT_ID` | Client ID for server | `mcp-server` |
| `OAUTH2_CLIENT_SECRET` | Client secret | `abc123...` |
| `GRAFANA_PROXY_AUTH_ENABLED` | Enable Auth Proxy headers | `true` |
| `GRAFANA_URL` | Grafana API base URL | `http://localhost:3000` |
| `GRAFANA_SERVICE_ACCOUNT_TOKEN` | Service account token | `eyJ...` |
| `MCP_SERVER_MODE` | Transport mode | `sse` or `stdio` |

## Next Steps

After verifying local testing:

1. **Deploy to staging**: Update environment variables, use production Keycloak realm
2. **Enable audit logging**: Monitor `~/.local/share/grafana/logs/grafana.log` for Auth Proxy events
3. **Set up LDAP federation**: Configure Keycloak LDAP user provider for real user directory
4. **Production secrets**: Use secret management (Vault, AWS Secrets Manager, etc.)
5. **Monitor and alert**: Set up alerts for token validation failures

## Additional Resources

- [Keycloak Admin Console](http://localhost:8082/admin/)
- [Grafana with Auth Proxy](http://localhost:3001)
- [MCP Protocol Specification](../../README.md)
- [OAuth2 Implementation Details](../OAUTH2_IMPLEMENTATION.md)
