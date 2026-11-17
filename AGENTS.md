# Agent guidance

## Contribution workflow

Read [CONTRIBUTING.md](CONTRIBUTING.md) and applicable directory instructions before editing.
Keep changes focused and follow surrounding conventions. Add comments for non-obvious reasons,
not to narrate self-explanatory code. Read [contribute/testing.md](contribute/testing.md) before
writing or running tests; it routes to the checked-in shared testing skills and local commands.

Use the contributor's configured signing identity for every commit. Never disable signing to get
past an error or fabricate an identity. Preserve authorship; get explicit approval before rewriting
published history. Follow the signing setup in CONTRIBUTING.md.

Before `git push`, stop, summarize changes and validation, and obtain explicit human approval.
A request to open a PR does not by itself authorize pushing. Report unavailable or skipped checks.

## Repository essentials

- Go server and tools: use `mise` and [DEVELOPING.md](DEVELOPING.md).
- Frontend: `ui/mcp-apps` uses React, Vitest, and Testing Library; `ui/panel-viewer` has a separate build.
  Read [contribute/testing.md](contribute/testing.md) for commands and committed bundle requirements.
- Preserve issue-first proposals for new tools and tool shape/name changes, token-budget constraints,
  read/write/query gates, RBAC declarations, and generated permission documentation in CONTRIBUTING.md.
- Ordinary contributions do not edit CHANGELOG.md. Release preparation is a separate workflow.
- Do not claim cloud checks passed without credentials. Use local unit checks and report limitations.
