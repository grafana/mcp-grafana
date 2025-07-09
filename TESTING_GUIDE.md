# MCP Grafana Tools Testing Guide

This guide shows you how to test the MCP Grafana tools via command line against your actual Grafana instance.

## 🚀 Setup

### 1. Environment Variables
```bash
export GRAFANA_URL="http://localhost:3000"  # Your Grafana URL
export GRAFANA_API_KEY="your-api-key-here"   # Your service account token
```

### 2. Start Grafana (if using docker-compose)
```bash
# Start the development environment
docker-compose up -d

# Check if Grafana is running
curl http://localhost:3000/api/health
```

## 🧪 Testing Commands

### Basic JSON-RPC Format
All commands follow this pattern:
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "METHOD", "params": {PARAMS}}' | go run cmd/mcp-grafana/main.go -t stdio
```

## 📋 **1. List All Available Tools**

```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.tools[] | .name' | sort
```

## 📊 **2. Dashboard Tools**

### Search Dashboards
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "search_dashboards", "arguments": {"query": ""}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

### Get Dashboard by UID
```bash
# Replace with actual dashboard UID from search results
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "get_dashboard_by_uid", "arguments": {"uid": "UDdpyzz7z"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

### Get Dashboard Manager Info (GitOps)
```bash
# Check if dashboard is managed by GitOps
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "get_dashboard_manager", "arguments": {"id": "UDdpyzz7z"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

### Smart Dashboard Update
```bash
# Test smart dashboard update (detects if provisioned)
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "smart_update_dashboard", "arguments": {"dashboard": {"uid": "test-uid", "title": "Test Dashboard", "panels": []}, "message": "Test update via MCP"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

## 🔄 **3. GitOps/Provisioning Tools**

### List Git Repositories
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "list_provisioning_repositories", "arguments": {}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

### List Repository Branches
```bash
# Replace with actual repository name
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "list_provisioning_repository_branches", "arguments": {"repository_name": "my-repo"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

### Get Repository File Content
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "get_provisioning_repository_file_content", "arguments": {"repository_name": "my-repo", "path": "dashboards/my-dashboard.json"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

### Manage Repository File
```bash
# Create/update a file in the repository
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "manage_file", "arguments": {"repository_name": "my-repo", "path": "dashboards/new-dashboard.json", "operation": "create", "content": "{\"dashboard\": {\"title\": \"New Dashboard\"}}", "message": "Add new dashboard via MCP"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

### Create Pull Request
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "create_provisioning_repository_pr", "arguments": {"repository_name": "my-repo", "title": "Add new dashboard", "body": "This PR adds a new dashboard for monitoring", "ref": "feature/new-dashboard"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

## 📊 **4. Datasource Tools**

### List Datasources
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "list_datasources", "arguments": {}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

### Get Datasource by UID
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "get_datasource_by_uid", "arguments": {"uid": "prometheus-uid"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

## 📈 **5. Prometheus Tools**

### Query Prometheus
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "query_prometheus", "arguments": {"datasource_uid": "prometheus-uid", "query": "up", "type": "instant"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

### List Prometheus Metrics
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "list_prometheus_metric_names", "arguments": {"datasource_uid": "prometheus-uid"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

## 📝 **6. Loki Tools**

### Query Loki Logs
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "query_loki_logs", "arguments": {"datasource_uid": "loki-uid", "query": "{job=\"grafana\"}", "type": "logs"}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

## 🚨 **7. Alerting Tools**

### List Alert Rules
```bash
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "list_alert_rules", "arguments": {}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

## 🎯 **Quick Test Script**

Create a test script:

```bash
#!/bin/bash
# test-mcp-tools.sh

set -e

export GRAFANA_URL="http://localhost:3000"
export GRAFANA_API_KEY="your-api-key-here"

echo "🔍 Testing MCP Grafana Tools..."

echo "📋 1. Listing tools..."
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.tools | length' | \
  xargs echo "Found tools:"

echo "📊 2. Searching dashboards..."
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "search_dashboards", "arguments": {"query": ""}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text' | \
  jq length | \
  xargs echo "Found dashboards:"

echo "🔄 3. Checking GitOps repositories..."
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "list_provisioning_repositories", "arguments": {}}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'

echo "✅ Test complete!"
```

## 🔧 **Development Testing with Auto-Restart**

For development, use the auto-restart setup:

```bash
# Terminal 1: Start development server
make dev

# Terminal 2: Test tools (they'll auto-restart when you change code)
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "search_dashboards", "arguments": {"query": ""}}}' | \
  ./tmp/main -t stdio 2>/dev/null | \
  jq -r '.result.content[0].text'
```

## 🚨 **Troubleshooting**

### Connection Refused
```bash
# Check if Grafana is running
curl http://localhost:3000/api/health

# Check docker containers
docker-compose ps
```

### Authentication Errors
```bash
# Test API key
curl -H "Authorization: Bearer $GRAFANA_API_KEY" \
     http://localhost:3000/api/datasources
```

### Tool Not Found
```bash
# List all available tools
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}' | \
  go run cmd/mcp-grafana/main.go -t stdio 2>/dev/null | \
  jq -r '.result.tools[].name' | grep dashboard
```

## 📖 **Tips**

1. **Use `jq` for JSON formatting**: Install with `brew install jq`
2. **Redirect logs**: Use `2>/dev/null` to hide server logs
3. **Check tool schemas**: Use `tools/list` to see available parameters
4. **Test incrementally**: Start with simple tools like `list_datasources`
5. **Use development mode**: `make dev` for auto-restart during development 