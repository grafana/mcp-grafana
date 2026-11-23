# Testing contributions

## Frontend validation

From `ui/mcp-apps`:

```bash
npm ci --ignore-scripts
npm run typecheck
npm test
```

The test command runs Vitest once. Type checking and test setup download the pinned Renderer.
Network access is required. Keep the existing provenance verification and renderer fixtures.

From the repository root, run `mise run build-ui` after UI source changes and commit the resulting
`ui/*/dist` artifacts. CI rebuilds and checks for drift. This builds both `ui/mcp-apps` and
`ui/panel-viewer`. Panel-viewer has no dedicated unit test script. Its browser coverage runs through the
shared Playwright suite described below; the MCP Apps Vitest suite does not cover it.

## Frontend test conventions

When writing or reviewing frontend tests, read the
[frontend testing strategy](../.claude/skills/frontend-testing-strategy/SKILL.md) if not already loaded.
Tests live in `ui/mcp-apps/test/**/*.test.{ts,tsx}` with jsdom and Testing Library.
Use Vitest's `vi` APIs, not Jest globals. No skill read is needed merely to run existing tests.

## Reviewing MCP App changes

Unit tests run in jsdom with a mock Renderer. They do not show a layout that breaks, a control that
does not respond, or a host that refuses a call. A review of a PR that changes source under
`ui/mcp-apps` runs the App before a verdict. A PR that changes only tests or docs there is exempt.

1. Check out the PR head. Both hosts below show the committed bundles in `ui/mcp-apps/dist`.
   Run `mise run build-ui` first if the bundle drift check of the PR has not passed.
2. Open the App in a host:
   - **Preview host.** From `ui/mcp-apps`, run `npm ci --ignore-scripts`, then `npm run dev`.
     Go must be on the PATH, because the host builds the Go server. Open http://127.0.0.1:5194
     and select a scenario: chart, trace, panel, error, or a visualization fixture.
     The fixtures do not query Grafana.
   - **Real host.** From the repository root, run `mise run run-app [port]`. The default port is 8000.
     Connect the host to `http://localhost:<port>/mcp`. The task enables each source of
     `render_grafana_app`, `query_datasource` included. It uses the test stack of
     `mise run run-test-services`. For another local Grafana, set `GRAFANA_URL` in the shell.
     For a live instance, set `GRAFANA_URL` and `GRAFANA_SERVICE_ACCOUNT_TOKEN`.
3. Use the changed behavior. For example, pick another chart, zoom out, change the time range,
   or click Refresh.

The preview host is enough for most changes. Use a real host when the PR changes
a file that talks to the host:
`ui/mcp-apps/app/loadRenderer.ts`, `ui/mcp-apps/app/appHostState.ts`, `ui_grafana.go`, `ui_apps.go`,
or the `_meta.ui` values in `tools/app.go`.

The review states what the reviewer ran in the App, in which host, and what the reviewer did not run.
A review with no App run does not approve. It can still comment on the code. An agent reviewer can
use a headless browser run of the preview host with a screenshot. The review then says so.

## Visualization adapter

For chart rendering and interactions, also read the
[panel testing strategy](../.claude/skills/panel-testing-strategy/SKILL.md) if not already loaded.
Use the Renderer fixtures and setup in `ui/mcp-apps/test/` and the existing Vitest configuration.
Do not substitute Grafana core's panel-props helpers or automatic E2E server harness.
Preserve the distinction between MCP Apps unit coverage and shared browser coverage of panel-viewer.

## Server and integration

Read only "Testing" or "Linting" in [DEVELOPING.md](../DEVELOPING.md) for server validation commands.
Cloud checks require credentials. The frontend skills do not replace Go test conventions.

## Provenance

[shared-testing-source.json](../.claude/skills/shared-testing-source.json) records the original source
and hashes of these locally adapted copies. The hashes do not assert equality with upstream files.
No automated drift check consumes them. Update them when changing the local skills.
Publication of a canonical shared repository is a follow-up. Preserve the
[skill license](../.claude/skills/SHARED-TESTING-LICENSE) when distributing them.

## Browser host contracts

Chromium Playwright tests now cover both built MCP Apps and panel-viewer using
real AppBridge handshakes and resources served by the built Go binary. See the
[browser test guide](../ui/mcp-apps/e2e/README.md) for local execution, fixture
boundaries, diagnostics, and the resource-policy regression test for #1654.
The secret-free Browser E2E Tests workflow runs on pull requests.

For intentional visual changes, see [regenerating one chart or all screenshot baselines](../ui/mcp-apps/e2e/README.md#regenerate-one-chart-or-all-charts), including the Linux CI container command and verification steps.
