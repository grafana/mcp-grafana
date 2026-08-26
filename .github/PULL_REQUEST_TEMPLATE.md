<!--
Thanks for contributing! A few notes before you submit:

  - Adding a new tool, or changing a tool's name or input/output shape?
    Please open a new tool proposal issue first and link it below.
    See CONTRIBUTING.md — we'd rather discuss the idea than decline a finished PR.

  - Bug fix, docs, tests, or a new parameter on an existing tool?
    No issue needed. Delete the "New or changed tools" section and send it.
-->

## What this changes

<!-- One or two sentences. What problem does this solve, and for whom? -->

Fixes #

## New or changed tools

<!-- Delete this whole section if you aren't adding or changing a tool. -->

**Proposal issue:** #

| Tool name | New or changed | Proposed tier |
| --- | --- | --- |
| `` | new / changed | default-on / opt-in category |

**Token cost:** <!-- From the Token Analysis check's run summary, or `make token-check` locally. Please paste the change if this adds a default-on tool — it's the most useful number for reviewing the proposal. -->

**Why not extend an existing tool?** <!-- Only needed for new tools. -->

**Read-only safety:** <!-- If this tool writes anything, confirm it's gated behind `--disable-write`. -->

## How you tested it

<!--
What did you actually run this against? A real Grafana instance, the docker-compose
test stack, unit tests only? Reviewers here often test against a live instance,
so anything that helps them reproduce your setup is genuinely useful.
-->

## Checklist

- [ ] `make lint` and `make test-unit` pass locally
- [ ] Commits are signed (required to merge — see CONTRIBUTING.md)
- [ ] CLA signed (required to merge)
- [ ] README tool table updated, with RBAC permissions and scopes, if this adds a tool
- [ ] Any write operation respects `--disable-write`
- [ ] Any datasource query execution respects `--disable-query` (and `--disable-write` too, if the query is passed through unfiltered)

<!--
Note on CI: workflow runs on fork PRs need manual maintainer approval, on every
push. If your checks look stuck, they're waiting on us, not on you.
`Test Cloud` and the Python E2E suites are informational and cannot pass from a
fork — a red mark there is expected and won't block your merge.
-->
