#!/bin/bash

# OAuth2 Testing Setup Script - User-Centric Model
# Sets up local environment for testing OAuth2 + Auth Proxy integration
# No Grafana service account required - uses user identity headers

set -e

echo "================================"
echo "OAuth2 + Auth Proxy Setup Script"
echo "================================"

# Color codes
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Step 1: Start Docker Compose
echo -e "\n${BLUE}Step 1: Starting Docker Compose services...${NC}"
docker-compose up -d keycloak grafana-oauth prometheus loki

echo -e "${GREEN}✓ Waiting for services to be healthy...${NC}"
sleep 5

# Check Keycloak health
echo -n "Keycloak: "
for i in {1..30}; do
  if curl -s http://localhost:8082/health/ready > /dev/null 2>&1; then
    echo -e "${GREEN}✓${NC}"
    break
  fi
  if [ $i -eq 30 ]; then
    echo -e "${YELLOW}⚠ (might take longer)${NC}"
  fi
  echo -n "."
  sleep 1
done

# Check Grafana health
echo -n "Grafana: "
for i in {1..30}; do
  if curl -s http://localhost:3001/health > /dev/null 2>&1; then
    echo -e "${GREEN}✓${NC}"
    break
  fi
  if [ $i -eq 30 ]; then
    echo -e "${YELLOW}⚠ (might take longer)${NC}"
  fi
  echo -n "."
  sleep 1
done

# Step 2: Display credentials and workflow
echo -e "\n${BLUE}Step 2: Test Environment Ready${NC}"
echo -e "${GREEN}Keycloak OAuth2 Provider:${NC}"
echo "  URL: http://localhost:8082"
echo "  Admin: admin / admin123"
echo "  Realm: mcp-grafana"
echo ""
echo -e "${GREEN}Keycloak Test Users:${NC}"
echo "  admin / admin123 (Admin role)"
echo "  john.doe / password123 (Editor role)"
echo "  jane.smith / password123 (Viewer role)"
echo ""
echo -e "${GREEN}Grafana (Auth Proxy Enabled):${NC}"
echo "  URL: http://localhost:3001"
echo "  Admin: admin / admin"
echo "  Note: Uses X-WEBAUTH-* headers for user identity"
echo ""
echo -e "${GREEN}MCP Server (Ready to Use):${NC}"
echo "  Configuration: .env.oauth2-test"
echo "  Transport: SSE (Server-Sent Events)"
echo "  Start with: go run ./cmd/mcp-grafana/main.go"
echo ""

# Step 3: Testing workflow
echo -e "\n${BLUE}Step 3: User-Centric OAuth2 Flow${NC}"
echo -e "${YELLOW}User-Centric Model (No Service Account):${NC}"
echo "  1. Client gets OAuth2 token from Keycloak"
echo "  2. Client sends token to MCP (Authorization header)"
echo "  3. MCP validates token against Keycloak"
echo "  4. MPC extracts user identity (username, email, groups)"
echo "  5. MPC injects X-WEBAUTH-* headers for Grafana"
echo "  6. Grafana trusts headers → treats request as user"
echo "  7. All operations execute in user's context"
echo ""

# Step 4: Quick start commands
echo -e "\n${BLUE}Step 4: Quick Start Commands${NC}"
echo -e "${YELLOW}1. Get OAuth2 token (user credentials):${NC}"
echo '   TOKEN=$(curl -s -X POST "http://localhost:8082/auth/realms/mcp-grafana/protocol/openid-connect/token" \\'
echo '     -H "Content-Type: application/x-www-form-urlencoded" \\'
echo '     -d "client_id=grafana-ui" \\'
echo '     -d "grant_type=password" \\'
echo '     -d "username=john.doe" \\'
echo '     -d "password=password123" | jq -r ".access_token")'
echo ""
echo -e "${YELLOW}2. Start MCP Server:${NC}"
echo '   source .env.oauth2-test'
echo '   go run ./cmd/mcp-grafana/main.go'
echo ""
echo -e "${YELLOW}3. Call MCP with user token (new terminal):${NC}"
echo '   curl -X GET "http://localhost:8080/tools" \\'
echo '     -H "Authorization: Bearer $TOKEN" | jq .'
echo ""
echo -e "${YELLOW}4. Run complete test flow:${NC}"
echo '   ./testdata/oauth2-test.sh test-flow john.doe password123'
echo ""

# Final summary
echo -e "\n${GREEN}================================${NC}"
echo -e "${GREEN}✓ Environment Ready!${NC}"
echo -e "${GREEN}================================${NC}"
echo ""
echo "Configuration: ${BLUE}.env.oauth2-test${NC} (no manual edits needed)"
echo ""
echo "Next steps:"
echo "1. Start MCP:  ${BLUE}source .env.oauth2-test && go run ./cmd/mcp-grafana/main.go${NC}"
echo "2. Test flow:  ${BLUE}./testdata/oauth2-test.sh test-flow john.doe password123${NC}"
echo ""
