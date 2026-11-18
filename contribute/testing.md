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

For owner-level coverage, run `npm run test:coverage` from `ui/mcp-apps`. See the
[coverage reporting README](../ui/mcp-apps/scripts/codeowners-coverage/README.md)
for report scope, planned shared-package extraction, and nightly Bench setup.

## Frontend test conventions

When writing or reviewing frontend tests, read the
[frontend testing strategy](../.claude/skills/frontend-testing-strategy/SKILL.md) if not already loaded.
Tests live in `ui/mcp-apps/test/**/*.test.{ts,tsx}` with jsdom and Testing Library.
Use Vitest's `vi` APIs, not Jest globals. No skill read is needed merely to run existing tests.

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
