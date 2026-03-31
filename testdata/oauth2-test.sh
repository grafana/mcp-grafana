#!/bin/bash

# OAuth2 Testing Helper Script
# Use this script to get tokens and test the OAuth2 + Auth Proxy flow

set -e

# Color codes
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

grafana_url="${GRAFANA_URL:-http://localhost:3000}"

# Parse global options before loading configuration.
env_file=".env.oauth2-test"

while [ $# -gt 0 ]; do
  case "$1" in
    -e|--env-file)
      if [ -z "$2" ]; then
        echo -e "${RED}Error: missing value for $1${NC}"
        exit 1
      fi
      env_file="$2"
      shift 2
      ;;
    *)
      break
      ;;
  esac
done

if [ ! -f "$env_file" ]; then
  # If not found at the given path, try in testdata/
  if [ -f "testdata/$env_file" ]; then
    env_file="testdata/$env_file"
  fi
fi

if [ -f "$env_file" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$env_file"
  set +a
else
  echo -e "${RED}Error: OAuth2 env file not found${NC}"
  echo "Expected one of:"
  echo "  testdata/.env.oauth2-test"
  echo "  testdata/.env.oauth2-forward-test"
  exit 1
fi

function usage() {
  cat << EOF
Usage: $0 [--env-file <path>] <command> [args]

Commands:
  token <user> [password]     Get OAuth2 token for a user
  get-users                   List users in Grafana using admin credentials
  test-flow <user> [password] Run end-to-end OAuth2/Auth Proxy check
  cleanup                     Stop and remove docker containers
  help                        Show this help message

Examples:
  # Get token for john.doe
  $0 token john.doe password123

  # Test complete flow
  $0 test-flow john.doe password123

  # Use the forwarding env file
  $0 --env-file testdata/.env.oauth2-forward-test test-flow john.doe password123

  # Get users in Grafana
  $0 get-users
EOF
}

function get_token() {
  local user=$1
  local password=$2
  local keycloak_url="${OAUTH2_PROVIDER_URL%/realms*}"
  local realm="mcp-grafana"
  
  if [ -z "$user" ] || [ -z "$password" ]; then
    echo -e "${RED}Error: user and password required${NC}"
    return 1
  fi
  
  echo -e "${BLUE}Requesting token for: $user${NC}" >&2
  
  local response=$(curl -s -X POST "${keycloak_url}/realms/${realm}/protocol/openid-connect/token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "client_id=grafana-ui" \
    -d "grant_type=password" \
    -d "username=$user" \
    -d "password=$password")
  
  local token=$(echo "$response" | jq -r '.access_token' 2>/dev/null || echo "")
  
  if [ -z "$token" ] || [ "$token" = "null" ]; then
    echo -e "${RED}Failed to get token${NC}" >&2
    echo "Response: $response" >&2
    return 1
  fi
  
  echo -e "${GREEN}Token received${NC}" >&2
  echo "$token"
  return 0
}

function get_users() {
  echo -e "${BLUE}Fetching users from Grafana...${NC}"
  
  local response=$(curl -s -u admin:admin "${grafana_url}/api/users" 2>/dev/null || echo "")
  
  if [ -z "$response" ]; then
    echo -e "${RED}Failed to get users (Grafana not responding)${NC}"
    return 1
  fi
  
  echo -e "${GREEN}Users in Grafana:${NC}"
  echo "$response" | jq '.[] | "\(.id): \(.login) (\(.name))"' 2>/dev/null || echo "$response"
  return 0
}

function test_flow() {
  local user=$1
  local password=$2
  local payload
  
  echo -e "\n${BLUE}OAuth2/Auth Proxy test flow${NC}\n"
  
  # Step 1: Get token
  echo -e "${YELLOW}1. Get OAuth2 token${NC}"
  local token
  if ! token=$(get_token "$user" "$password"); then
    echo -e "${RED}Test failed at token retrieval${NC}"
    return 1
  fi
  echo -e "Token: ${GREEN}${token:0:20}...${NC}\n"
  
  # Step 2: Decode token to show claims
  echo -e "${YELLOW}2. Token claims${NC}"
  payload=$(echo "$token" | cut -d. -f2 | tr '_-' '/+' | awk '{ l=length($0)%4; if (l==2) printf "%s==", $0; else if (l==3) printf "%s=", $0; else printf "%s", $0 }')
  local claims=$(printf '%s' "$payload" | base64 -d 2>/dev/null | jq . 2>/dev/null || echo "Could not decode")
  echo "$claims"
  echo ""
  
  # Step 3: Call MCP API with token
  echo -e "${YELLOW}3. MCP health check${NC}"
  if command -v go &> /dev/null; then
    local health_response=$(curl -s "http://localhost:8080/healthz" 2>/dev/null || echo "")
    
    if [ -n "$health_response" ]; then
      echo -e "${GREEN}MCP API reachable${NC}"
      echo "Response: $health_response"
    else
      echo -e "${YELLOW}MCP not running on localhost:8080${NC}"
      echo "  Start with: source testdata/.env.oauth2-test && go run ./cmd/mcp-grafana/main.go"
    fi
  fi
  echo ""
  
  # Step 4: Check if user created in Grafana
  echo -e "${YELLOW}4. Grafana auth proxy user check${NC}"
  local auth_proxy_response=$(curl -s "${grafana_url}/api/user" \
    -H "${GRAFANA_PROXY_USER_HEADER}: $user" \
    -H "${GRAFANA_PROXY_EMAIL_HEADER}: ${user}@example.com" \
    -H "${GRAFANA_PROXY_NAME_HEADER}: ${user}" \
    2>/dev/null || echo "")
  if [ -n "$auth_proxy_response" ] && echo "$auth_proxy_response" | jq -e '.login' >/dev/null 2>&1; then
    echo -e "${GREEN}Auth proxy accepted user headers${NC}"
    echo "$auth_proxy_response" | jq '{id, login, email, name}'
  else
    echo -e "${YELLOW}Could not confirm auth proxy user via /api/user${NC}"
    echo "Response: ${auth_proxy_response:-<empty>}"
  fi
  echo ""
  
  # Step 5: Test Auth Proxy header injection
  echo -e "${YELLOW}5. Auth proxy header response${NC}"
  local response=$(curl -s -i "${grafana_url}/api/user" \
    -H "${GRAFANA_PROXY_USER_HEADER}: $user" \
    -H "${GRAFANA_PROXY_EMAIL_HEADER}: ${user}@example.com" \
    -H "${GRAFANA_PROXY_NAME_HEADER}: ${user}" \
    2>/dev/null || echo "")

  if echo "$response" | grep -q "HTTP/.* 200"; then
    echo -e "${GREEN}Auth proxy headers accepted${NC}"
  else
    echo -e "${YELLOW}Auth proxy header test response:${NC}"
    echo "$response"
  fi
  echo ""
  
  echo -e "${GREEN}Test flow completed${NC}\n"
}

function cleanup() {
  echo -e "${BLUE}Stopping and removing containers...${NC}"
  docker-compose down
  echo -e "${GREEN}Cleanup complete${NC}"
}

# Main command handler
case "$1" in
  token)
    get_token "$2" "$3"
    ;;
  get-users)
    get_users
    ;;
  test-flow)
    test_flow "$2" "$3"
    ;;
  cleanup)
    cleanup
    ;;
  help|--help|-h)
    usage
    ;;
  "")
    echo "No command specified. Use 'help' for usage."
    exit 1
    ;;
  *)
    echo "Unknown command: $1"
    usage
    exit 1
    ;;
esac
