# MCP Grafana Debugging Guide

This guide walks you through debugging the MCP Grafana Go application in Cursor/VS Code, specifically for troubleshooting the automated PR creation functionality.

## 🔧 Prerequisites

- **Cursor/VS Code** with Go extension installed
- **Go Debugger (Delve)** - usually installed with Go extension
- **MCP Grafana** project cloned and built
- **Grafana instance** running locally (http://localhost:3000)
- **Grafana API Key** - Replace `YOUR_GRAFANA_API_KEY_HERE` with your actual API key

## 📁 Debug Configuration Files

### 1. Launch Configuration (`.vscode/launch.json`)

> **⚠️ Security Note**: Replace `YOUR_GRAFANA_API_KEY_HERE` with your actual Grafana API key. Never commit real API keys to version control!

```json
{
    "version": "0.2.0",
    "configurations": [
        {
            "name": "Debug MCP Grafana",
            "type": "go",
            "request": "launch",
            "mode": "debug",
            "program": "${workspaceFolder}/cmd/mcp-grafana",
            "args": ["-t", "stdio"],
            "env": {
                "GRAFANA_URL": "http://localhost:3000",
                "GRAFANA_API_KEY": "YOUR_GRAFANA_API_KEY_HERE",
                "GRAFANA_LOG_LEVEL": "debug"
            },
            "console": "integratedTerminal",
            "stopOnEntry": false,
            "showLog": true
        },
        {
            "name": "Debug MCP Grafana with Input",
            "type": "go",
            "request": "launch",
            "mode": "debug",
            "program": "${workspaceFolder}/cmd/mcp-grafana",
            "args": ["-t", "stdio"],
            "env": {
                "GRAFANA_URL": "http://localhost:3000",
                "GRAFANA_API_KEY": "YOUR_GRAFANA_API_KEY_HERE",
                "GRAFANA_LOG_LEVEL": "debug"
            },
            "console": "integratedTerminal",
            "stopOnEntry": false,
            "showLog": true,
            "preLaunchTask": "prepare-debug-input"
        }
    ]
}
```

### 2. Debug Input File (`debug-input.json`)

```json
{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "create_provisioning_repository_pr", "arguments": {"repository_name": "repository-64c4c30", "title": "Update dashboard title to mcp-testing-dashboard-3", "body": "This PR updates the dashboard title from \"mcp-testing-dashboard-2\" to \"mcp-testing-dashboard-3\" as part of testing the GitOps workflow for dashboard management through file operations.\n\nChanges:\n- Updated spec.title field in new-dashboard.json\n- Dashboard UID remains the same (adpmnwf)\n- All other dashboard configuration preserved\n\nTesting: This change demonstrates the complete GitOps workflow: File Update → Feature Branch → Pull Request → Review → Merge → Sync", "ref": "feature/update-dashboard-title-v3"}}}
```

### 3. Tasks Configuration (`.vscode/tasks.json`)

```json
{
    "version": "2.0.0",
    "tasks": [
        {
            "label": "prepare-debug-input",
            "type": "shell",
            "command": "echo",
            "args": ["Debug input prepared. Use the integrated terminal to paste the JSON-RPC command."],
            "group": "build",
            "presentation": {
                "echo": true,
                "reveal": "always",
                "focus": false,
                "panel": "shared"
            },
            "problemMatcher": []
        }
    ]
}
```

## 🎯 Step-by-Step Debugging Process

### Step 1: Set Breakpoints

Open `tools/provisioning_repositories.go` and set breakpoints at these critical locations:

#### Key Breakpoint Locations:

1. **Function Entry** (Line ~410):
   ```go
   func createProvisioningRepositoryPR(ctx context.Context, args CreateProvisioningRepositoryPRParams) (string, error) {
   ```

2. **URL Construction** (Line ~420):
   ```go
   apiPath := fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/%s/pr?%s", args.RepositoryName, params.Encode())
   requestURL := fmt.Sprintf("%s%s", strings.TrimRight(cfg.URL, "/"), apiPath)
   ```

3. **Request Creation** (Line ~435):
   ```go
   req, err := http.NewRequestWithContext(ctx, "POST", requestURL, nil)
   ```

4. **Authorization Header** (Line ~445):
   ```go
   if cfg.APIKey != "" {
       req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
   }
   ```

5. **HTTP Request** (Line ~455):
   ```go
   resp, err := client.Do(req)
   ```

6. **Status Code Check** (Line ~465):
   ```go
   if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
       return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
   }
   ```

### Step 2: Start Debugging

1. **Open Debug Panel**: Press `Ctrl+Shift+D` (or `Cmd+Shift+D` on Mac)

2. **Select Configuration**: Choose "Debug MCP Grafana" from the dropdown

3. **Start Debugging**: Press `F5` or click the green play button

4. **Wait for Initialization**: You'll see logs in the integrated terminal:
   ```
   Starting Grafana MCP server using stdio transport
   Using Grafana configuration url=http://localhost:3000 api_key_set=true
   ```

### Step 3: Send JSON-RPC Command

1. **Ensure the debugger is running** and waiting for input

2. **In the integrated terminal**, paste the JSON-RPC command:
   ```json
   {"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "create_provisioning_repository_pr", "arguments": {"repository_name": "repository-64c4c30", "title": "Update dashboard title to mcp-testing-dashboard-3", "body": "This PR updates the dashboard title from \"mcp-testing-dashboard-2\" to \"mcp-testing-dashboard-3\" as part of testing the GitOps workflow for dashboard management through file operations.\n\nChanges:\n- Updated spec.title field in new-dashboard.json\n- Dashboard UID remains the same (adpmnwf)\n- All other dashboard configuration preserved\n\nTesting: This change demonstrates the complete GitOps workflow: File Update → Feature Branch → Pull Request → Review → Merge → Sync", "ref": "feature/update-dashboard-title-v3"}}}
   ```

3. **Press Enter** - The debugger will hit your first breakpoint!

### Step 4: Debug Navigation

| Key | Action | Description |
|-----|--------|-------------|
| `F5` | Continue | Resume execution until next breakpoint |
| `F10` | Step Over | Execute current line, don't go into functions |
| `F11` | Step Into | Go into function calls |
| `Shift+F11` | Step Out | Exit current function |
| `Ctrl+Shift+F5` | Restart | Restart debugging session |
| `Shift+F5` | Stop | Stop debugging |

### Step 5: Inspect Variables

In the **Debug Sidebar**, you can inspect:

- **`args`** - Function parameters (repository_name, title, body, ref)
- **`cfg`** - Grafana configuration (URL, API key, etc.)
- **`params`** - URL query parameters being sent
- **`apiPath`** - Constructed API path
- **`requestURL`** - Complete request URL
- **`req`** - HTTP request object with headers
- **`resp`** - HTTP response (status code, body)
- **`err`** - Any errors encountered

## 🔍 Key Debugging Points

### 1. URL Construction Analysis
```go
// Check that the URL is constructed correctly
apiPath := "/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/repository-64c4c30/pr?title=...&content=...&ref=..."
requestURL := "http://localhost:3000/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/repository-64c4c30/pr?..."
```

### 2. Authorization Headers
```go
// Verify the authorization header is set
req.Header.Get("Authorization") // Should be "Bearer YOUR_API_KEY"
```

### 3. Request Parameters
```go
// Check URL parameters are properly encoded
params.Get("title") // "Update dashboard title to mcp-testing-dashboard-3"
params.Get("ref")   // "feature/update-dashboard-title-v3"
params.Get("content") // PR body content
```

### 4. HTTP Response Analysis
```go
// Examine the 403 response
resp.StatusCode // Should be 403
body, _ := io.ReadAll(resp.Body) // Read the error message
```

## 🐛 Expected Debug Findings

Based on our previous analysis, you should observe:

### 1. **Request Construction** ✅
- URL properly constructed
- Parameters correctly encoded
- Authorization header set

### 2. **HTTP Request** ✅
- Request sent successfully
- No network errors

### 3. **Response Analysis** ❌
- **Status Code**: `403 Forbidden`
- **Error Message**: 
  ```json
  {
    "message": "User \"local-mcp-grafana-testing\" cannot create resource \"repositories/pr\" in API group \"provisioning.grafana.app\" in the namespace \"default\": unmapped subresource defaults to no access"
  }
  ```

### 4. **Root Cause** 🎯
- The `"pr"` subresource is not mapped in the authorization logic
- Falls back to "unmapped subresource defaults to no access"
- Requires Grafana admin privileges, but user only has editor/viewer role

## 🔧 Debugging Commands

### Alternative Debug Methods

#### 1. Command Line Debug
```bash
# Build with debug info
go build -o dist/mcp-grafana ./cmd/mcp-grafana

# Run with debug logging
echo '{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "create_provisioning_repository_pr", "arguments": {"repository_name": "repository-64c4c30", "title": "Test PR", "body": "Test", "ref": "feature/test"}}}' | GRAFANA_LOG_LEVEL=debug ./dist/mcp-grafana -t stdio
```

#### 2. Add Debug Prints
```go
// Add to createProvisioningRepositoryPR function
fmt.Printf("DEBUG: Request URL: %s\n", requestURL)
fmt.Printf("DEBUG: Request Headers: %+v\n", req.Header)
fmt.Printf("DEBUG: Response Status: %d\n", resp.StatusCode)
```

## 📊 Debug Output Analysis

### Expected Terminal Output:
```
time=2025-07-02T22:34:09.482+02:00 level=INFO msg="Starting Grafana MCP server using stdio transport"
time=2025-07-02T22:34:09.482+02:00 level=INFO msg="Using Grafana configuration" url=http://localhost:3000 api_key_set=true
DEBUG: Request URL: http://localhost:3000/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/repository-64c4c30/pr?title=Update+dashboard+title+to+mcp-testing-dashboard-3&content=This+PR+updates...&ref=feature/update-dashboard-title-v3
DEBUG: Request Headers: map[Authorization:[Bearer YOUR_GRAFANA_API_KEY_HERE]]
DEBUG: Response Status: 403
{"jsonrpc":"2.0","id":4,"error":{"code":-32603,"message":"unexpected status code: 403"}}
```

## 🎯 Troubleshooting Tips

### 1. Breakpoints Not Hit
- Ensure you're using the correct configuration
- Check that the function name matches exactly
- Rebuild the application: `go build -o dist/mcp-grafana ./cmd/mcp-grafana`

### 2. Debugger Won't Start
- Install Go extension in Cursor/VS Code
- Install Delve: `go install github.com/go-delve/delve/cmd/dlv@latest`
- Check Go version: `go version`

### 3. Environment Variables
- Verify Grafana URL is correct
- Check API key is valid
- Ensure Grafana is running on port 3000

### 4. JSON-RPC Format
- Ensure the command is on a single line
- Check for proper JSON escaping
- Verify repository name exists

## 🚀 Next Steps After Debugging

Once you've confirmed the 403 authorization issue:

1. **Document the exact error response**
2. **Identify the authorization code location**
3. **Propose the fix**: Add `"pr"` to the editor-level subresources
4. **Test the fix** with the same debugging setup
5. **Verify the solution** works end-to-end

## 📝 Debug Session Checklist

- [ ] Breakpoints set at key locations
- [ ] Debug configuration loaded
- [ ] Grafana instance running
- [ ] JSON-RPC command prepared
- [ ] Variables inspected at each breakpoint
- [ ] Error response captured and analyzed
- [ ] Root cause identified
- [ ] Solution path determined

---

**Happy Debugging!** 🐛✨

This debugging setup gives you complete visibility into the PR creation process and helps identify exactly where and why the authorization fails. 