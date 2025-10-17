# Claude Desktop Configuration Examples

This guide provides configuration examples for using the Grafana MCP server with Claude Desktop.

## Configuration File Location

Claude Desktop uses a JSON configuration file located at:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`
- **Linux:** `~/.config/Claude/claude_desktop_config.json`

## Basic Configuration

### Using Binary (Local Grafana)

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-service-account-token>"
      }
    }
  }
}
```

### Using Binary (Grafana Cloud)

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "https://myinstance.grafana.net",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-service-account-token>"
      }
    }
  }
}
```

### With Full Binary Path

If you see `Error: spawn mcp-grafana ENOENT`, use the full path:

```json
{
  "mcpServers": {
    "grafana": {
      "command": "/usr/local/bin/mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

**Finding the binary path:**

```bash
# macOS/Linux
which mcp-grafana

# Windows (PowerShell)
(Get-Command mcp-grafana).Path
```

## Docker Configuration

### Basic Docker Setup

```json
{
  "mcpServers": {
    "grafana": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "-e",
        "GRAFANA_URL",
        "-e",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN",
        "mcp/grafana",
        "-t",
        "stdio"
      ],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

> **Important:** The `-t stdio` argument is essential to override the default SSE mode in the Docker image.

### Docker with Grafana Cloud

```json
{
  "mcpServers": {
    "grafana": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "-e",
        "GRAFANA_URL",
        "-e",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN",
        "mcp/grafana",
        "-t",
        "stdio"
      ],
      "env": {
        "GRAFANA_URL": "https://myinstance.grafana.net",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-cloud-token>"
      }
    }
  }
}
```

### Docker on Windows

For Windows, use `host.docker.internal` to access localhost:

```json
{
  "mcpServers": {
    "grafana": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "-e",
        "GRAFANA_URL",
        "-e",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN",
        "mcp/grafana",
        "-t",
        "stdio"
      ],
      "env": {
        "GRAFANA_URL": "http://host.docker.internal:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

## Authentication Options

### Service Account Token (Recommended)

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_xxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
      }
    }
  }
}
```

### Username and Password

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_USERNAME": "admin",
        "GRAFANA_PASSWORD": "admin"
      }
    }
  }
}
```

> **Note:** Service account tokens are more secure and recommended for production use.

## Advanced Configuration

### With Debug Mode

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": ["--debug"],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

### With Disabled Tool Categories

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [
        "--disable-oncall",
        "--disable-incident",
        "--disable-sift"
      ],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

### With Specific Tools Enabled

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [
        "--enabled-tools",
        "search_dashboards,get_dashboard_by_uid,query_prometheus,query_loki_logs"
      ],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

### With Client TLS Certificates

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [
        "--tls-cert-file",
        "/path/to/client.crt",
        "--tls-key-file",
        "/path/to/client.key",
        "--tls-ca-file",
        "/path/to/ca.crt"
      ],
      "env": {
        "GRAFANA_URL": "https://secure-grafana.example.com",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

### Docker with TLS Certificates

```json
{
  "mcpServers": {
    "grafana": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "-v",
        "/path/to/certs:/certs:ro",
        "-e",
        "GRAFANA_URL",
        "-e",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN",
        "mcp/grafana",
        "-t",
        "stdio",
        "--tls-cert-file",
        "/certs/client.crt",
        "--tls-key-file",
        "/certs/client.key",
        "--tls-ca-file",
        "/certs/ca.crt"
      ],
      "env": {
        "GRAFANA_URL": "https://secure-grafana.example.com",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

## Multiple Grafana Instances

You can configure multiple Grafana instances:

```json
{
  "mcpServers": {
    "grafana-prod": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "https://prod.grafana.net",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<prod-token>"
      }
    },
    "grafana-dev": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<dev-token>"
      }
    }
  }
}
```

## Platform-Specific Examples

### macOS

```json
{
  "mcpServers": {
    "grafana": {
      "command": "/usr/local/bin/mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

### Windows

```json
{
  "mcpServers": {
    "grafana": {
      "command": "C:\\Program Files\\mcp-grafana\\mcp-grafana.exe",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

### Linux

```json
{
  "mcpServers": {
    "grafana": {
      "command": "/usr/local/bin/mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "http://localhost:3000",
        "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
      }
    }
  }
}
```

## Testing Your Configuration

After updating the configuration:

1. **Restart Claude Desktop** - Close and reopen the application
2. **Check the MCP section** - Look for the Grafana server in the tools list
3. **Test a simple query** - Try asking Claude to list your dashboards
4. **Check logs** - If issues occur, check Claude Desktop logs:
   - **macOS:** `~/Library/Logs/Claude/`
   - **Windows:** `%APPDATA%\Claude\logs\`
   - **Linux:** `~/.config/Claude/logs/`

## Troubleshooting

### Binary Not Found

**Error:** `Error: spawn mcp-grafana ENOENT`

**Solution:** Use the full path to the binary in the `command` field.

### Connection Refused

**Error:** Connection to Grafana fails

**Solutions:**
- Verify Grafana is running: `curl http://localhost:3000/api/health`
- Check GRAFANA_URL is correct
- For Docker on macOS/Windows, use `host.docker.internal` instead of `localhost`

### Invalid Token

**Error:** 401 Unauthorized

**Solutions:**
- Verify the service account token is correct
- Check the token hasn't expired
- Ensure the service account has necessary permissions

### Docker Issues

**Error:** Docker-related errors

**Solutions:**
- Ensure Docker is running: `docker ps`
- Pull the image: `docker pull mcp/grafana`
- Check the `-t stdio` argument is included

## Next Steps

- [Configure TLS](../CONFIGURATION.md#tls-configuration)
- [Set up RBAC permissions](../RBAC.md)
- [Explore available features](../FEATURES.md)
- [Review troubleshooting guide](../TROUBLESHOOTING.md)