# OAuth2 + Auth Proxy Integration Guide

This guide explains how to integrate the MCP Grafana server with OAuth2 for user authentication and Grafana's Auth Proxy for user impersonation.

## Overview

The integration allows the MCP server to:
1. **Receive** OAuth2 bearer tokens from clients
2. **Validate** tokens against an OAuth2 provider
3. **Extract** user information (username, email, groups, roles) from OAuth2
4. **Relay** user identity to Grafana via Auth Proxy headers
5. **Execute** API calls with the authenticated user's permissions

This enables:
- Per-user authorization and audit trails
- LDAP group/role synchronization through OAuth2
- Secure impersonation of authenticated users
- Compliance with corporate authentication standards

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│ OAuth2-authenticated Client                                     │
│ (e.g., browser, application with OAuth2 library)                │
└──────────────────────────────────────────────────────────────────┘
         │
         │ 1. Obtains OAuth2 bearer token from provider
         │ 2. Calls MCP Server with: Authorization: Bearer {token}
         ▼
┌──────────────────────────────────────────────────────────────────┐
│ MCP Server (mcp-grafana)                                         │
│                                                                  │
│ ExtractGrafanaInfoFromHeaders():                                 │
│   ├─ Extracts bearer token from Authorization header             │
│   ├─ Validates token against OAuth2 provider                     │
│   └─ Extracts user info: username, email, groups, roles          │
│                                                                  │
│ NewGrafanaClient() + AuthProxyRoundTripper():                    │
│   ├─ Adds X-WEBAUTH-USER: {username} header                      │
│   ├─ Adds X-WEBAUTH-EMAIL: {email} header                        │
│   ├─ Adds X-WEBAUTH-ROLE: {roles} header                         │
│   └─ Makes API calls to Grafana with service account token       │
└──────────────────────────────────────────────────────────────────┘
         │
         │ API call with Auth Proxy headers + service account auth
         ▼
┌──────────────────────────────────────────────────────────────────┐
│ Grafana Instance (with Auth Proxy enabled)                       │
│                                                                  │
│ auth.proxy intercepts headers:                                   │
│   ├─ Syncs user with X-WEBAUTH-USER                              │
│   ├─ Creates/updates user in Grafana database                    │
│   ├─ Assigns roles from X-WEBAUTH-ROLE                           │
│   └─ Executes request as authenticated user                      │
└──────────────────────────────────────────────────────────────────┘
```

## Prerequisites

1. **Grafana Instance** (v8.0+)
   - Service account with Admin or Editor role for MCP server
   - Auth Proxy configured in grafana.ini

2. **OAuth2 Provider** (e.g., Keycloak, Okta, Auth0)
   - LDAP backend (or equivalent user directory)
   - OAuth2/OpenID Connect endpoints exposed
   - User info endpoint returning: sub, preferred_username, email, groups

3. **MCP Server Host**
   - Network access to Grafana
   - Network access to OAuth2 provider

## Grafana Configuration

### 1. Enable Auth Proxy Mode

Edit `grafana.ini`:

```ini
[auth]
# Disable OAuth2 in Grafana if using external OAuth2 provider
oauth_auto_login = false

[auth.proxy]
# Enable auth proxy mode
enabled = true

# Header to read username from (case-insensitive)
header_name = X-WEBAUTH-USER

# What property to use as the user identifier
# Options: "username", "name", "email", "login"
header_property = username

# Automatically create users in Grafana if not exists
auto_sign_up = true

# Sync email from X-WEBAUTH-EMAIL header
enable_email_tracking = true

#  Sync display name from X-WEBAUTH-NAME header
# This is optional - requires additional configuration
```

### 2. Create Service Account Token

```bash
# Create a Service Account with Admin role for MCP server
# In Grafana UI: Admin → Service Accounts → Create Service Account → Add Token

# Or via API:
curl -X POST http://localhost:3000/api/serviceaccounts \
  -H "Authorization: Bearer {admin_token}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "mcp-server",
    "role": "Admin"
  }'

