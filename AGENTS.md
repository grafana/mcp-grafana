# mcp-grafana Agent Guidelines

## Overview

Go-based MCP server for Grafana. Single binary (`cmd/mcp-grafana`), no mise/tool-version manager. Uses `Makefile` for tasks.

## Quality Commands

- `make lint` — runs golangci-lint + custom linters (jsonschema, openapi)
- `make test-unit` — unit tests (no external deps)
- `make test-integration` — requires `docker compose up -d` services running
- `make test-python-e2e` — requires services + SSE server + `uv` (Python)
- `make build` — builds binary to `dist/mcp-grafana`

## Cursor Cloud specific instructions

### Services overview

The Docker Compose stack (`docker-compose.yaml`) provides a full test Grafana instance with multiple datasources: Prometheus (:9090), Loki (:3100), Tempo (:3200/:3201), Alertmanager (:9093), Pyroscope (:4040), Elasticsearch (:9200), OpenSearch (:9201), InfluxDB (:8086), ClickHouse (:8123), Graphite, and LocalStack (:4566). Grafana runs on :3000.

### Running tests

- Unit tests: `go test -tags unit ./...` (no dependencies)
- Build: `go build -o /dev/null ./cmd/mcp-grafana`
- The MCP server supports `--transport=stdio` for testing without network

### Gotchas

- golangci-lint requires a Go version >= the project's `go.mod` version (1.26.1). As of May 2026, golangci-lint is built with Go 1.25, causing `"can't load config"` errors. The custom linters (jsonschema, openapi) still work via `go run`.
- The `$HOME/go/bin` directory needs to be in PATH for `golangci-lint` if installed via `go install`.
- Integration/E2E tests require the Docker Compose services; unit tests (`-tags unit`) do not.
- Python E2E tests require `uv` (`cd tests && uv sync --all-groups && uv run pytest`).
