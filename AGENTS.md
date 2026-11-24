# Agent guidance

Go server and tools live at the root. `ui/mcp-apps` uses React, Vitest, and Testing Library.
`ui/panel-viewer` has a separate build and no dedicated test script.

## Read only when needed

- Server setup, build, or existing tests: "Prerequisites", "Building", "Testing", or "Linting" in DEVELOPING.md.
- Existing frontend tests or UI source changes: "Frontend validation" in contribute/testing.md.
  UI bundles are committed for Go embedding and must be rebuilt after UI source changes.
- Reviewing a PR that changes `ui/mcp-apps` source: "Reviewing MCP App changes" in contribute/testing.md.
  Approval needs browser evidence of the changed behavior: relevant Playwright results or a run of the App.
- Writing or reviewing frontend tests: "Frontend test conventions" in contribute/testing.md.
- Visualization tests: "Visualization adapter" in contribute/testing.md.
- New tools or tool name/shape changes: "New tools need an issue first" and "The three tiers" in CONTRIBUTING.md.
- Tool catalog or schema changes: "Conventions that CI enforces" in CONTRIBUTING.md for gates, RBAC, and schema rules.
- Tool descriptions or schema growth: "Measuring the token cost" in CONTRIBUTING.md.
- Signing error: [Signed commits](CONTRIBUTING.md#signed-commits). Never disable signing.

## Git

- Sign every commit with the contributor's configured identity.
- Get explicit approval before `git push` and before rewriting published history.
  A request to open a PR does not authorize a push.
- Ordinary contributions do not edit CHANGELOG.md. Release preparation is a separate workflow.
- Report skipped or unavailable checks. Cloud checks require credentials.