# Create token for the service account
curl -X POST http://localhost:3000/api/serviceaccounts/{service_account_id}/tokens \
  -H "Authorization: Bearer {admin_token}" \
  -H "Content-Type: application/json" \
  -d '{"name": "MCP Token"}'
```

### 3. Optional: Configure Role Mapping

If you want to sync LDAP groups to Grafana roles:

```ini
[auth.proxy]
# Enable group sync
enable_auto_sync_roles = true

# Group header name
groups_header_name = X-WEBAUTH-ROLE

# Map groups to  roles (optional)
[auth.proxy.groups]
# Format: LDAP_Group = Grafana_Role
ldap-admins = Admin
ldap-editors = Editor
ldap-viewers = Viewer
```

## MCP Server Configuration

### Environment Variables

```bash
# OAuth2 Configuration
OAUTH2_ENABLED=true
OAUTH2_PROVIDER_URL=http://keycloak:8080/auth/realms/master
OAUTH2_CLIENT_ID=mcp-server
OAUTH2_CLIENT_SECRET=your_client_secret
OAUTH2_TOKEN_ENDPOINT=/protocol/openid-connect/token
OAUTH2_USERINFO_ENDPOINT=/protocol/openid-connect/userinfo
OAUTH2_TOKEN_CACHE_TTL=300  # Cache validated tokens for 5 minutes

# Grafana Configuration (existing)
GRAFANA_URL=http://grafana:3000
GRAFANA_SERVICE_ACCOUNT_TOKEN=your_service_account_token

# Auth Proxy Configuration
GRAFANA_PROXY_AUTH_ENABLED=true
GRAFANA_PROXY_USER_HEADER=X-WEBAUTH-USER
GRAFANA_PROXY_EMAIL_HEADER=X-WEBAUTH-EMAIL
GRAFANA_PROXY_NAME_HEADER=X-WEBAUTH-NAME
GRAFANA_PROXY_ROLE_HEADER=X-WEBAUTH-ROLE
```

### Docker Compose Example

```yaml
version: '3.8'

services:
  mcp-server:
    image: grafana/mcp-grafana:latest
    ports:
      - "8080:8080"
    environment:
      # OAuth2
      OAUTH2_ENABLED: "true"
      OAUTH2_PROVIDER_URL: "http://keycloak:8080/auth/realms/master"
      OAUTH2_CLIENT_ID: "mcp-server"
      OAUTH2_CLIENT_SECRET: "${OAUTH2_CLIENT_SECRET}"
      OAUTH2_TOKEN_ENDPOINT: "/protocol/openid-connect/token"
      OAUTH2_USERINFO_ENDPOINT: "/protocol/openid-connect/userinfo"
      
      # Grafana
      GRAFANA_URL: "http://grafana:3000"
      GRAFANA_SERVICE_ACCOUNT_TOKEN: "${GRAFANA_SERVICE_ACCOUNT_TOKEN}"
      
      # Auth Proxy
      GRAFANA_PROXY_AUTH_ENABLED: "true"
    depends_on:
      - grafana
      - keycloak

  grafana:
    image: grafana/grafana:latest
    ports:
      - "3000:3000"
    environment:
      GF_AUTH_PROXY_ENABLED: "true"
      GF_AUTH_PROXY_HEADER_NAME: "X-WEBAUTH-USER"
    volumes:
      - ./grafana.ini:/etc/grafana/grafana.ini

  keycloak:
    image: quay.io/keycloak/keycloak:latest
    ports:
      - "8081:8080"
    environment:
      KEYCLOAK_ADMIN: "admin"
      KEYCLOAK_ADMIN_PASSWORD: "admin"
```

### Configuration in Code

If using stdio transport:

```go
package main

import (
	"context"
	"os"
	"github.com/grafana/mcp-grafana"
)

