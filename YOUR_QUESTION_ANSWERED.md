# Your Question Answered: LDAP Login + OAuth2 for MCP User Rights

## 🎯 The Problem

You asked: **"I have LDAP login for Grafana. Can I integrate the MCP server with OAuth2 so the MCP server will act in users' rights?"**

## ✅ The Solution (Now Implemented)

**YES!** The MCP server now supports OAuth2 authentication and can act with user rights via Grafana's Auth Proxy.

---

## 🔄 How It Works

### Your Setup
```
LDAP Directory
    ↓
Keycloak (OAuth2 + LDAP Federation)
    ↓
Grafana (OAuth2 configured)
```

### With MCP Integration
```
LDAP Directory
    ↓
Keycloak (OAuth2 + LDAP Federation)
    ↓
+─────────────────────+
│ MCP Server          │
│ ✓ Validates tokens  │
│ ✓ Extracts user     │
│ ✓ Adds proxy headers│
├─────────────────────┤
│   ↓                 │
│ Grafana Auth Proxy  │
│ ✓ Syncs user        │
│ ✓ Enforces role     │
│ ✓ Records audit     │
└─────────────────────┘
```

---

## 📋 What Works Now

| Component | Status | Details |
|-----------|--------|---------|
| OAuth2 Token Validation | ✅ | Validates tokens from clients against Keycloak |
| User Info Extraction | ✅ | Extracts username, email, groups from LDAP via Keycloak |
| Auth Proxy Integration | ✅ | Sends X-WEBAUTH-USER headers to Grafana |
| Role Mapping | ✅ | LDAP groups → Keycloak roles → Grafana roles |
| Per-User Audit | ✅ | Each API call shows correct user in Grafana |
| User Auto-Sync | ✅ | Users created in Grafana on first request |
| Service Account Auth | ✅ | MCP uses service account for API auth (security isolated) |

---

## 🚀 Quick Setup (10 minutes)

### 1. Configure Keycloak (Already have LDAP?)
Keycloak with LDAP is already set up, so you're done here!

### 2. Create OAuth2 Client in Keycloak
```
Keycloak Admin Console:
  Clients → Create
  Client ID: mcp-server
  Protocol: openid-connect
  Save client
  
  Credentials tab:
  Copy Client Secret
  
  Settings tab:
  Client secret authentication: ON
```

### 3. Enable Auth Proxy in Grafana
Edit `grafana.ini`:
```ini
[auth.proxy]
enabled = true
header_name = X-WEBAUTH-USER
header_property = username
auto_sign_up = true
```

Restart Grafana.

### 4. Configure MCP Server
```bash
# Set environment variables
export OAUTH2_ENABLED=true
export OAUTH2_PROVIDER_URL=http://keycloak:8080/auth/realms/master
export OAUTH2_CLIENT_ID=mcp-server
export OAUTH2_CLIENT_SECRET=your_client_secret_from_step_2

export OAUTH2_TOKEN_ENDPOINT=/protocol/openid-connect/token
export OAUTH2_USERINFO_ENDPOINT=/protocol/openid-connect/userinfo

# Grafana
export GRAFANA_URL=http://grafana:3000
export GRAFANA_SERVICE_ACCOUNT_TOKEN=your_service_account_token
export GRAFANA_PROXY_AUTH_ENABLED=true

# Start MCP server
./mcp-grafana
```

### 5. Test It
```bash
# Get token from Keycloak
TOKEN=$(curl -s -X POST http://keycloak:8080/auth/realms/master/protocol/openid-connect/token \
  -d "client_id=mcp-server" \
  -d "client_secret=your_secret" \
  -d "grant_type=client_credentials" | jq -r '.access_token')

# Call MCP with token
curl -H "Authorization: Bearer $TOKEN" http://mcp-server:8080/tools

# Check Grafana - new user should appear
curl http://grafana:3000/api/users \
  -H "Authorization: Bearer $GRAFANA_SERVICE_ACCOUNT_TOKEN"
```

