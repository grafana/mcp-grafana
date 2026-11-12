# Tests

This directory contains end-to-end tests that run the real `mcp-grafana`
binary against the docker-compose stack. They call tools directly over the MCP
protocol and assert on the seeded data, and run on every PR over all three
transports (`.github/workflows/e2e.yml`).

Tests use [`uv`] to manage dependencies.

## Running

1. Install the dependencies:
   ```bash
   uv sync --all-groups
   ```

2. Start the docker-compose services (`mise run run-test-services` from the repo
   root) and build the binary (`go build -o dist/mcp-grafana ./cmd/mcp-grafana`).

3. For the SSE and streamable-http transports, start the server; from the repo
   root:
   ```bash
   ./dist/mcp-grafana -t sse --enabled-tools search,datasource,incident,prometheus,loki,elasticsearch,influxdb,alerting,dashboard,folder,oncall,asserts,pyroscope,navigation,tempo,annotations,rendering,admin,sql,cloudwatch
   ```
   The stdio transport spawns `../dist/mcp-grafana` itself.

4. Run the E2E tests:
   ```bash
   GRAFANA_USERNAME=admin GRAFANA_PASSWORD=admin MCP_TRANSPORT=sse uv run pytest
   ```

`GRAFANA_URL` (default `http://localhost:3000`), `MCP_GRAFANA_URL` (default
`http://localhost:8000`) and `MCP_TRANSPORT` (`sse`, `streamable-http` or
`stdio`) point the tests at a different setup.

[`uv`]: https://docs.astral.sh/uv/
