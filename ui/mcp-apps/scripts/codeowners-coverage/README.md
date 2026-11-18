# Frontend coverage by codeowner

This is a temporary, locally owned implementation of Grafana's ownership-manifest
and coverage-reporting approach. It will be extracted into a shared package for
`grafana/grafana` and `grafana/mcp-grafana`. Keep runner execution, source filters,
and Bench credentials in each repository when that extraction happens.

Reference: [Grafana's codeowners-manifest scripts](https://github.com/grafana/grafana/tree/d1d5df632c9d8edf66cd9728352c4d3a3688dde8/scripts/codeowners-manifest)
and its `report-frontend-coverage-to-bench.yaml` workflow at the same revision.
The implementation here is independently written around the same MIT-licensed
`github-codeowners` dependency; Grafana's AGPL source files are not copied.

## Run locally

From `ui/mcp-apps`:

```sh
npm ci --ignore-scripts
npm run test:coverage
# Pick a team interactively (type to search, arrows to navigate, Enter to select):
npm run test:coverage:by-codeowner
# Filter coverage to one owner using the freshly generated ownership manifest:
npm run test:coverage:by-codeowner -- @grafana/dataviz-squad
```

The command runs reporting tests, runs the whole Vitest suite once with V8
coverage, and splits the result by source-file ownership. It requires the same
network access for the pinned Renderer as ordinary frontend tests.

The positional owner is optional. Without it, an interactive autocomplete picker
lists Grafana teams from the freshly generated ownership manifest, as in Grafana.
Press Ctrl+C to cancel without running tests. Non-interactive runs must pass an
explicit owner. The selected owner must have production sources.
It runs all tests but restricts Vitest coverage (including HTML and totals) to
that owner’s production files from the manifest. Shared files remain included.
Only that owner is written to the publishing index. Unknown owners fail before
running tests. The nightly `test:coverage` command still reports all owners.

- `coverage/index.html`: browsable coverage report.
- `coverage/coverage-summary.json`: original Istanbul summary, including overall totals.
- `coverage/by-codeowner/`: owner reports, manifests, and an index with commit SHA.

Coverage includes all production TypeScript in `src/` and `app/`, including files
not imported by tests. Declarations are excluded. Preview code, tests, downloaded
Renderer code, scripts, and generated bundles are outside this inventory.
Panel-viewer coverage is **not collected**. No percentage threshold is enforced.

The manifest uses tracked files and the root `CODEOWNERS` from the current
checkout. Add new source files to Git before generating owner reports. Ownership
is regenerated each time, with no stale manifest cache. Missing manifest entries
fail reporting rather than silently dropping files.

Shared files contribute fully to each owner; the original overall summary counts
each source once. Counts are summed before computing percentages. Unowned files
have a separate `unowned` report. Zero executable items produce a null percentage
(Bench omits that metric); measured zero coverage remains zero. Owners with no
source coverage get no report. Local edits can affect measurements; CI uses a
clean checkout and records the exact tested commit.

Unlike Grafana's owner-selected Jest runs, this reports coverage of an owner's
source from **all** tests. Preserve that distinction when comparing dashboards.

## Nightly Bench reporting

The workflow runs nightly at 00:00 UTC and supports manual runs on main. It retains
reports for 14 days and publishes each owner using Bench's `jscoverage` input.
Bench expects a top-level `summary` object, not Istanbul's top-level `total`.
Package is `@mcp-grafana/mcp-apps`, suite is `grafana/mcp-grafana`, and service version
is the tested commit SHA. Publishing failures fail the job; reports remain available.

Before enabling publishing, provision the repository-scoped Vault secret
`frontend_perf_tests` with `prometheus_push_url`, `prometheus_user`, and
`prometheus_token`, and authorize this repository's GitHub OIDC workflow access.
The existing Grafana repository's secret is not automatically shared. Dashboards
must include the new suite/package labels. No publishing runs on pull requests.

To publish an existing report with configured `PROMETHEUS_URL`, `PROMETHEUS_USER`,
and `PROMETHEUS_PASSWORD`, install Grafana Bench v1.0.6 and run:

```sh
npm run coverage:bench
# Validate Bench parsing locally without sending metrics:
npm run coverage:bench -- --dry-run
```

## Extraction boundary

Move manifest generation, normalized file ownership, and coverage aggregation to
the future shared package. Preserve the three existing manifest filenames and
weighted/joint-ownership semantics. Keep Vitest/Jest selection, exclusions, CI
scheduling, and Bench deployment configuration outside that package. Tests here
cover weighting, zero/missing data, and real CODEOWNERS precedence.
