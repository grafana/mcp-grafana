#!/bin/bash

# Quick Start Guide
# Copy and paste these commands to get your OAuth2 testing environment running

echo "================================================"
echo "OAuth2 Testing Environment - Quick Start"
echo "================================================"
echo ""

# Step 1: Navigate to workspace
echo "Step 1: Navigate to workspace"
echo "cd /workspaces/mcp-grafana"
echo ""

# Step 2: Make scripts executable (one-time)
echo "Step 2: Make scripts executable"
echo "chmod +x testdata/oauth2-setup.sh testdata/oauth2-test.sh"
echo ""

# Step 3: Run setup
echo "Step 3: Run automated setup (30-60 seconds)"
echo "./testdata/oauth2-setup.sh"
echo ""
echo "This will:"
echo "  ✓ Start Keycloak (port 8082) and Grafana (port 3001)"
echo "  ✓ Prepare the local OAuth2 + Auth Proxy demo environment"
echo "  ✓ Use the user-centric flow without a Grafana service account"
echo "  ✓ Leave .env.oauth2-test ready to source"
echo ""

# Step 4: Start MCP in new terminal
echo "Step 4: Start MCP Server (in Terminal 1)"
echo "source .env.oauth2-test"
echo "go run ./cmd/mcp-grafana/main.go"
echo ""

# Step 5: Test in new terminal
echo "Step 5: Test OAuth2 Flow (in Terminal 2)"
echo "./testdata/oauth2-test.sh test-flow john.doe password123"
echo ""

# Step 6: Additional commands
echo "Step 6: Other useful commands"
echo ""
echo "Get user token:"
echo "  TOKEN=\$(./testdata/oauth2-test.sh token john.doe password123)"
echo "  echo \$TOKEN"
echo ""
echo "Call MCP with token:"
echo "  curl -H \"Authorization: Bearer \$TOKEN\" http://localhost:8080/tools | jq ."
echo ""
echo "Check user in Grafana:"
echo "  curl -s \"http://localhost:3001/api/user\" \\"
echo "    -H \"X-WEBAUTH-USER: john.doe\" \\"
echo "    -H \"X-WEBAUTH-EMAIL: john.doe@example.com\" | jq ."
echo ""
echo "View Keycloak admin console:"
echo "  http://localhost:8082  (admin / admin123)"
echo ""
echo "View Grafana:"
echo "  http://localhost:3001  (admin / admin)"
echo ""

echo "================================================"
echo "Documentation:"
echo "================================================"
echo ""
echo "Full Testing Guide:         docs/OAUTH2_LOCAL_TESTING.md"
echo "Quick Reference:            docs/OAUTH2_QUICK_REFERENCE.md"
echo "Setup Overview:             testdata/README_OAUTH2_TESTING.md"
echo "File Inventory:             OAUTH2_FILE_INVENTORY.md"
echo "Completion Summary:         OAUTH2_SETUP_COMPLETE.md"
echo ""

echo "Ready to start? Run:"
echo "  cd /workspaces/mcp-grafana && ./testdata/oauth2-setup.sh"
echo ""
