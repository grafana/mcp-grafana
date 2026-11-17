# Testing contributions

Read the checked-in [frontend testing strategy](../.claude/skills/frontend-testing-strategy/SKILL.md).
For chart rendering and interactions, also read the
[panel testing strategy](../.claude/skills/panel-testing-strategy/SKILL.md).
These Markdown guides require no agent installation or private access. Local commands and fixtures
below specialize those principles. Provenance and content hashes live in
[shared-testing-source.json](../.claude/skills/shared-testing-source.json); publication of the shared
source is pending. Update shared copies together through reviewed PRs, preserving the adjacent license.


## MCP Apps frontend

From `ui/mcp-apps`:

```bash
npm ci --ignore-scripts
npm run typecheck
npm test
```

`npm test` runs Vitest once; tests live in `test/**/*.test.{ts,tsx}` with jsdom and Testing Library.
Use Vitest's `vi` APIs, not Jest globals. Type checking and test setup download the pinned Renderer;
network access is required. Keep the existing provenance verification and renderer fixtures.

From the repository root, run `mise run build-ui` after UI source changes and commit the resulting
`ui/*/dist` artifacts. CI rebuilds and checks for drift. This builds both `ui/mcp-apps` and
`ui/panel-viewer`; panel-viewer currently has no dedicated test script. Do not report it as tested
by the MCP Apps suite. Validate its changed behavior manually in the host and report results.

## Server and integration

Use `mise run lint` and `mise run test-unit`. For integration checks, start services with
`mise run run-test-services`, then run `mise run test-integration`. Cloud checks require credentials;
see [DEVELOPING.md](../DEVELOPING.md). Shared frontend guidance does not replace Go test conventions.
