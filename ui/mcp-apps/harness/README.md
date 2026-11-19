# Shared browser host

Run `npm run dev` after building `dist/mcp-grafana` at the repository root.
Open http://127.0.0.1:5194 and select a scenario or visualization. Playwright uses
this same entry point and host implementation.

- `host.ts`: sandbox bootstrap, bridge callbacks, controls, and diagnostics.
- `scenarios.ts`: deterministic tool input/result delivery and refresh fixtures.
- `metricsFixtures.ts`: synthetic Prometheus examples for interactive exploration.
- `vite.config.ts`: real Go MCP catalog and resource proxy.
- `basic-host/`: pinned, hash-verified upstream sandbox and HTTP CSP server.

Tests and screenshot baselines live in `../e2e/specs`. See
[the browser test guide](../e2e/README.md) for sandbox guarantees and baseline updates.
