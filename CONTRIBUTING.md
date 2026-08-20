# Contributing to the Grafana MCP server

Thanks for wanting to contribute. This document covers **what belongs in this server and how to propose it**. For build, test and lint mechanics, see [DEVELOPING.md](DEVELOPING.md).

Please read the section below before writing a new tool. It will save you time, and it means we can say yes faster.

## The one thing to understand first

Every tool in the default set is sent to the model on **every request, by every user, forever**. A tool's name, description and JSON schema are spent out of a context budget that users also want to spend on their actual data.

That makes a new tool different from most contributions: it isn't free to add and then ignore. A tool nobody uses still costs everybody. This is a real, measured constraint, not a stylistic preference — CI reports the token cost of every PR, and shrinking the server is an [open request from users](https://github.com/grafana/mcp-grafana/issues/569).

So the interesting question is never only "does this work?" It's "**should everyone pay for this?**" — and there are three possible answers.

## New tools need an issue first

**Before you write a new tool, please [open a new tool proposal](https://github.com/grafana/mcp-grafana/issues/new?template=new-tool-proposal.yml) and wait for a maintainer to agree with the approach.**

We know this is friction, and we're asking for it anyway for one reason: **we would much rather say "not like this" to a three-paragraph issue than to your finished pull request.** Declining completed work is a bad outcome for everyone, and it has happened here more than once.

This applies to:

- net-new tools, and
- changes to a tool's name, or to the shape of its inputs or outputs.

It does **not** apply to (please just send a PR):

- bug fixes
- documentation
- tests
- performance and reliability work
- adding a parameter to an existing tool, where the tool's purpose doesn't change

If you have already written the code, that's fine — open the proposal anyway and link it. We'll review the idea before the diff.

## The three tiers

Not every good idea has to be in everyone's context window. The server already supports shipping a tool that costs nothing to people who don't want it, and **that is where most new tools should land**.

| Tier | How it ships | The bar | Cost to other users |
| --- | --- | --- | --- |
| **Default-on** | Registered in the default category set | Useful to *most* people using Grafana through an MCP client | Everyone pays, every request |
| **Opt-in category** | Behind `--enabled-tools` | Coherent, with a real audience | **Zero** |
| **Not in this server** | — | — | — |

The default-on bar is deliberately high, because it is the only tier that spends a shared budget. The opt-in bar is much lower: if your tool is genuinely useful to a subset of users, it can almost certainly ship as an opt-in category. That is not a consolation prize — 12 of the server's 33 categories already work this way, including `admin`, `cloudwatch`, `clickhouse`, `athena`, `snowflake`, `graphite`, `elasticsearch`, `quickwit`, `examples`, `runpanelquery`, `agento11y` and `assistant`.

Good reasons for a tool to be opt-in rather than default-on:

- it targets one datasource type that most users don't have
- it only works on Grafana Cloud, or only with a plugin installed
- it's for operators or administrators rather than everyday querying
- it's powerful enough that some users will want it off

If you're proposing something niche, **propose it as opt-in yourself**. It makes the decision much easier to say yes to.

## Prefer extending a tool over adding one

Each tool carries fixed overhead: a name, a description, and a schema, all of them permanent. An extra optional parameter on a tool that already exists carries almost none.

Before adding a tool, please check whether an existing one could answer the same question with one more argument, or whether several related operations could be a single tool with a `action`-style parameter. `alerting_manage_rules` and `alerting_manage_routing` are examples of the latter in this repo.

If two open PRs would add overlapping tools, we'd rather consolidate them before merging than ship both and deprecate one later. Searching [open pull requests](https://github.com/grafana/mcp-grafana/pulls) for your tool's area before you start is genuinely worth the two minutes — it has saved contributors here from duplicating each other's work.

## A note on AI-assisted contributions

These are welcome, and plenty of good contributions to this repo were written with an agent's help. Two requests:

1. **Please read and understand the diff before you send it.** We will ask questions about design decisions, and "the agent chose that" is a difficult place to review from.
2. **The tiering question above is the part an agent is least likely to get right.** An agent asked to add a feature will add a feature. Whether *everyone* should pay for it is a judgement about this project's users, and it needs a human — which is exactly why we ask for the issue first.

A PR that is easy to generate can still be expensive to review. The proposal step is how we keep that cost from landing on you as a rejection.

## Measuring the token cost

CI runs a **Token Analysis** check on every pull request, including from forks. It compares your branch against the current baseline and fails above a 5% increase.

To see the numbers, open that check's run and read its summary — you'll get the baseline, the new total, the change, and a per-tool breakdown of what was added or modified. It isn't posted as a comment, so it's worth knowing where to look.

If you want the number before you push, or you're weighing two designs against each other:

```bash
make token-baseline   # once, on main
make token-check      # on your branch
```

A tool's cost is almost entirely its description and its JSON schema. If your delta looks larger than you expected, that's usually where to trim — long parameter descriptions and deeply nested schemas are the expensive parts.

## Conventions that CI enforces

Run `make lint` and `make test-unit` before pushing. The specifics:

- **Signed commits are required.** Every commit must have a verified signature — this is [an organisation-wide Grafana policy](https://community.grafana.com/t/action-required-signed-commits-mandatory-for-all-grafana-repositories/163404). Setting this up once saves a round trip later; it is the most common reason a finished PR sits unmerged here.
- **The CLA must be signed.** It's a required check, so nothing can merge without it, however good the code is.
- **Conventional commit messages** — `feat(tools):`, `fix(loki):`, `docs:` and so on.
- **Commas inside `jsonschema` struct tags must be escaped.** There's a dedicated linter because it's easy to get wrong: `make lint-jsonschema-fix` will fix it for you.
- **No bare boolean sub-schemas.** An `any` or `interface{}` field makes the schema reflector emit `true`, which breaks some MCP clients outright. Tool creation panics if this happens; add the type to the reflector's `Mapper` in `tools.go`.
- **Pass `context.Context` through Grafana API client calls** — enforced by `make lint-openapi`.
- **Write tools must respect `--disable-write`.** Any tool that creates, updates or deletes anything must be registered so that it disappears when the server runs read-only. Take the `enableWriteTools` parameter in your category's `Add*Tools` function.
- **Query tools must respect `--disable-query`.** Any tool that executes a query against a datasource must disappear when query execution is disabled. Take the `enableQueryTools` parameter in your category's `Add*Tools` function, and leave metadata and discovery tools registered unconditionally. If the tool passes the query through unfiltered — raw SQL, InfluxQL — it can write, so it counts as a write tool too: add its category to `mutatingQueryCategories` in `cmd/mcp-grafana/main.go` so `--disable-write` removes it.
- **New tools need a row in the README's tool table**, including the category and the required RBAC permissions and scopes.

The checks that must pass to merge are `license/cla`, `Test Unit`, `Test Integration`, `Lint Go`, `Lint JSON Schemas`, `docker` and `docker-alpine`. `Test Cloud` and the Python E2E suites are informational — `Test Cloud` cannot pass from a fork at all, because it needs credentials, so please don't worry about it being red.

## What you can expect from us

- **Your CI will not start on its own.** For security reasons, workflow runs on pull requests from forks require manual maintainer approval — and that applies to *every push*, not just your first. If your checks show as pending or never seem to start, you are not being ignored; it needs one of us to click. Feel free to comment if it's been a while.
- **We'll tell you the tier.** If we ask for your tool to be opt-in rather than default-on, that's a yes with a placement, not a rejection.
- **If we're going to say no, we'll try to say it on the issue, not on your PR.** That's the whole point of proposing first.

Thanks — the constraint this document describes exists because people rely on this server being fast and cheap to load, and that's worth protecting.
