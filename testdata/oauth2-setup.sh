#!/bin/bash

# OAuth2 local setup for Keycloak + Grafana Auth Proxy test flow.

set -e

echo "OAuth2 setup: start local services"

# Color codes
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}Starting Docker Compose services...${NC}"
docker-compose up -d keycloak grafana prometheus loki

echo -e "${GREEN}Waiting for services to become healthy...${NC}"
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
  if curl -s http://localhost:3000/health > /dev/null 2>&1; then
    echo -e "${GREEN}✓${NC}"
    break
  fi
  if [ $i -eq 30 ]; then
    echo -e "${YELLOW}⚠ (might take longer)${NC}"
  fi
  echo -n "."
  sleep 1
done

echo
echo -e "${BLUE}Downloading Keycloak JWKS for Grafana JWT auth...${NC}"
JWKS_FILE="$(dirname "$0")/keycloak-jwks.json"
for i in {1..10}; do
  if curl -sf http://localhost:8082/realms/mcp-grafana/protocol/openid-connect/certs -o "$JWKS_FILE" 2>/dev/null; then
    KEYS=$(jq '.keys | length' "$JWKS_FILE" 2>/dev/null || echo 0)
    echo -e "${GREEN}✓ JWKS saved ($KEYS keys)${NC}"
    break
  fi
  if [ $i -eq 10 ]; then
    echo -e "${YELLOW}⚠ Could not download JWKS — re-run setup after Keycloak is ready${NC}"
  fi
  sleep 2
done

echo
echo -e "${GREEN}Environment ready.${NC}"
echo
echo "Services:"
echo "  Keycloak: http://localhost:8082 (admin/admin123)"
echo "  Grafana:  http://localhost:3000 (admin/admin)"
echo
echo "Start MCP:"
echo "  source .env.oauth2-test"
echo "  go run ./cmd/mcp-grafana/main.go"
echo
echo "Run test flow (new terminal):"
echo "  ./testdata/oauth2-test.sh test-flow john.doe password123"
