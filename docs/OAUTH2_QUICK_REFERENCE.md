# OAuth2 Testing Quick Reference

## Quick Start (Copy & Paste)

```bash
# 1. Initialize environment and start services
./testdata/oauth2-setup.sh

# 2. In new terminal, start MCP server
source .env.oauth2-test
go run ./cmd/mcp-grafana/main.go

# 3. In another terminal, test the flow
./testdata/oauth2-test.sh test-flow john.doe password123
```

## Common Commands

### Get Tokens

```bash
# Get user token
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)
echo $TOKEN

# Get service account token
CLIENT_TOKEN=$(./testdata/oauth2-test.sh client-token)
echo $CLIENT_TOKEN

# Get admin token
ADMIN_TOKEN=$(./testdata/oauth2-test.sh token admin admin123)
echo $ADMIN_TOKEN
```

### Test Endpoints

```bash
# Test MCP tools endpoint with token
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/tools | jq .

# Test Grafana as authenticated user
curl -H "X-WEBAUTH-USER: john.doe" \
     -H "Authorization: Bearer $(grep GRAFANA_SERVICE_ACCOUNT_TOKEN .env.oauth2-test | cut -d= -f2)" \
     http://localhost:3001/api/user | jq .

# Check Grafana datasources with user identity
curl -H "X-WEBAUTH-USER: john.doe" \
     -H "Authorization: Bearer $(grep GRAFANA_SERVICE_ACCOUNT_TOKEN .env.oauth2-test | cut -d= -f2)" \
     http://localhost:3001/api/datasources | jq .
```

### View Configuration

```bash
# Show current OAuth2 config
cat .env.oauth2-test

# Show Keycloak realm config
cat testdata/keycloak-realm.json | jq .

# Show Docker Compose services
docker-compose ps
```

### Debugging

```bash
# View MCP server logs
# (check terminal where MCP is running)

# View Keycloak logs
docker logs mcp-grafana-keycloak-1

# View Grafana logs  
docker logs mcp-grafana-grafana-oauth-1

# Check if services are healthy
curl http://localhost:8082/health/ready      # Keycloak
curl http://localhost:3001/health            # Grafana
curl http://localhost:8080/tools             # MCP (requires token)
```

### Test Users

```bash
# Keycloak admin
Username: admin
Password: admin123

# Test user (editor)
Username: john.doe
Password: password123

# Test user (viewer)
Username: jane.smith
Password: password123

# Test user (admin)
Username: admin
Password: admin123
```

### Service URLs

```
Keycloak:        http://localhost:8082
  Admin:         http://localhost:8082/admin/
  Token:         http://localhost:8082/auth/realms/mcp-grafana/protocol/openid-connect/token
  Userinfo:      http://localhost:8082/auth/realms/mcp-grafana/protocol/openid-connect/userinfo

Grafana:         http://localhost:3001 (with Auth Proxy)
  API:           http://localhost:3001/api/
  Admin:         http://localhost:3001/admin/

MCP Server:      http://localhost:8080
  Tools:         http://localhost:8080/tools
  Health:        http://localhost:8080/health
  Transport:     SSE (Server-Sent Events)
```

## Full Test Scenario

```bash
# 1. Setup (one time)
./testdata/oauth2-setup.sh

# 2. Source config
source .env.oauth2-test

# 3. Start MCP (terminal 1)
go run ./cmd/mcp-grafana/main.go

# 4. Test (terminal 2)

# Get token
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)

# Decode token to inspect claims
echo "$TOKEN" | cut -d. -f2 | base64 -d | jq .

# Call MCP endpoint
curl -s http://localhost:8080/tools \
  -H "Authorization: Bearer $TOKEN" | jq .

# Verify user in Grafana
GRAFANA_TOKEN=$(grep GRAFANA_SERVICE_ACCOUNT_TOKEN .env.oauth2-test | cut -d= -f2)
curl -s "http://localhost:3001/api/users?loginOrEmail=john.doe" \
  -H "Authorization: Bearer $GRAFANA_TOKEN" | jq '.[0]'

# Check Grafana datasources (Auth Proxy headers added by MCP)
curl -s http://localhost:3001/api/datasources \
  -H "X-WEBAUTH-USER: john.doe" \
  -H "Authorization: Bearer $GRAFANA_TOKEN" | jq '.[] | {id, name, type}'
```

