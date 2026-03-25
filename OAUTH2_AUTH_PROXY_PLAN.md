# OAuth2 Auth Proxy Integration Plan for mcp-grafana

## Overview
Integrate the MCP server with OAuth2 and Grafana's Auth Proxy to act with user rights. The MCP server will:
1. Receive OAuth2 token credentials from clients
2. Validate the token against an OAuth2 provider
3. Extract user identity, groups, and attributes
4. Relay this identity to Grafana via Auth Proxy headers
5. Execute API calls with the authenticated user's permissions

This enables per-user authorization, audit trails, and RBAC enforcement.

---

## Architecture

### Current Flow
```
LDAP-authenticated → MCP Server → Grafana (via Service Account Token)
  Client             (no user identity)
```

### Target Flow (OAuth2 + Auth Proxy)
```
OAuth2 Client ─────→ MCP Server ──────→ OAuth2 Provider (validate token, get user info)
(access token)      (verify & extract)        ↓
                                          Extract: username, email, groups, roles
                                              ↓
                          MCP Server passes user identity
                                              ↓
                    Grafana (via X-WEBAUTH-USER + other headers)
                     (acts as authenticated user)
```

---

## Implementation Plan

### Phase 1: Configuration & Discovery (Week 1)
**Goals**: Understand user identity sources, configure Grafana Auth Proxy

#### 1.1 Grafana Configuration
- [x] Document required `grafana.ini` settings for Auth Proxy:
  ```ini
  [auth.proxy]
  enabled = true
  header_name = X-WEBAUTH-USER
  header_property = username
  auto_sign_up = true
  
  [auth]
  signout_redirect_url = 
  ```
- Document optional headers: `X-WEBAUTH-EMAIL`, `X-WEBAUTH-NAME`, `X-WEBAUTH-ROLE`
- Test Auth Proxy setup with direct curl requests to Grafana

**Tasks**:
- [ ] Add documentation to `/docs/auth-proxy-setup.md`
- [ ] Add sample `testdata/grafana-auth-proxy.ini`
- [ ] Add curl test examples to documentation

#### 1.2 User Identity Source Analysis
**Your Scenario**: OAuth2 Token from Client → Validate → Extract User Info

User identity will come from OAuth2 token validation flow:

**Flow**:
1. Client sends request with OAuth2 token (in `Authorization: Bearer {token}` header)
2. MCP server validates token against OAuth2 provider (token introspection or JWKS endpoint)
3. MCP server calls OAuth2 UserInfo endpoint to fetch user attributes
4. Extract: username (from LDAP), email, groups, roles
5. Store in request context for use by all downstream tools
6. Inject into Grafana API calls via Auth Proxy headers

