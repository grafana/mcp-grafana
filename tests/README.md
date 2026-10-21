# Tests

This directory contains two suites that run the real `mcp-grafana` binary
against the docker-compose stack:

- **E2E tests** (`pytest -m "not llm"`, e.g. `protocol_test.py`) call tools
  directly over the MCP protocol and assert on the seeded data. They are
  deterministic, need no API keys, and run on every PR over all three
  transports (`.github/workflows/e2e.yml`).
- **LLM evals** (`pytest -m llm`) drive real models through a tool-calling loop
  and grade the answers with [`deepeval`] (`MCPUseMetric` and `GEval`). They
  are non-deterministic and cost money, so they run nightly and on PRs that
  change `tests/` (`.github/workflows/llm-evals.yml`), never as a required
  check. Any test that takes a `model` parameter is marked `llm` automatically.

When an eval's pass condition is really a string match ("mentions Email1"), use
`assert_mentions` rather than a `GEval` criterion: it cannot flake.

Tests use [`uv`] to manage dependencies.

## Running

1. Install the dependencies:
   ```bash
   uv sync --all-groups
   ```

2. Start the docker-compose services (`make run-test-services` from the repo
   root) and build the binary (`go build -o dist/mcp-grafana ./cmd/mcp-grafana`).

3. For the SSE and streamable-http transports, start the server; from the repo
   root:
   ```bash
   ./dist/mcp-grafana -t sse --enabled-tools search,datasource,incident,prometheus,loki,elasticsearch,influxdb,alerting,dashboard,folder,oncall,asserts,pyroscope,navigation,tempo,annotations,rendering,admin,sql,cloudwatch
   ```
   The stdio transport spawns `../dist/mcp-grafana` itself.

4. Run the E2E tests:
   ```bash
   GRAFANA_USERNAME=admin GRAFANA_PASSWORD=admin MCP_TRANSPORT=sse uv run pytest -m "not llm"
   ```

5. For the LLM evals, also create a `.env` file with your API keys, then run
   `uv run pytest -m llm`:
   ```env
   OPENAI_API_KEY=sk-...
   ANTHROPIC_API_KEY=sk-ant-...
   ```

`GRAFANA_URL` (default `http://localhost:3000`), `MCP_GRAFANA_URL` (default
`http://localhost:8000`) and `MCP_TRANSPORT` (`sse`, `streamable-http` or
`stdio`) point the tests at a different setup.

[`deepeval`]: https://github.com/confident-ai/deepeval
[`uv`]: https://docs.astral.sh/uv/