func main() {
	ctx := context.Background()
	
	// OAuth2 configuration is automatically loaded from environment
	ctx = mcpgrafana.ExtractGrafanaInfoFromEnv(ctx)
	
	// GrafanaConfig now contains OAuth2 settings
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	
	// Create clients as usual - Auth Proxy headers will be added automatically
	client := mcpgrafana.NewGrafanaClient(ctx, config.URL, config.APIKey, nil)
	// ...
}
```

##  Keycloak Setup Example

### 1. Create Realm and LDAP Federation

1. Login to Keycloak admin console
2. Create new realm (e.g., "Grafana")
3. Add LDAP provider federation:
   ```
   Provider: ldap
   User Federation → Add Provider → LDAP → Configure
   
   LDAP Connection Settings:
   - Vendor: Active Directory (or OpenLDAP)
   - Connection URL: ldap://ldap-server:389
   - Bind DN: cn=admin,dc=example,dc=com
   - Bind Credentials: {password}
   
   LDAP Searching and Updating:
   - Users DN: ou=users,dc=example,dc=com
   - Username LDAP attribute: sAMAccountName (or uid)
   - Email LDAP attribute: mail
   - Full name LDAP attribute: displayName
   
   Sync Settings:
   - Import Users: ON
   - Periodic Full Sync: ON
   - Periodic Changed Users Sync: ON
   ```

### 2. Create OAuth2 Client for MCP Server

1. Clients → Create client
   ```
   Client ID: mcp-server
   Protocol: openid-connect
   Client Type: Confidential
   ```

2. Settings tab:
   ```
   Client authentication: ON
   Authorization: Standard flow disabled
   Service accounts roles: ON (for server-to-server authentication)
   ```

3. Credentials tab:
   ```
   Client secret: (copy this value for OAUTH2_CLIENT_SECRET)
   ```

4. Service Account Roles:
   ```
   Assign: realm-admin role (so it can list users for groups)
   ```

### 3. Configure Group Mapping

1. Realm Roles → Create Role
   ```
   Role Name: admin
   Role Name: editor
   Role Name: viewer
   ```

2. Users → (select LDAP user) → Realm Roles → Assign
   (Map LDAP groups to Realm roles)

### 4. Create Test OAuth2 Client (for client testing)

1. Clients → Create client  
   ```
   Client ID: grafana-ui
   Protocol: openid-connect
   ```

2. Settings:
   ```
   Client Type: Public
   Valid Redirect URIs: http://localhost:3000/*
   ```

## Testing

### 1. Get OAuth2 Token (from Keycloak)

```bash
# Using client credentials (service account)
curl -X POST http://keycloak:8080/auth/realms/master/protocol/openid-connect/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "client_id=mcp-server" \
  -d "client_secret=your_client_secret" \
  -d "grant_type=client_credentials"

# Response:
{
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "expires_in": 300,
  "refresh_expires_in": 0,
  "token_type": "Bearer"
}
```

### 2. Call MCP Server with Token

```bash
curl -X POST http://mcp-server:8080/tools/admin/get_organization \
  -H "Authorization: Bearer {access_token}" \
  -H "Content-Type: application/json" \
  -d '{"orgName": "Main"}' 

# MCP Server will:
# 1. Extract token from Authorization header
# 2. Validate against Keycloak
# 3. Extract user info (username, email, groups)
# 4. Add X-WEBAUTH-USER header
# 5. Call Grafana API with Auth Proxy headers
```

### 3. Verify User Created in Grafana

```bash
# Check if user was synced to Grafana
curl -X GET http://graf ana:3000/api/users/lookup?loginOrEmail=john.doe@example.com \
  -H "Authorization: Bearer {grafana_service_account_token}"

# Response:
{
  "id": 2,
  "email": "john.doe@example.com",
  "name": "John Doe",
  "login": "john.doe",
  "theme": "dark",
  "orgId": 1,
  "isGrafanaAdmin": false,
  "isDisabled": false,
  "lastSeenAt": "2025-01-15T10:30:00Z",
  "lastSeenAtAge": "34 seconds"
}
```

### 4. Verify Audit Log

```bash
# Check Grafana audit log to see API calls attributed to user
curl -X GET 'http://grafana:3000/api/audit/logs' \
  -H "Authorization: Bearer {grafana_service_account_token}" \
  -G --data-urlencode 'limit=100'

