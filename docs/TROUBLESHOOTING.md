# Troubleshooting Guide

This guide helps you diagnose and resolve common issues with the Grafana MCP server.

## Table of Contents

- [General Troubleshooting](#general-troubleshooting)
- [Installation Issues](#installation-issues)
- [Connection Issues](#connection-issues)
- [Authentication Issues](#authentication-issues)
- [TLS/Certificate Issues](#tls-certificate-issues)
- [Tool-Specific Issues](#tool-specific-issues)
- [Performance Issues](#performance-issues)
- [Debug Mode](#debug-mode)
- [Common Error Messages](#common-error-messages)
- [Getting Help](#getting-help)

## General Troubleshooting

### Enable Debug Mode

The first step in troubleshooting is to enable debug mode for detailed logging:

```bash
mcp-grafana --debug
```

Debug mode provides:
- Full HTTP request/response logs
- Detailed error messages
- Timing information
- Connection details

See [Debug Mode](#debug-mode) section for more details.

### Check Basic Connectivity

Verify you can reach Grafana:

```bash
# Check if Grafana is accessible
curl -v $GRAFANA_URL/api/health

# Check with authentication
curl -v -H "Authorization: Bearer $GRAFANA_SERVICE_ACCOUNT_TOKEN" \
  $GRAFANA_URL/api/datasources
```

### Verify Environment Variables

Ensure required environment variables are set:

```bash
echo $GRAFANA_URL
echo $GRAFANA_SERVICE_ACCOUNT_TOKEN
```

If empty, export them:

```bash
export GRAFANA_URL=http://localhost:3000
export GRAFANA_SERVICE_ACCOUNT_TOKEN=your-token
```

## Installation Issues

### Binary Not Found: `spawn mcp-grafana ENOENT`

**Symptom:** Claude Desktop or other clients show "spawn mcp-grafana ENOENT" error.

**Cause:** The binary is not in PATH or the path in configuration is incorrect.

**Solutions:**

1. **Use absolute path in configuration:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "/usr/local/bin/mcp-grafana",
      "args": [],
      "env": { ... }
    }
  }
}
```

2. **Find the binary location:**

```bash
which mcp-grafana
# or
whereis mcp-grafana
```

3. **Ensure binary is executable:**

```bash
chmod +x /path/to/mcp-grafana
```

### Docker Image Not Found

**Symptom:** `docker: Error response from daemon: manifest for mcp/grafana:latest not found`

**Solutions:**

1. **Pull the image explicitly:**

```bash
docker pull mcp/grafana:latest
```

2. **Check Docker Hub for available tags:**

```bash
docker search mcp/grafana
```

### Go Install Fails

**Symptom:** `go install` command fails or binary not found after installation.

**Solutions:**

1. **Ensure Go is installed:**

```bash
go version
```

2. **Check GOBIN or GOPATH:**

```bash
echo $GOBIN
echo $GOPATH
```

3. **Add Go bin directory to PATH:**

```bash
export PATH=$PATH:$(go env GOPATH)/bin
```

## Connection Issues

### Cannot Connect to Grafana

**Symptom:** Connection refused, timeout, or network errors.

**Solutions:**

1. **Verify Grafana is running:**

```bash
curl $GRAFANA_URL/api/health
```

2. **Check firewall rules:**

```bash
# Linux
sudo iptables -L

# Check if port is open
nc -zv localhost 3000
```

3. **Verify URL format:**

- Include protocol: `http://` or `https://`
- No trailing slash: `http://localhost:3000` (not `http://localhost:3000/`)
- Correct port number

4. **For Docker containers:**

Use `host.docker.internal` instead of `localhost`:

```bash
docker run --rm -i \
  -e GRAFANA_URL=http://host.docker.internal:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<token> \
  mcp/grafana -t stdio
```

### SSE/HTTP Server Not Starting

**Symptom:** Server fails to start in SSE or streamable-http mode.

**Solutions:**

1. **Check if port is already in use:**

```bash
# Linux/macOS
lsof -i :8000

# Windows
netstat -ano | findstr :8000
```

2. **Use a different port:**

```bash
mcp-grafana -t sse --address :9090
```

3. **Check permissions:**

Ports below 1024 require root/administrator privileges.

### Health Check Fails

**Symptom:** `/healthz` endpoint returns errors or is not accessible.

**Solutions:**

1. **Verify transport mode:**

Health check is only available for SSE and streamable-http transports, not stdio.

2. **Check server is running:**

```bash
ps aux | grep mcp-grafana
```

3. **Test endpoint:**

```bash
curl http://localhost:8000/healthz
# Should return: ok
```

## Authentication Issues

### Invalid Service Account Token

**Symptom:** 401 Unauthorized or authentication failed errors.

**Solutions:**

1. **Verify token is correct:**

```bash
# Test token
curl -H "Authorization: Bearer $GRAFANA_SERVICE_ACCOUNT_TOKEN" \
  $GRAFANA_URL/api/datasources
```

2. **Check token hasn't expired:**

- Go to Grafana → Administration → Service Accounts
- Find your service account
- Check token expiration date

3. **Generate new token:**

If token is invalid or expired, generate a new one.

4. **Verify token format:**

Token should be a long string without spaces or special characters.

### Deprecated API Key Warning

**Symptom:** Warning about `GRAFANA_API_KEY` being deprecated.

**Solution:**

Replace `GRAFANA_API_KEY` with `GRAFANA_SERVICE_ACCOUNT_TOKEN`:

```bash
# Old (deprecated)
export GRAFANA_API_KEY=<your-key>

# New (recommended)
export GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token>
```

### Username/Password Authentication Fails

**Symptom:** Authentication fails with username and password.

**Solutions:**

1. **Verify credentials:**

```bash
curl -u "$GRAFANA_USERNAME:$GRAFANA_PASSWORD" \
  $GRAFANA_URL/api/datasources
```

2. **Check both variables are set:**

```bash
echo $GRAFANA_USERNAME
echo $GRAFANA_PASSWORD
```

3. **Escape special characters:**

If password contains special characters, quote it properly.

## TLS/Certificate Issues

### TLS Handshake Failed

**Symptom:** `tls: handshake failure` or certificate verification errors.

**Solutions:**

1. **Verify certificate paths:**

```bash
ls -l /path/to/client.crt
ls -l /path/to/client.key
ls -l /path/to/ca.crt
```

2. **Check certificate validity:**

```bash
openssl x509 -in /path/to/client.crt -text -noout
# Check dates in "Validity" section
```

3. **Verify CA matches server certificate:**

```bash
# Check server certificate
openssl s_client -connect grafana.example.com:443 -showcerts
```

4. **For testing only, skip verification:**

```bash
mcp-grafana --tls-skip-verify
```

> **Warning:** Never use `--tls-skip-verify` in production!

### Certificate Not Trusted

**Symptom:** Certificate validation errors or untrusted certificate warnings.

**Solutions:**

1. **Use custom CA file:**

```bash
mcp-grafana --tls-ca-file /path/to/ca.crt
```

2. **Add certificate to system trust store:**

```bash
# Linux (Ubuntu/Debian)
sudo cp ca.crt /usr/local/share/ca-certificates/
sudo update-ca-certificates

# macOS
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain ca.crt
```

### mTLS Authentication Fails

**Symptom:** Client certificate authentication errors.

**Solutions:**

1. **Verify both cert and key are provided:**

```bash
mcp-grafana \
  --tls-cert-file /path/to/client.crt \
  --tls-key-file /path/to/client.key
```

2. **Check certificate and key match:**

```bash
# Certificate modulus
openssl x509 -noout -modulus -in client.crt | openssl md5

# Key modulus
openssl rsa -noout -modulus -in client.key | openssl md5

# These should match
```

3. **Verify certificate is not encrypted:**

If key is encrypted, decrypt it:

```bash
openssl rsa -in encrypted.key -out decrypted.key
```

## Tool-Specific Issues

### Grafana Version Compatibility

**Symptom:** Error when using datasource tools:

```
get datasource by uid : [GET /datasources/uid/{uid}][400] getDataSourceByUidBadRequest {"message":"id is invalid"}
```

**Cause:** Grafana version is earlier than 9.0.

**Solution:** Upgrade Grafana to version 9.0 or later. The `/datasources/uid/{uid}` API endpoint was introduced in Grafana 9.0.

### Dashboard Tools Not Working

**Symptom:** Dashboard operations fail with permission errors.

**Solutions:**

1. **Check RBAC permissions:**

See [RBAC Guide](RBAC.md) for required permissions:
- `dashboards:read` for read operations
- `dashboards:write` for updates
- `dashboards:create` for new dashboards

2. **Verify scope includes target dashboard:**

```bash
# Test API access
curl -H "Authorization: Bearer $GRAFANA_SERVICE_ACCOUNT_TOKEN" \
  $GRAFANA_URL/api/dashboards/uid/<dashboard-uid>
```

3. **Check dashboard exists:**

```bash
# Search for dashboard
curl -H "Authorization: Bearer $GRAFANA_SERVICE_ACCOUNT_TOKEN" \
  "$GRAFANA_URL/api/search?query=<dashboard-name>"
```

### Datasource Query Fails

**Symptom:** Prometheus or Loki queries fail.

**Solutions:**

1. **Verify datasource exists:**

```bash
curl -H "Authorization: Bearer $GRAFANA_SERVICE_ACCOUNT_TOKEN" \
  $GRAFANA_URL/api/datasources
```

2. **Check datasource UID:**

Use the correct UID from the datasource list.

3. **Test datasource connectivity:**

In Grafana UI, go to datasource settings and click "Save & test".

4. **Verify query permissions:**

Ensure service account has `datasources:query` permission.

### Incident Tools Not Working

**Symptom:** Incident operations fail.

**Solutions:**

1. **Verify Grafana Incident is installed:**

Check in Grafana → Administration → Plugins.

2. **Check service account role:**

Incident tools require basic roles:
- Viewer role for read operations
- Editor role for write operations

3. **Verify incident plugin is licensed:**

Some Grafana Incident features require a license.

### OnCall Tools Not Working

**Symptom:** OnCall operations fail.

**Solutions:**

1. **Verify Grafana OnCall is installed and configured:**

Check in Grafana → Administration → Plugins.

2. **Check plugin-specific permissions:**

OnCall requires plugin-specific RBAC permissions. See [RBAC Guide](RBAC.md).

3. **Test OnCall API:**

```bash
curl -H "Authorization: Bearer $GRAFANA_SERVICE_ACCOUNT_TOKEN" \
  $GRAFANA_URL/api/plugins/grafana-oncall-app/resources/api/v1/schedules
```

## Performance Issues

### Slow Response Times

**Symptom:** Tools take a long time to respond.

**Solutions:**

1. **Check Grafana server performance:**

```bash
curl -w "@curl-format.txt" -o /dev/null -s \
  -H "Authorization: Bearer $GRAFANA_SERVICE_ACCOUNT_TOKEN" \
  $GRAFANA_URL/api/datasources
```

2. **Reduce dashboard size:**

Use `get_dashboard_summary` or `get_dashboard_property` instead of `get_dashboard_by_uid` for large dashboards.

3. **Limit query time ranges:**

For Prometheus/Loki queries, use shorter time ranges.

4. **Disable unused tools:**

```bash
mcp-grafana --disable-oncall --disable-incident --disable-sift
```

### High Memory Usage

**Symptom:** MCP server uses excessive memory.

**Solutions:**

1. **Avoid loading large dashboards:**

Use summary/property tools instead of full dashboard retrieval.

2. **Limit concurrent operations:**

If using streamable-http mode, consider resource limits.

3. **Monitor with debug mode:**

```bash
mcp-grafana --debug
```

### Context Window Exhausted

**Symptom:** AI assistant reports context window full or token limit exceeded.

**Solutions:**

1. **Use context-efficient tools:**

- `get_dashboard_summary` instead of `get_dashboard_by_uid`
- `get_dashboard_property` with JSONPath for specific data
- `patch_dashboard` instead of full updates

2. **Disable unused tool categories:**

```bash
mcp-grafana --disable-oncall --disable-incident --disable-sift
```

3. **Use specific tool enable list:**

```bash
mcp-grafana --enabled-tools list_dashboards,query_prometheus
```

## Debug Mode

### Enabling Debug Mode

Add `--debug` flag to any command:

```bash
mcp-grafana --debug
```

### What Debug Mode Shows

Debug mode logs:
- HTTP request method, URL, headers
- Request body (for POST/PUT/PATCH)
- Response status code and headers
- Response body
- Request/response timing
- TLS connection details
- Error stack traces

### Debug Mode in Client Configuration

**Claude Desktop:**

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": ["--debug"],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<token>"
      }
    }
  }
}
```

**Docker:**

```bash
docker run --rm -i \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<token> \
  mcp/grafana -t stdio --debug
```

### Analyzing Debug Output

Look for:
- HTTP status codes (200 = success, 401 = auth, 403 = permission, 404 = not found)
- Error messages in response bodies
- Timing to identify slow operations
- TLS handshake details for certificate issues

## Common Error Messages

### "Permission denied" or "403 Forbidden"

**Cause:** Missing RBAC permissions.

**Solution:** Add required permissions to service account. See [RBAC Guide](RBAC.md).

### "401 Unauthorized"

**Cause:** Invalid or missing authentication.

**Solution:** Check service account token is valid and properly set.

### "404 Not Found"

**Cause:** Resource doesn't exist or incorrect UID/ID.

**Solutions:**
- Verify resource exists in Grafana
- Check UID/ID is correct
- Ensure resource is in accessible folder

### "Connection refused"

**Cause:** Cannot connect to Grafana.

**Solutions:**
- Verify Grafana is running
- Check URL is correct
- Verify firewall rules
- For Docker, use `host.docker.internal`

### "Context deadline exceeded" or "Timeout"

**Cause:** Request took too long.

**Solutions:**
- Check Grafana server performance
- Reduce query complexity
- Increase timeout settings
- Check network connectivity

### "Invalid JSONPath expression"

**Cause:** Malformed JSONPath in `get_dashboard_property`.

**Solution:** Verify JSONPath syntax. Examples:
- `$.title` - root level property
- `$.panels[*].title` - array elements
- `$.panels[?(@.type=='graph')]` - filtered elements

## Getting Help

### Before Asking for Help

1. Enable debug mode and capture logs
2. Check this troubleshooting guide
3. Review [RBAC documentation](RBAC.md)
4. Test Grafana API directly with curl
5. Verify Grafana version compatibility

### Where to Get Help

- **GitHub Issues:** [github.com/grafana/mcp-grafana/issues](https://github.com/grafana/mcp-grafana/issues)
- **GitHub Discussions:** [github.com/grafana/mcp-grafana/discussions](https://github.com/grafana/mcp-grafana/discussions)
- **Grafana Community:** [community.grafana.com](https://community.grafana.com)

### Information to Include

When reporting issues, include:

1. **Version information:**
   ```bash
   mcp-grafana --version
   ```

2. **Grafana version:**
   ```bash
   curl $GRAFANA_URL/api/health
   ```

3. **Debug logs:**
   Run with `--debug` and capture relevant output

4. **Configuration:**
   - Transport mode used
   - Client type (Claude Desktop, VSCode, etc.)
   - Any custom flags or settings

5. **Steps to reproduce:**
   - Exact commands run
   - Expected vs actual behavior
   - Error messages

6. **Environment:**
   - Operating system
   - Docker version (if applicable)
   - Go version (if built from source)

## Next Steps

- [Review configuration guide](CONFIGURATION.md)
- [Check RBAC permissions](RBAC.md)
- [Explore features](FEATURES.md)
- [View examples](examples/)