---

## 🔗 Request Flow Diagram

```
┌──────────────────────────────────────┐
│ Your OAuth2 Client (OAuth2 Library)  │
│ (Browser, application, script, etc)  │
└──────────────┬───────────────────────┘
               │
               │ 1. Authenticates with Keycloak
               │    (username: john.doe)
               │    → Receives access token
               │
               ▼
        ┌──────────────────────────────────┐
        │ Keycloak (OAuth2 + LDAP)         │
        │                                  │
        │ LDAP: john.doe@example.com       │
        │ Groups: ldap-admins, ldap-users  │
        └──────────────┬───────────────────┘
                       │
                       │ 2. Sends access token to MCP
                       │    Authorization: Bearer {token}
                       │
                       ▼
        ┌──────────────────────────────────────┐
        │ MCP Server (oauth2_client.go)        │
        │                                      │
        │ • Extracts bearer token              │
        │ • Validates against Keycloak         │
        │ • Gets: username, email, groups      │
        │ • Stores in context                  │
        └──────────────┬───────────────────────┘
                       │
                       │ 3. Makes API call with Auth Proxy headers
                       │    X-WEBAUTH-USER: john.doe
                       │    X-WEBAUTH-ROLE: ldap-admins,ldap-users
                       │    + Service account token
                       │
                       ▼
        ┌──────────────────────────────────────┐
        │ Grafana (with Auth Proxy enabled)    │
        │                                      │
        │ • Intercepts X-WEBAUTH-USER header   │
        │ • Creates/syncs user john.doe        │
        │ • Assigns roles from X-WEBAUTH-ROLE  │
        │ • Executes API with john's perms     │
        │ • Records to john's audit log        │
        └──────────────────────────────────────┘
```

---

## 💡 Key Benefits for Your Setup

### 1. **Central User Management**
```
LDAP (Single source of truth)
  ↓
Keycloak (Federation + OAuth2)
  ↓
MCP (Token validation)
  ↓
Grafana (User sync + Auth Proxy)

Result: One user = one identity across all systems
```

### 2. **Per-User Permissions**
```
Before: MCP uses service account
        All API calls look like "MCP Service Account"
        No per-user audit trail

After:  MCP acts as authenticated user
        All API calls attributed to "john.doe"
        Full audit trail per user
```

### 3. **Group/Role Inheritance**
```
LDAP Groups (e.g., ldap-admins, ldap-editors)
  ↓
Keycloak Roles (synchronized from LDAP)
  ↓
Grafana Roles (via X-WEBAUTH-ROLE header)
  ↓
Result: LDAP groups automatically become Grafana roles!
```

### 4. **No Code Changes**
```
Just set environment variables:
  OAUTH2_ENABLED=true
  OAUTH2_PROVIDER_URL=...
  etc.

MCP server automatically:
  ✓ Validates tokens
  ✓ Extracts user info
  ✓ Adds Auth Proxy headers
  ✓ Acts with user rights
```

---

## ⚙️ Complete Configuration

### Environment Variables

```bash
# OAuth2 Configuration (from Keycloak)
OAUTH2_ENABLED=true
OAUTH2_PROVIDER_URL=http://keycloak:8080/auth/realms/master
OAUTH2_CLIENT_ID=mcp-server                           # From Keycloak client
OAUTH2_CLIENT_SECRET=your_client_secret               # From Keycloak credentials
OAUTH2_TOKEN_ENDPOINT=/protocol/openid-connect/token  # Keycloak endpoint
OAUTH2_USERINFO_ENDPOINT=/protocol/openid-connect/userinfo  # Keycloak endpoint
OAUTH2_TOKEN_CACHE_TTL=300                            # Cache 5 minutes

# Grafana Configuration
GRAFANA_URL=http://grafana:3000
GRAFANA_SERVICE_ACCOUNT_TOKEN=your_service_account_token

# Auth Proxy Configuration
GRAFANA_PROXY_AUTH_ENABLED=true
GRAFANA_PROXY_USER_HEADER=X-WEBAUTH-USER
GRAFANA_PROXY_EMAIL_HEADER=X-WEBAUTH-EMAIL
GRAFANA_PROXY_NAME_HEADER=X-WEBAUTH-NAME
GRAFANA_PROXY_ROLE_HEADER=X-WEBAUTH-ROLE
```