# Should show entries like:
# "userId": 2, "userName": "john.doe", "action": "...", "resourceType": "...", "timestamp": "..."
```

## Troubleshooting

### OAuth2 Token Validation Fails

**Error**: `OAuth2 token validation failed: failed to validate token: ...`

**Solutions**:
1. Verify OAuth2 provider is accessible from MCP server
   ```bash
   curl -I http://keycloak:8080/auth/realms/master/protocol/openid-connect/userinfo
   ```

2. Check token is valid and not expired
   ```bash
   # Decode JWT (install jwt-cli or use jwt.io)
   jwt decode {token}
   ```

3. Verify OAUTH2_USERINFO_ENDPOINT is set correctly
   ```bash
   curl http://keycloak:8080/auth/realms/master/.well-known/openid-configuration \
     | jq '.userinfo_endpoint'
   ```

### Auth Proxy Headers Not Received by Grafana

**Error**: Grafana is creating multiple user accounts or not recognizing X-WEBAUTH-USER

**Solutions**:
1. Verify Auth Proxy is enabled in grafana.ini
   ```bash
   curl http://grafana:3000/api/org -H "X-WEBAUTH-USER: test_user" \
     -H "Authorization: Bearer {token}"
   ```

2. Check Grafana logs for Auth Proxy errors
   ```bash
   docker logs grafana | grep -i auth
   ```

3. Ensure GRAFANA_PROXY_AUTH_ENABLED=true in environment

### Service Account Token Invalid

**Error**: `Unauthorized` when MCP server calls Grafana API

**Solutions**:
1. Verify service account token is valid
   ```bash
   curl http://grafana:3000/api/org \
     -H "Authorization: Bearer {service_account_token}"
   ```

2. Check service account still has admin role
   ```bash
   curl http://grafana:3000/api/serviceaccounts \
     -H "Authorization: Bearer {token}" | jq '.[] | select(.name=="mcp-server")'
   ```

3. Regenerate token if expired
   ```bash
   curl -X POST http://grafana:3000/api/serviceaccounts/{id}/tokens \
     -H "Authorization: Bearer {admin_token}" \
     -d '{"name":"MCP Token"}'
   ```

## Security Considerations

1. **HTTPS Only**: Use HTTPS for all communication
   - Protect OAuth2 tokens in transit
   - Enable certificate verification

2. **Token Time-to-Live (TTL)**:
   - Default: 300 seconds (5 minutes) for cached tokens
   - Shorter for high-security environments
   - Longer for better performance

3. **Service Account Token**:
   - Store securely (use secrets management)
   - Rotate regularly
   - Use minimal required permissions (Editor or Admin)

4. **Auth Proxy Headers**:
   - Only trusted sources should be able to set these headers
   - Consider using reverse proxy to add headers
   - Never trust user-supplied headers on public internet

5. **OAuth2 Client Secret**:
   - Never commit to version control
   - Use environment variables or secrets management
   - Rotate regularly

## Performance Optimization

1. **Token Caching**:
   - Default: 300 seconds
   - Adjust OAUTH2_TOKEN_CACHE_TTL based on needs
   - Reduces OAuth2 provider load

2. **Connection Pooling**:
   - OAuth2 client reuses HTTP connections
   - Configure timeouts appropriately

3. **User Sync**:
   - Auth Proxy auto-creates users on first request
   - Subsequent requests are faster (no sync needed)

## References

- [Grafana Auth Proxy Documentation](https://grafana.com/docs/grafana/latest/administration/configuration/#auth-proxy)
- [Keycloak LDAP Federation](https://www.keycloak.org/docs/latest/server_admin/index.html#_ldap)
- [OpenID Connect UserInfo Endpoint](https://openid.net/specs/openid-connect-core-1_0.html#UserInfo)
- [OAuth2 RFC 6749](https://tools.ietf.org/html/rfc6749)
