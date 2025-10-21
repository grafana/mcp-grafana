# VSCode Configuration Examples

This guide provides configuration examples for using the Grafana MCP server with VSCode.

## Prerequisites

- VSCode with MCP support enabled
- Grafana MCP server installed (binary, Docker, or running remotely)

## Configuration File Location

VSCode MCP configuration is stored in `.vscode/settings.json` within your workspace or in your user settings.

## Remote Server Configuration (SSE Mode)

### Basic Remote Server

If you're running the MCP server separately in SSE mode:

**Start the server:**

```bash
mcp-grafana -t sse --address :8000
```

**VSCode settings.json:**

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "sse",
        "url": "http://localhost:8000/sse"
      }
    }
  }
}
```

### Remote Server with Custom Port

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "sse",
        "url": "http://localhost:9090/sse"
      }
    }
  }
}
```

### HTTPS Remote Server

If using server TLS:

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "sse",
        "url": "https://localhost:8443/sse"
      }
    }
  }
}
```

### Remote Server on Different Host

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "sse",
        "url": "http://mcp-server.example.com:8000/sse"
      }
    }
  }
}
```

## Local Binary Configuration

### Using stdio Mode

If you want VSCode to manage the MCP server process:

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
        "command": "mcp-grafana",
        "args": [],
        "env": {
          "GRAFANA_URL": "http://localhost:3000",
          "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
        }
      }
    }
  }
}
```

### With Full Binary Path

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
        "command": "/usr/local/bin/mcp-grafana",
        "args": [],
        "env": {
          "GRAFANA_URL": "http://localhost:3000",
          "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
        }
      }
    }
  }
}
```

### With Debug Mode

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
        "command": "mcp-grafana",
        "args": ["--debug"],
        "env": {
          "GRAFANA_URL": "http://localhost:3000",
          "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
        }
      }
    }
  }
}
```

## Docker Configuration

### Docker with stdio Mode

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
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
}
```

### Docker SSE Server

**Start the Docker container:**

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://host.docker.internal:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana
```

**VSCode settings.json:**

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "sse",
        "url": "http://localhost:8000/sse"
      }
    }
  }
}
```

## Advanced Configuration

### With Disabled Tool Categories

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
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
}
```

### With TLS Certificates

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
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
}
```

### Grafana Cloud Configuration

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
        "command": "mcp-grafana",
        "args": [],
        "env": {
          "GRAFANA_URL": "https://myinstance.grafana.net",
          "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-cloud-token>"
        }
      }
    }
  }
}
```

## Multiple Grafana Instances

Configure multiple Grafana instances in the same workspace:

```json
{
  "mcp": {
    "servers": {
      "grafana-prod": {
        "type": "sse",
        "url": "http://prod-mcp:8000/sse"
      },
      "grafana-dev": {
        "type": "stdio",
        "command": "mcp-grafana",
        "args": [],
        "env": {
          "GRAFANA_URL": "http://localhost:3000",
          "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<dev-token>"
        }
      }
    }
  }
}
```

## Workspace vs User Settings

### Workspace Settings

Store configuration in `.vscode/settings.json` in your project root for project-specific Grafana instances:

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
        "command": "mcp-grafana",
        "args": [],
        "env": {
          "GRAFANA_URL": "http://localhost:3000",
          "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<project-token>"
        }
      }
    }
  }
}
```

### User Settings

Store configuration in your user settings for global access across all workspaces:

1. Open Command Palette (Cmd/Ctrl + Shift + P)
2. Select "Preferences: Open User Settings (JSON)"
3. Add MCP configuration

## Platform-Specific Examples

### macOS

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
        "command": "/usr/local/bin/mcp-grafana",
        "args": [],
        "env": {
          "GRAFANA_URL": "http://localhost:3000",
          "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
        }
      }
    }
  }
}
```

### Windows

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
        "command": "C:\\Program Files\\mcp-grafana\\mcp-grafana.exe",
        "args": [],
        "env": {
          "GRAFANA_URL": "http://localhost:3000",
          "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
        }
      }
    }
  }
}
```

### Linux

```json
{
  "mcp": {
    "servers": {
      "grafana": {
        "type": "stdio",
        "command": "/usr/local/bin/mcp-grafana",
        "args": [],
        "env": {
          "GRAFANA_URL": "http://localhost:3000",
          "GRAFANA_SERVICE_ACCOUNT_TOKEN": "<your-token>"
        }
      }
    }
  }
}
```

## Testing Your Configuration

### Verify Server Connection

1. **Open Command Palette** (Cmd/Ctrl + Shift + P)
2. **Search for MCP commands** to verify the server is connected
3. **Check Output panel** for MCP-related logs

### Test with AI Assistant

If using VSCode with an AI assistant that supports MCP:

1. Ask the assistant to list Grafana dashboards
2. Try querying Prometheus metrics
3. Request dashboard summaries

### Check Logs

Look for MCP server logs in:
- VSCode Output panel (select "MCP" from dropdown)
- Terminal panel if running server separately
- Docker logs if using Docker: `docker logs <container-id>`

## Troubleshooting

### Server Not Starting

**Symptom:** MCP server doesn't appear in VSCode

**Solutions:**
- Check the binary path is correct
- Verify environment variables are set
- Check VSCode Output panel for errors
- Restart VSCode

### Connection Refused

**Symptom:** Cannot connect to remote MCP server

**Solutions:**
- Verify the server is running: `curl http://localhost:8000/healthz`
- Check the URL in settings is correct
- Ensure firewall allows connection
- Verify port is not blocked

### Authentication Fails

**Symptom:** 401 or 403 errors

**Solutions:**
- Verify service account token is correct
- Check token hasn't expired
- Ensure proper RBAC permissions are set
- Test with curl: `curl -H "Authorization: Bearer <token>" <grafana-url>/api/datasources`

### Docker Issues

**Symptom:** Docker container fails to start or connect

**Solutions:**
- Ensure Docker is running
- Use `host.docker.internal` instead of `localhost` for Grafana URL
- Check the `-t stdio` flag is included
- Verify image is pulled: `docker pull mcp/grafana`

### Environment Variables Not Set

**Symptom:** Server starts but can't connect to Grafana

**Solutions:**
- Verify env variables are in the correct format
- Check for typos in variable names
- Ensure no trailing spaces in values
- Use debug mode to see actual values: `--debug`

## Best Practices

1. **Use workspace settings** for project-specific configurations
2. **Use user settings** for personal Grafana instances
3. **Store tokens securely** - Consider using environment variables from shell
4. **Enable debug mode** during initial setup
5. **Use SSE mode** for long-running sessions to avoid repeated startups
6. **Disable unused tools** to reduce context window usage

## Next Steps

- [Configure TLS certificates](../CONFIGURATION.md#tls-configuration)
- [Set up RBAC permissions](../RBAC.md)
- [Explore available features](../FEATURES.md)
- [Review troubleshooting guide](../TROUBLESHOOTING.md)