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
`ui/panel-viewer`. Panel-viewer has no dedicated test script. Do not report it as tested by the
MCP Apps suite. Validate its changed behavior manually in the host and report results.

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
   - **Preview host.** From `ui/mcp-apps`, run `npm ci --ignore-scripts`, then
     `npx vite --config localhost.vite.config.ts`.
     At the URL that Vite prints, open `/localhost.html` for the metrics view, or
     `/localhost.html?fixture=viz` for the viz view.
     `npm run dev` serves the same page, but not the built App.
   - **Real host.** From the repository root, run `mise run run-app [port]`. The default port is 8000.
     Connect the host to `http://localhost:<port>/mcp`. The task enables each source of
     `render_grafana_app`, `query_datasource` included. It uses the test stack of
     `mise run run-test-services`. For another local Grafana, set `GRAFANA_URL` in the shell.
     For a live instance, set `GRAFANA_URL` and `GRAFANA_SERVICE_ACCOUNT_TOKEN`.
3. Use the changed behavior. For example, pick another chart, zoom out, change the time range,
   or click Refresh.

The preview host is enough for most changes. Use a real host when the PR changes the trace view,
which has no fixture in the preview host, or a file that talks to the host:
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
Preserve the distinction between MCP Apps coverage and manual panel-viewer validation.

## Server and integration

Read only "Testing" or "Linting" in [DEVELOPING.md](../DEVELOPING.md) for server validation commands.
Cloud checks require credentials. The frontend skills do not replace Go test conventions.

## Provenance

[shared-testing-source.json](../.claude/skills/shared-testing-source.json) records the original source
and hashes of these locally adapted copies. The hashes do not assert equality with upstream files.
No automated drift check consumes them. Update them when changing the local skills.
Publication of a canonical shared repository is a follow-up. Preserve the
[skill license](../.claude/skills/SHARED-TESTING-LICENSE) when distributing them.