## Troubleshooting

| Issue | Solution |
|-------|----------|
| "Port already in use" | `docker-compose down && docker-compose up -d` |
| "Cannot connect to Keycloak" | Wait 10s for startup, then `curl http://localhost:8082/health/ready` |
| "Invalid token" | Get fresh token: `TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)` |
| "Grafana service account failed" | Check `.env.oauth2-test` has `GRAFANA_SERVICE_ACCOUNT_TOKEN` |
| "User not in Grafana" | Run MCP call first, then check again (Auth Proxy creates user on first request) |
| "Certificate error" | Using self-signed certs? Add `--insecure` to curl or `verify=false` in client |

## Performance Checks

```bash
# Token caching (should see multiple hits with one validation)
TOKEN=$(./testdata/oauth2-test.sh token john.doe password123)
time for i in {1..10}; do 
  curl -s http://localhost:8080/tools \
    -H "Authorization: Bearer $TOKEN" > /dev/null
done
# Time should be < 500ms for 10 requests (cache working)
```

## Environment Variables (All Options)

```bash
# OAuth2 Configuration
OAUTH2_ENABLED=true
OAUTH2_PROVIDER_URL=http://localhost:8082/auth/realms/mcp-grafana
OAUTH2_CLIENT_ID=mcp-server
OAUTH2_CLIENT_SECRET=<client-secret>
OAUTH2_USER_INFO_ENDPOINT=/protocol/openid-connect/userinfo
OAUTH2_TOKEN_CACHE_TTL=300

# Grafana Configuration
GRAFANA_URL=http://localhost:3000
GRAFANA_ADMIN_TOKEN=<token>
GRAFANA_SERVICE_ACCOUNT_TOKEN=<token>

# Auth Proxy Configuration
GRAFANA_PROXY_AUTH_ENABLED=true
GRAFANA_PROXY_HEADER_USER=X-WEBAUTH-USER
GRAFANA_PROXY_HEADER_EMAIL=X-WEBAUTH-EMAIL
GRAFANA_PROXY_HEADER_NAME=X-WEBAUTH-NAME
GRAFANA_PROXY_HEADER_ROLE=X-WEBAUTH-ROLE

# MCP Server Configuration
MCP_SERVER_MODE=sse
```

## Keycloak Tasks

### View Realm
```
1. Open http://localhost:8082/admin/
2. Login as admin / admin123
3. Select realm "mcp-grafana" from dropdown
4. View Users, Groups, Client Scopes, etc.
```

### Add New User
```bash
# Via API (get admin token first)
ADMIN_TOKEN=$(curl -s -X POST "http://localhost:8082/auth/realms/master/protocol/openid-connect/token" \
  -d "client_id=admin-cli" \
  -d "username=admin" \
  -d "password=admin123" \
  -d "grant_type=password" | jq -r '.access_token')

curl -X POST "http://localhost:8082/auth/admin/realms/mcp-grafana/users" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "username": "newuser",
    "email": "newuser@example.com",
    "firstName": "New",
    "lastName": "User",
    "enabled": true
  }'
```

## Grafana Tasks

### View Audit Logs
```bash
GRAFANA_TOKEN=$(grep GRAFANA_SERVICE_ACCOUNT_TOKEN .env.oauth2-test | cut -d= -f2)
curl -s "http://localhost:3001/api/audit" \
  -H "Authorization: Bearer $GRAFANA_TOKEN" | jq '.[] | {user, action, resources}'
```

### Check Auth Proxy Status
```bash
GRAFANA_TOKEN=$(grep GRAFANA_SERVICE_ACCOUNT_TOKEN .env.oauth2-test | cut -d= -f2)
curl -s "http://localhost:3001/api/admin/settings" \
  -H "Authorization: Bearer $GRAFANA_TOKEN" | jq '.auth'
```

## Stop Everything

```bash
# Stop MCP server (Ctrl+C in its terminal)

# Stop and remove containers
docker-compose down

# Clean environment
rm .env.oauth2-test
```