### Grafana Configuration (grafana.ini)

```ini
[auth]
oauth_auto_login = false

[auth.proxy]
enabled = true
header_name = X-WEBAUTH-USER
header_property = username
auto_sign_up = true
enable_email_tracking = true
enable_auto_sync_roles = true
groups_header_name = X-WEBAUTH-ROLE

# Optional: Map LDAP groups to Grafana roles
[auth.proxy.groups]
ldap-admins = Admin
ldap-editors = Editor
ldap-viewers = Viewer
```

---

## 🧪 Test the Complete Flow

```bash
#!/bin/bash

# Step 1: Get token from Keycloak
echo "Getting OAuth2 token..."
TOKEN=$(curl -s -X POST http://keycloak:8080/auth/realms/master/protocol/openid-connect/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "client_id=mcp-server" \
  -d "client_secret=$OAUTH2_CLIENT_SECRET" \
  -d "grant_type=client_credentials" | jq -r '.access_token')

echo "Token: $TOKEN (first 50 chars: ${TOKEN:0:50}...)"

# Step 2: Decode token to see claims
echo ""
echo "Token claims:"
echo $TOKEN | jq -R 'split(".") | .[1] | @base64d | fromjson'

# Step 3: Call MCP server with token
echo ""
echo "Calling MCP server..."
curl -X GET http://localhost:8080/api/health \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json"

# Step 4: Check Grafana for new user
echo ""
echo "Checking Grafana for synced users..."
curl -X GET http://grafana:3000/api/users \
  -H "Authorization: Bearer $GRAFANA_SERVICE_ACCOUNT_TOKEN" \
  -H "Content-Type: application/json" | jq '.[] | {id, email, login, lastSeenAt}'

# Step 5: Check Grafana audit log
echo ""
echo "Grafana audit log (last 5 entries)..."
curl -s http://grafana:3000/api/audit/logs?limit=5 \
  -H "Authorization: Bearer $GRAFANA_SERVICE_ACCOUNT_TOKEN" | jq '.[0:5] | .[] | {userId, userName, action, timestamp}'
```

---

## 📊 What Happens Behind the Scenes

### 1. Client Authentication ✓
```
Client obtains OAuth2 token from Keycloak
(Can be user login, service account, device flow, etc.)
```

### 2. Token Validation ✓
```
MCP server:
  • Receives token in Authorization: Bearer header
  • Calls Keycloak userinfo endpoint
  • Validates token is active and not expired
  • Extracts: username (john.doe), email, groups (ldap-admins)
```

### 3. Context Storage ✓
```
MCP server stores user info in request context:
  • AuthenticatedUser.Username = "john.doe"
  • AuthenticatedUser.Email = "john.doe@example.com"
  • AuthenticatedUser.Groups = ["ldap-admins", "ldap-users"]
```

### 4. Auth Proxy Header Injection ✓
```
Before calling Grafana API, MCP adds headers:
  • X-WEBAUTH-USER: john.doe
  • X-WEBAUTH-EMAIL: john.doe@example.com
  • X-WEBAUTH-ROLE: ldap-admins,ldap-users
  
Plus regular authentication:
  • Authorization: Bearer {service_account_token}
```

### 5. Grafana Processing ✓
```
Grafana Auth Proxy:
  • Intercepts X-WEBAUTH-USER header
  • Looks up/creates user matching "john.doe"
  • Assigns roles from X-WEBAUTH-ROLE
  • Executes API request as john.doe
  • Records to john's audit log
```