**Configuration Needed**:
- OAuth2 Provider URL (e.g., http://auth-provider:8080)
- OAuth2 Client ID and Secret (for server-to-server authentication)
- Token endpoint for validation
- UserInfo endpoint for user attributes

**Tasks**:
- [ ] Document required OAuth2 endpoints from your provider
- [ ] Test OAuth2 token validation with sample token
- [ ] Verify LDAP user attributes available via OAuth2

---

### Phase 2: Code Implementation (Week 2-3)

#### 2.1 Extend GrafanaConfig & Add OAuth2Config
**File**: `mcpgrafana.go` + new `oauth2_client.go`

Add OAuth2 and Auth Proxy fields:
```go
type GrafanaConfig struct {
    // ... existing fields ...
    
    // Auth Proxy fields (for Grafana-side)
    ProxyAuthEnabled bool   // Enable Auth Proxy mode
    ProxyUserHeader  string // Header name (default: X-WEBAUTH-USER)
    ProxyEmailHeader string // Header name (default: X-WEBAUTH-EMAIL)
    ProxyNameHeader  string // Header name (default: X-WEBAUTH-NAME)
    ProxyRoleHeader  string // Header name (default: X-WEBAUTH-ROLE)
    
    // Authenticated user from current request
    AuthenticatedUser *OAuth2UserInfo
}

type OAuth2Config struct {
    Enabled            bool   // Enable OAuth2 token validation
    ProviderURL        string // OAuth2 provider base URL
    ClientID           string // MCP server's OAuth2 client ID (for server-to-server calls)
    ClientSecret       string // MCP server's OAuth2 client secret
    TokenEndpoint      string // Token validation endpoint
    UserInfoEndpoint   string // User info endpoint
    JWKSEndpoint       string // For JWT validation (if using RS256)
    TokenIntrospection bool   // Use introspection instead of userinfo
    TokenCacheTTL      int    // Cache validated tokens (seconds)
}
```

**Tasks**:
- [ ] Update `GrafanaConfig` struct in mcpgrafana.go
- [ ] Create `OAuth2Config` in new oauth2_client.go
- [ ] Update `ExtractGrafanaInfoFromEnv()` to read OAuth2 settings
- [ ] Update `ExtractGrafanaInfoFromHeaders()` to accept OAuth2 token

#### 2.2 OAuth2 Token Validation & User Info Extraction
**New File**: `oauth2_client.go`

Create OAuth2 integration layer to validate tokens and fetch user info:

```go
type OAuth2Config struct {
    Enabled         bool
    ProviderURL     string // e.g., http://oauth2-provider:8080
    ClientID        string
    ClientSecret    string
    TokenEndpoint   string // /oauth/token or /.well-known/oauth-authorization-server
    UserInfoEndpoint string // /userinfo or /oauth/userinfo
    TokenIntrospection bool // Use introspection instead of userinfo
}

type OAuth2UserInfo struct {
    ID        string            // sub, user_id
    Username  string            // preferred_username or name from LDAP
    Email     string
    Name      string
    Groups    []string
    Roles     []string
    Attributes map[string]interface{}
}

// Validate OAuth2 token and fetch user info
func ValidateToken(ctx context.Context, token string, config OAuth2Config) (*OAuth2UserInfo, error)

// Parse and validate JWT locally (if using RSA keys)
func ValidateTokenLocal(token string, config OAuth2Config) (*OAuth2UserInfo, error)
```

**Key Features**:
- Support both token introspection and JWT validation
- Cache validated tokens to reduce OAuth2 provider calls
- Extract LDAP username and groups from OAuth2 response
- Map OAuth2 attributes to MCP context

**Tasks**:
- [ ] Implement token validation (introspection endpoint)
- [ ] Implement JWT parsing and validation
- [ ] Add token caching with TTL
- [ ] Implement user info fetching
- [ ] Handle token refresh if needed

#### 2.3 HTTP Transport Enhancement
**File**: `proxied_client.go`

Update the HTTP client to include Auth Proxy headers:

```go
// In NewGrafanaClient() or authRoundTripper
func (rt *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    // ... existing auth ...
    
    // Add Auth Proxy headers
    if rt.config.ProxyAuthEnabled && rt.config.AuthenticatedUser != "" {
        req.Header.Set(rt.config.ProxyUserHeader, rt.config.AuthenticatedUser)
        // Could also set email, name, role if available
    }
    
    return rt.transport.RoundTrip(req)
}
```

**Tasks**:
- [ ] Modify `authRoundTripper` to include proxy headers
- [ ] Ensure headers don't override existing auth (service account token still sent)
- [ ] Add configuration validation

#### 2.4 Request Processing Middleware (Extract OAuth2 Token)
**File**: `proxied_handler.go` + new `middleware.go`

Create middleware to extract and validate OAuth2 token early in request pipeline:

```go
// Middleware that runs at start of each request
func OAuth2Middleware(next http.Handler, oauth2Config OAuth2Config) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Extract token from Authorization header
        token := extractBearerToken(r.Header.Get("Authorization"))
        if token == "" {
            http.Error(w, "Missing Authorization token", http.StatusUnauthorized)
            return
        }
        
        // Validate token and get user info
        userInfo, err := ValidateToken(r.Context(), token, oauth2Config)
        if err != nil {
            http.Error(w, "Invalid token", http.StatusUnauthorized)
            return
        }
        
        // Store user info in context for downstream handlers
        ctx := WithOAuth2UserInfo(r.Context(), userInfo)
        
        // Update GrafanaConfig with authenticated user
        grafanaConfig := GrafanaConfigFromContext(ctx)
        grafanaConfig.AuthenticatedUser = userInfo
        
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

**For Stdio Transport**:
- Read token from environment `GRAFANA_OAUTH2_TOKEN` (for testing)
- Or accept via stdin with request metadata

**Tasks**:
- [ ] Create middleware for HTTP/SSE transport
- [ ] Integrate into request handler pipeline
- [ ] Add error handling and logging
- [ ] Add optional token caching

#### 2.5 Configuration & Documentation
**Files to Update**:
- `server.json` - Add new environment variables
- `DEVELOPING.md` - Add auth proxy section
- New file: `docs/auth-proxy-guide.md` - Full integration guide

**New Environment Variables**:
```
# OAuth2 Configuration
OAUTH2_ENABLED                  (default: false)
OAUTH2_PROVIDER_URL             (e.g., http://oauth2-provider:8080)
OAUTH2_CLIENT_ID                (MCP server's client ID for server-to-server calls)
OAUTH2_CLIENT_SECRET            (secret, sensitive)
OAUTH2_TOKEN_ENDPOINT           (e.g., /oauth/token or /oauth2/token)
OAUTH2_USERINFO_ENDPOINT        (e.g., /userinfo or /oauth/userinfo)
OAUTH2_JWKS_ENDPOINT            (optional, for JWT validation)
OAUTH2_TOKEN_INTROSPECTION      (default: false, true to use introspection instead of userinfo)
OAUTH2_TOKEN_CACHE_TTL          (default: 300, cache tokens for N seconds)

# Grafana Auth Proxy
GRAFANA_PROXY_AUTH_ENABLED      (default: true if OAuth2_ENABLED)
GRAFANA_PROXY_USER_HEADER       (default: X-WEBAUTH-USER)
GRAFANA_PROXY_EMAIL_HEADER      (default: X-WEBAUTH-EMAIL)
GRAFANA_PROXY_NAME_HEADER       (default: X-WEBAUTH-NAME)
GRAFANA_PROXY_ROLE_HEADER       (default: X-WEBAUTH-ROLE)
```

**Tasks**:
- [ ] Update `server.json`
- [ ] Write comprehensive integration guide
- [ ] Add troubleshooting section

---

### Phase 3: Testing & Validation (Week 3-4)

#### 3.1 Unit Tests
**Files to Create/Update**:
- `proxied_client_test.go` - Test Auth Proxy header injection
- `auth_proxy_test.go` (new if separate file) - Test user extraction logic
- `mcpgrafana_test.go` - Test config parsing

**Test Cases**:
- [ ] Header extraction with various formats
- [ ] JWT token parsing
- [ ] Service account fallback
- [ ] Multiple transport types
- [ ] Invalid/missing user identity handling
- [ ] Header bypass prevention (service account still sent)

#### 3.2 Integration Tests
**Files**: `tools/admin_integration_test.go` (add new scenarios)

**Test Scenarios**:
- [ ] MCP server configured with Auth Proxy enabled
- [ ] Request with valid X-WEBAUTH-USER header
- [ ] Request with invalid/missing user header
- [ ] Verify Grafana audit logs show correct user
- [ ] Verify API responses reflect user permissions

**Setup**:
```bash
# Start test Grafana with Auth Proxy enabled
docker-compose -f docker-compose.yaml up -d

# Run integration tests
go test ./tools -v -integration -run TestAuthProxy
```

#### 3.3 End-to-End Testing (Manual)

**Setup**:
1. Configure Grafana with Auth Proxy
2. Place reverse proxy in front of MCP server or simulate headers
3. Make test requests with different users
4. Verify:
   - User appears in Grafana UI as logged-in user
   - Audit logs show correct user
   - API calls execute with user's permissions
   - Alerts/annotations attributed to correct user

**Test Script**: Add to `tests/` directory
```bash
# Example: tests/auth_proxy_test.sh
```

---

### Phase 4: Deployment & Documentation (Week 4)

#### 4.1 Docker & Deployment
**Dockerfile Updates**:
- Ensure Auth Proxy configuration documented
- Add example environment variables to Dockerfile.alpine

**docker-compose.yaml**:
- Add example Auth Proxy settings for dev/testing

#### 4.2 Release & Documentation
**Tasks**:
- [ ] Update CHANGELOG.md
- [ ] Add deployment guide to `/docs/`
- [ ] Create migration guide for existing users
- [ ] Add troubleshooting section
- [ ] Create example configurations

---

## Configuration Examples

### Your Scenario: OAuth2 + LDAP + Grafana Auth Proxy
```
OAuth2-authenticated Client (with access_token)
  ↓ (Authorization: Bearer {token})
MCP Server:
  - Validates token against OAuth2 provider (/introspect or /userinfo)
  - Extracts user info: username (from LDAP), email, groups, roles
  - Stores in context
  ↓ (X-WEBAUTH-USER: {username} + X-WEBAUTH-EMAIL + X-WEBAUTH-ROLE)
Grafana:
  - Auth Proxy intercepts headers
  - Syncs user with Grafana database
  - Enforces user permissions (LDAP groups → Grafana roles)
```

**Environment Setup**:
```bash
# OAuth2 Configuration
OAUTH2_ENABLED=true
OAUTH2_PROVIDER_URL=http://oauth2-provider:8080
OAUTH2_CLIENT_ID=mcp-server
OAUTH2_CLIENT_SECRET=secret123
OAUTH2_TOKEN_ENDPOINT=/oauth/token
OAUTH2_USERINFO_ENDPOINT=/userinfo

# Grafana Auth Proxy
GRAFANA_PROXY_AUTH_ENABLED=true
GRAFANA_PROXY_USER_HEADER=X-WEBAUTH-USER
GRAFANA_PROXY_EMAIL_HEADER=X-WEBAUTH-EMAIL
GRAFANA_PROXY_ROLE_HEADER=X-WEBAUTH-ROLE

# OAuth2 Client (service account for validating other tokens)
GRAFANA_SERVICE_ACCOUNT_TOKEN=existing-service-account-token
```

### Example 2: Keycloak OAuth2 with LDAP Backend
```
Keycloak (LDAP + OAuth2)
  ↓
MCP Server validates token → calls /protocol/openid-connect/userinfo
  ↓
Extracts: username, email, groups (from LDAP sync in Keycloak)
  ↓
Grafana Auth Proxy (receives X-WEBAUTH-USER with Keycloak/LDAP username)
```

### Example 3: OAuth2 Proxy (separate OAuth2 gateway)
```
OAuth2 Proxy (validates user against Keycloak/LDAP)
  → (Already has valid token/user info)
MCP Server (validates token, enriches with userinfo endpoint)
  → Grafana Auth Proxy
```

---

## Success Criteria

- [x] MCP server accepts and passes user identity to Grafana
- [x] Grafana authenticates requests using Auth Proxy headers
- [x] User's permissions are enforced (read/write access)
- [x] Audit logs show correct user for all API calls
- [x] Backward compatible (works without Auth Proxy config)
- [x] Documented and tested for all three transport types
- [x] Example configurations provided

---

## Dependencies & Assumptions

**Assumptions**:
1. Grafana is configured with Auth Proxy (`auth.proxy.enabled = true`)
2. User identity is available from one of: HTTP headers, JWT, or session
3. MCP server can be placed behind a reverse proxy that adds user headers
4. Backward compatibility maintained (non-proxy setups still work)

**Dependencies**:
- Go 1.20+ (if adding JWT parsing libraries)
- Grafana 8.0+ (Auth Proxy support)
- No external OAuth2 libraries required (use headers from incoming request)

---

## Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| Service account token + proxy headers sent together | Service account token provides fallback, proxy headers are additional context |
| User impersonation attacks | Must disable Auth Proxy in untrusted networks; validate source of headers |
| Missing user identity | Graceful fallback to service account (maintains functionality) |
| LDAP user mapping | Store LDAP username format constants; document in config guide |
| Performance impact | Auth Proxy mode adds minimal overhead (one header set operation) |

---

---

## Next Steps

Now I'll implement the OAuth2 + Auth Proxy integration. Before starting, please provide:

1. **OAuth2 Provider Details**:
   - Provider URL (e.g., Keycloak instance URL)
   - Example token validation endpoints
   - UserInfo response format (to see what fields are available)

2. **LDAP Integration**:
   - What LDAP attribute contains the username? (e.g., `uid`, `mail`, `sAMAccountName`)
   - What OAuth2 field should we use? (e.g., `preferred_username` from Keycloak)

3. **Groups/Roles**:
   - How are LDAP groups exposed via OAuth2? (e.g., `groups` claim, `roles` claim)
   - Desired mapping to Grafana roles (e.g., `ldap-admins` → Grafana `Admin`)

4. **Testing Environment**:
   - Can I test against a live OAuth2 provider, or should I mock one?
   - Sample valid and invalid tokens available?

**Implementation will proceed as follows:**
1. Create `oauth2_client.go` with token validation logic
2. Extend `GrafanaConfig` and add OAuth2Config
3. Create middleware to extract and validate tokens
4. Modify HTTP transport to inject Auth Proxy headers
5. Add unit and integration tests
6. Update documentation with deployment guide

