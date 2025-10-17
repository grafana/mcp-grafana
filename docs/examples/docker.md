# Docker Configuration Examples

This guide provides comprehensive Docker usage examples for the Grafana MCP server.

## Table of Contents

- [Basic Usage](#basic-usage)
- [Transport Modes](#transport-modes)
- [Authentication](#authentication)
- [Docker Compose](#docker-compose)
- [TLS Configuration](#tls-configuration)
- [Production Deployment](#production-deployment)
- [Troubleshooting](#troubleshooting)

## Basic Usage

### Pull the Image

```bash
docker pull mcp/grafana
```

### Quick Start (STDIO Mode)

For local AI assistants like Claude Desktop:

```bash
docker run --rm -i \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t stdio
```

> **Important:** The `-i` flag keeps stdin open, and `-t stdio` overrides the default SSE mode.

### Quick Start (SSE Mode)

For remote clients:

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana
```

> **Note:** SSE mode is the default for the Docker image.

## Transport Modes

### STDIO Mode (Interactive)

Used with AI assistants that communicate via stdin/stdout:

```bash
docker run --rm -i \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t stdio
```

**Use cases:**
- Claude Desktop
- Local AI assistant integrations
- Testing with MCP Inspector

### SSE Mode (HTTP Server)

Default mode, runs as an HTTP server:

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana
```

**Use cases:**
- Remote client connections
- VSCode with remote MCP
- Multiple concurrent clients

### Streamable HTTP Mode

For production deployments with streaming support:

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t streamable-http
```

**Use cases:**
- Production environments
- Load-balanced setups
- High-availability deployments

### Custom Port

```bash
docker run --rm -p 9090:9090 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t sse --address :9090
```

## Authentication

### Service Account Token

**Recommended method:**

```bash
docker run --rm -i \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=glsa_xxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
  mcp/grafana -t stdio
```

### Username and Password

```bash
docker run --rm -i \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_USERNAME=admin \
  -e GRAFANA_PASSWORD=admin \
  mcp/grafana -t stdio
```

### Grafana Cloud

```bash
docker run --rm -i \
  -e GRAFANA_URL=https://myinstance.grafana.net \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-cloud-token> \
  mcp/grafana -t stdio
```

## Docker Compose

### Basic Setup

Create `docker-compose.yml`:

```yaml
version: '3.8'

services:
  mcp-grafana:
    image: mcp/grafana
    container_name: mcp-grafana
    ports:
      - "8000:8000"
    environment:
      - GRAFANA_URL=http://grafana:3000
      - GRAFANA_SERVICE_ACCOUNT_TOKEN=${GRAFANA_SERVICE_ACCOUNT_TOKEN}
    depends_on:
      - grafana

  grafana:
    image: grafana/grafana:latest
    container_name: grafana
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
    volumes:
      - grafana-data:/var/lib/grafana

volumes:
  grafana-data:
```

**Usage:**

```bash
# Create .env file
echo "GRAFANA_SERVICE_ACCOUNT_TOKEN=your-token" > .env

# Start services
docker-compose up -d

# Check health
curl http://localhost:8000/healthz

# View logs
docker-compose logs -f mcp-grafana

# Stop services
docker-compose down
```

### With Prometheus and Loki

```yaml
version: '3.8'

services:
  mcp-grafana:
    image: mcp/grafana
    container_name: mcp-grafana
    ports:
      - "8000:8000"
    environment:
      - GRAFANA_URL=http://grafana:3000
      - GRAFANA_SERVICE_ACCOUNT_TOKEN=${GRAFANA_SERVICE_ACCOUNT_TOKEN}
    depends_on:
      - grafana

  grafana:
    image: grafana/grafana:latest
    container_name: grafana
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
    volumes:
      - grafana-data:/var/lib/grafana
      - ./provisioning:/etc/grafana/provisioning
    depends_on:
      - prometheus
      - loki

  prometheus:
    image: prom/prometheus:latest
    container_name: prometheus
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
      - prometheus-data:/prometheus

  loki:
    image: grafana/loki:latest
    container_name: loki
    ports:
      - "3100:3100"
    volumes:
      - ./loki-config.yaml:/etc/loki/local-config.yaml
      - loki-data:/loki

volumes:
  grafana-data:
  prometheus-data:
  loki-data:
```

### With Debug Mode

```yaml
version: '3.8'

services:
  mcp-grafana:
    image: mcp/grafana
    container_name: mcp-grafana
    ports:
      - "8000:8000"
    command: ["--debug"]
    environment:
      - GRAFANA_URL=http://grafana:3000
      - GRAFANA_SERVICE_ACCOUNT_TOKEN=${GRAFANA_SERVICE_ACCOUNT_TOKEN}
    depends_on:
      - grafana

  grafana:
    image: grafana/grafana:latest
    container_name: grafana
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
    volumes:
      - grafana-data:/var/lib/grafana

volumes:
  grafana-data:
```

## TLS Configuration

### Client TLS (Connecting to Grafana)

Mount certificates as read-only volumes:

```bash
docker run --rm -i \
  -v /path/to/certs:/certs:ro \
  -e GRAFANA_URL=https://secure-grafana.example.com \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana \
  -t stdio \
  --tls-cert-file /certs/client.crt \
  --tls-key-file /certs/client.key \
  --tls-ca-file /certs/ca.crt
```

### Server TLS (HTTPS for MCP Server)

```bash
docker run --rm -p 8443:8443 \
  -v /path/to/certs:/certs:ro \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana \
  -t streamable-http \
  --address :8443 \
  --server.tls-cert-file /certs/server.crt \
  --server.tls-key-file /certs/server.key
```

### Docker Compose with TLS

```yaml
version: '3.8'

services:
  mcp-grafana:
    image: mcp/grafana
    container_name: mcp-grafana
    ports:
      - "8443:8443"
    command:
      - "-t"
      - "streamable-http"
      - "--address"
      - ":8443"
      - "--server.tls-cert-file"
      - "/certs/server.crt"
      - "--server.tls-key-file"
      - "/certs/server.key"
      - "--tls-cert-file"
      - "/certs/client.crt"
      - "--tls-key-file"
      - "/certs/client.key"
      - "--tls-ca-file"
      - "/certs/ca.crt"
    environment:
      - GRAFANA_URL=https://secure-grafana.example.com
      - GRAFANA_SERVICE_ACCOUNT_TOKEN=${GRAFANA_SERVICE_ACCOUNT_TOKEN}
    volumes:
      - ./certs:/certs:ro
```

## Production Deployment

### With Health Checks

```yaml
version: '3.8'

services:
  mcp-grafana:
    image: mcp/grafana
    container_name: mcp-grafana
    ports:
      - "8000:8000"
    environment:
      - GRAFANA_URL=${GRAFANA_URL}
      - GRAFANA_SERVICE_ACCOUNT_TOKEN=${GRAFANA_SERVICE_ACCOUNT_TOKEN}
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8000/healthz"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
    restart: unless-stopped
```

### With Resource Limits

```yaml
version: '3.8'

services:
  mcp-grafana:
    image: mcp/grafana
    container_name: mcp-grafana
    ports:
      - "8000:8000"
    environment:
      - GRAFANA_URL=${GRAFANA_URL}
      - GRAFANA_SERVICE_ACCOUNT_TOKEN=${GRAFANA_SERVICE_ACCOUNT_TOKEN}
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 512M
        reservations:
          cpus: '0.25'
          memory: 256M
    restart: unless-stopped
```

### With Logging

```yaml
version: '3.8'

services:
  mcp-grafana:
    image: mcp/grafana
    container_name: mcp-grafana
    ports:
      - "8000:8000"
    environment:
      - GRAFANA_URL=${GRAFANA_URL}
      - GRAFANA_SERVICE_ACCOUNT_TOKEN=${GRAFANA_SERVICE_ACCOUNT_TOKEN}
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
    restart: unless-stopped
```

### Behind Reverse Proxy (nginx)

**docker-compose.yml:**

```yaml
version: '3.8'

services:
  mcp-grafana:
    image: mcp/grafana
    container_name: mcp-grafana
    expose:
      - "8000"
    environment:
      - GRAFANA_URL=${GRAFANA_URL}
      - GRAFANA_SERVICE_ACCOUNT_TOKEN=${GRAFANA_SERVICE_ACCOUNT_TOKEN}
    networks:
      - mcp-network
    restart: unless-stopped

  nginx:
    image: nginx:alpine
    container_name: nginx-proxy
    ports:
      - "443:443"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
      - ./certs:/etc/nginx/certs:ro
    depends_on:
      - mcp-grafana
    networks:
      - mcp-network
    restart: unless-stopped

networks:
  mcp-network:
    driver: bridge
```

**nginx.conf:**

```nginx
http {
    upstream mcp-server {
        server mcp-grafana:8000;
    }

    server {
        listen 443 ssl;
        server_name mcp.example.com;

        ssl_certificate /etc/nginx/certs/server.crt;
        ssl_certificate_key /etc/nginx/certs/server.key;

        location /sse {
            proxy_pass http://mcp-server/sse;
            proxy_http_version 1.1;
            proxy_set_header Connection "";
            proxy_buffering off;
            proxy_cache off;
        }

        location /healthz {
            proxy_pass http://mcp-server/healthz;
        }
    }
}
```

## Advanced Examples

### Disable Specific Tools

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana \
  --disable-oncall \
  --disable-incident \
  --disable-sift
```

### Enable Only Specific Tools

```bash
docker run --rm -p 8000:8000 \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana \
  --enabled-tools search_dashboards,get_dashboard_by_uid,query_prometheus
```

### Multiple Replicas (Docker Swarm)

```yaml
version: '3.8'

services:
  mcp-grafana:
    image: mcp/grafana
    ports:
      - "8000:8000"
    environment:
      - GRAFANA_URL=${GRAFANA_URL}
      - GRAFANA_SERVICE_ACCOUNT_TOKEN=${GRAFANA_SERVICE_ACCOUNT_TOKEN}
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        delay: 10s
      restart_policy:
        condition: on-failure
    networks:
      - mcp-network

networks:
  mcp-network:
    driver: overlay
```

## Troubleshooting

### Check Container Status

```bash
# List running containers
docker ps

# Check specific container
docker ps -f name=mcp-grafana

# View logs
docker logs mcp-grafana

# Follow logs
docker logs -f mcp-grafana

# Last 100 lines
docker logs --tail 100 mcp-grafana
```

### Test Connectivity

```bash
# Check health endpoint
curl http://localhost:8000/healthz

# From inside container
docker exec mcp-grafana curl localhost:8000/healthz

# Test Grafana connection from container
docker exec mcp-grafana curl $GRAFANA_URL/api/health
```

### Debug Mode

```bash
# Run with debug logging
docker run --rm -i \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t stdio --debug
```

### Access Grafana from Container

**Problem:** Container can't reach Grafana on localhost.

**Solution:** Use `host.docker.internal`:

```bash
docker run --rm -i \
  -e GRAFANA_URL=http://host.docker.internal:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t stdio
```

**Alternative (Linux):** Use host network mode:

```bash
docker run --rm -i --network host \
  -e GRAFANA_URL=http://localhost:3000 \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token> \
  mcp/grafana -t stdio
```

### Inspect Container

```bash
# View container details
docker inspect mcp-grafana

# Check environment variables
docker inspect mcp-grafana | jq '.[0].Config.Env'

# Get IP address
docker inspect mcp-grafana | jq '.[0].NetworkSettings.IPAddress'
```

### Clean Up

```bash
# Stop container
docker stop mcp-grafana

# Remove container
docker rm mcp-grafana

# Remove image
docker rmi mcp/grafana

# Clean up all stopped containers
docker container prune

# Clean up all unused images
docker image prune -a
```

## Best Practices

1. **Use environment variables** for sensitive data (tokens)
2. **Mount volumes as read-only** when possible (`:ro`)
3. **Enable health checks** for production deployments
4. **Set resource limits** to prevent resource exhaustion
5. **Use specific image tags** instead of `latest` in production
6. **Configure logging** to prevent disk space issues
7. **Use Docker secrets** for token management in Swarm/Kubernetes
8. **Enable restart policies** for automatic recovery
9. **Use networks** to isolate services
10. **Regular updates** - pull latest images periodically

## Next Steps

- [Kubernetes deployment](kubernetes.md)
- [Configure TLS](../CONFIGURATION.md#tls-configuration)
- [Set up RBAC permissions](../RBAC.md)
- [Review troubleshooting guide](../TROUBLESHOOTING.md)