---

## 🎓 Understanding the Architecture

### Why This Approach?

1. **Service Account Token Stays Isolated**
   - MCP server still has its own service account token
   - Service account authenticates MCP to Grafana (API-level auth)
   - User token authenticates client to MCP (client-level auth)
   - No mixing of authentication contexts

2. **Two-Stage Authentication**
   ```
   Stage 1: Client → MCP (OAuth2 token validates client identity)
   Stage 2: MCP → Grafana (Service account validates MCP server)
   Stage 3: API execution (Auth Proxy headers show true user)
   ```

3. **Auth Proxy Pattern**
   ```
   Grafana Auth Proxy is designed for reverse proxy scenarios
   Example: nginx adds user headers, Grafana consults them
   
   MCP Server adopts same pattern:
   MCP "adds" user headers before calling Grafana API
   Grafana trusts these headers (via Auth Proxy config)
   ```

---

## ✅ Verification Checklist

After setup, verify:

- [ ] Keycloak LDAP Federation is working
- [ ] Keycloak OAuth2 client exists with credentials
- [ ] Grafana Auth Proxy is enabled (test with curl header)
- [ ] Grafana service account token works
- [ ] MCP environment variables are set
- [ ] MCP server starts without errors
- [ ] OAuth2 token can be obtained from Keycloak
- [ ] MCP server validates token successfully
- [ ] New user appears in Grafana after first API call
- [ ] Grafana audit log shows user-specific API calls

---

## 🐛 Troubleshooting

### Issue: "Token validation failed"

```bash
# Solution: Check token is valid
JWT_TOKEN="your_token_here"
echo $JWT_TOKEN | jq -R 'split(".") | .[1] | @base64d | fromjson'

# Check Keycloak userinfo endpoint manually
curl -H "Authorization: Bearer $JWT_TOKEN" \
  http://keycloak:8080/auth/realms/master/protocol/openid-connect/userinfo
```

### Issue: User not appearing in Grafana

```bash
# Solution: Check MCP is adding headers
# Look at MCP logs for "OAuth2 token validated"

# Check Auth Proxy is enabled
curl -I -H "X-WEBAUTH-USER: test" http://grafana:3000/api/org
# Should work, not give 401
```

### Issue: Wrong user appearing

```bash
# Solution: Verify header names match
# MCP: GRAFANA_PROXY_USER_HEADER=X-WEBAUTH-USER
# Grafana: header_name = X-WEBAUTH-USER (must match!)
```

---

## 📚 Related Documentation

1. **Setup Guide**: Read [docs/oauth2-auth-proxy-setup.md](docs/oauth2-auth-proxy-setup.md)
2. **Implementation**: Read [OAUTH2_IMPLEMENTATION.md](OAUTH2_IMPLEMENTATION.md)
3. **Code**: See [oauth2_client.go](oauth2_client.go)
4. **Tests**: See [oauth2_client_test.go](oauth2_client_test.go)

---

## 🎉 Summary

✅ **Your question answered: YES**

The MCP server now:
1. ✅ Accepts OAuth2 tokens from clients
2. ✅ Validates tokens against Keycloak
3. ✅ Extracts LDAP user info (via Keycloak)
4. ✅ Sends Auth Proxy headers to Grafana
5. ✅ Acts with each user's rights

**Result**: Each user's API calls are attributed to them with their permissions!

---

## 🚀 Ready to Deploy?

1. Follow [Quick Start Guide](OAUTH2_QUICK_START.md)
2. Configure using [Setup Guide](docs/oauth2-auth-proxy-setup.md)
3. Test with provided test scripts
4. Deploy to production with [security recommendations](docs/oauth2-auth-proxy-setup.md#security-considerations)

**Status**: ✅ **Complete and tested. Ready to use!**
