# Browser host-contract tests

Run from the repository root:

```sh
mise run build-ui
cd ui/mcp-apps
npx playwright install chromium
npm run test:e2e
```

The Vite test host starts the built Go server over stdio, reads its real tool
catalog and embedded resources, then connects the built app iframe through
AppBridge/PostMessageTransport. Most query results are deterministic fixtures; no
Grafana, LLM, Docker, credentials, or external MCP host is required. The host builds a temporary Go binary on startup using the Go build cache.
Rebuild UI bundles after UI source edits, then restart the host to embed them.
For an explicitly prebuilt server, set `MCP_GRAFANA_BINARY` to its path (relative
paths resolve from the working directory). This skips the automatic build. Playwright allows up to ten minutes for automatic
local builds (including cold module downloads), or three minutes when a prebuilt
binary is supplied. Go stderr is inherited so diagnostics stay visible and cannot
fill an unread pipe.

The `forward` profile forwards resource reads. The `tool-declared` profile
advertises `serverResources` but rejects URIs absent from the server's actual
tool `_meta.ui.resourceUri` declarations. This models the Claude Desktop Code
behavior reported in #1654, not a universal MCP requirement. It is not a test
of the installed Claude client and cannot detect changes in that client.

## Resource-policy regression coverage

Both resource policies must render a real chart. The `tool-declared` test covers
[#1654](https://github.com/grafana/mcp-grafana/issues/1654), fixed by
[#1659](https://github.com/grafana/mcp-grafana/pull/1659): the renderer resource is
now declared by an app-only tool for clients advertising MCP Apps support.
The harness advertises that capability and reads the actual Go tool catalog.
This is a normal passing regression test; do not replace its chart assertion
with an error-banner assertion.

The permissive profile is the positive control: the same server, query result,
and renderer draw successfully. Failure artifacts retain requested/rejected/
forwarded URIs, bridge events, browser errors, screenshots, and Playwright traces.

```sh
npx playwright show-report
npx playwright test --grep tool-declared
```

Locale, timezone, viewport, and reduced motion are fixed. Fonts come from
the built app. The visual suite below adds reviewed screenshot baselines.
Tests assert chart pixels, exact refresh arguments and replacement content,
trace selection, theme propagation, resize, empty/error feedback, and the
panel-viewer's decoded image and host-mediated link flow.

## Per-visualization screenshot smoke suite

```sh
npm run test:e2e:visual
```

`specs/viz-screenshots.spec.ts` iterates every chart in the pinned contract registry
(currently 20) and requires its `smoke-<type>/spec.json` fixture. The host adapts
that spec into query arguments/results and serves the real embedded app and
renderer. The only fixture option override disables animation. Each test waits
for the renderer's `ready` state and loaded fonts, then compares the rendered
chart container (including actual canvas pixels). It also attaches a real page
screenshot and host/browser diagnostics. The restricted-resource #1654 test
remains separate and must also pass.

Use absolute fixture time ranges. The clock starts at a fixed date but advances:
freezing Date.now() stalls Sankey rendering. Seeded Math.random stabilizes graph
layout. Screenshot comparison allows 0.2% differing pixels; do not increase this
to hide a regression. Playwright waits for consecutive stable screenshots.

Baselines are separate for macOS and Linux. CI builds the UI and a static Go server on the ARM64 Linux runner using the
repository mise setup. Browser tests run in the pinned Playwright 1.65.1 Noble
container with `MCP_GRAFANA_BINARY` pointing to that build; no Go build or Git
metadata lookup runs inside the browser container. Keep the
Playwright npm version, image, and baselines aligned when upgrading. Recheck
fonts and review every changed image; do not automatically update in CI.

### Regenerate one chart or all charts

Run regeneration commands sequentially: concurrent Playwright processes in the same
checkout share output/report directories and can delete each other's artifacts.

Update baselines only after confirming an intentional visual change. For example,
renderer v0.5.0 added a Reset control to the toolbar. CI tests the PR merged with
current main, so merge current main before updating for renderer changes there.
Do not loosen the comparison threshold to hide a difference.

First, from the repository root, build the UI that the Go binary will embed:

```sh
mise run build-ui
cd ui/mcp-apps
```

Stop any running `npm run dev` or Playwright UI session using ports 5194/5196/5197.
Unset `MCP_GRAFANA_BINARY` unless it points to a binary rebuilt from this checkout.
The commands below use `CI=true` to require fresh servers instead of silently
reusing an older harness/binary. They update only the current platform's baselines
(`*-darwin.png` on macOS, `*-linux.png` on Linux).

```sh
# One chart. The trailing $ prevents matching a longer chart name.
CI=true npm run test:e2e -- viz-screenshots.spec.ts --grep 'visual smoke: timeseries$' --update-snapshots

# Selected charts.
CI=true npm run test:e2e -- viz-screenshots.spec.ts --grep 'visual smoke: (timeseries|barchart)$' --update-snapshots

# Every visualization in the pinned registry.
CI=true npm run test:e2e -- viz-screenshots.spec.ts --update-snapshots
```

Use the direct `test:e2e` commands when supplying `--grep`: `test:e2e:visual`
already supplies its own grep filter. `--list` can replace `--update-snapshots`
to check which tests a filter selects before updating.

### Regenerate the Linux CI baselines from macOS or Linux

Use Docker with the exact CI image and ARM64 platform. Both harness servers and
Chromium run inside the container; do not connect to a macOS-hosted harness.
The following commands run from the **repository root**, after `mise run build-ui`.
They cross-compile the Go binary and install Linux dependencies in a temporary
mounted directory, preserving the host's `node_modules`. Baselines and diagnostics
are written directly into the checkout, owned by the current user.

```sh
snapshot_tmp="$(mktemp -d)"
mkdir "$snapshot_tmp/node_modules"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "$snapshot_tmp/mcp-grafana" ./cmd/mcp-grafana

docker run --rm --init --ipc=host --platform linux/arm64 \
  --user "$(id -u):$(id -g)" \
  -e HOME=/tmp -e CI=true -e MCP_GRAFANA_BINARY=/tmp/mcp-grafana \
  -v "$PWD:/work" \
  -v "$snapshot_tmp/node_modules:/work/ui/mcp-apps/node_modules" \
  -v "$snapshot_tmp/mcp-grafana:/tmp/mcp-grafana:ro" \
  -w /work/ui/mcp-apps \
  mcr.microsoft.com/playwright:v1.65.1-noble@sha256:1507cbfc550a63a6b7396791ca4f00df17beb56342d2447b3f106c306f525579 \
  sh -c 'npm ci --ignore-scripts && npm run test:e2e -- viz-screenshots.spec.ts --update-snapshots "$@"' \
  sh --grep 'visual smoke:'

rm -r "$snapshot_tmp"
```

The final `--grep 'visual smoke:'` updates every chart. Replace it with
`--grep 'visual smoke: timeseries$'` for one chart, or
`--grep 'visual smoke: (timeseries|barchart)$'` for a subset.
Network access is needed for the image, npm packages and pinned upstream sandbox.
Update the image reference here when changing it in `.github/workflows/browser-e2e.yml`.

### Review and verify

Review each changed PNG under `e2e/specs/viz-screenshots.spec.ts-snapshots/` against
its previous version. Check chart data and labels as well as toolbar/layout changes.
Regenerate both macOS and Linux baselines for shared UI changes; one platform's
update does not update the other. Commit only intentional baseline changes.

Then rerun the same selection **without** `--update-snapshots`. For a broader
stability check on the current platform:

```sh
# From ui/mcp-apps, with no old harness running:
CI=true npm run test:e2e -- viz-screenshots.spec.ts --repeat-each=10 --workers=2 --retries=0
```

For Linux, remove `--update-snapshots` from the Docker command and add the repeat
flags after its final `--grep` argument. A passing update run only writes new
expectations; the subsequent comparison run validates them.

## Upstream sandbox ownership

`../harness/basic-host/upstream.json` pins the upstream revision and SHA-256 of each file.
`../harness/basic-host/start.mjs` downloads verified files into an ignored cache, bundles the
upstream sandbox entry, and starts upstream `serve.ts` unchanged using Node.
It also preserves the upstream LICENSE. First run requires GitHub access.

We reuse the security-relevant parts of [basic-host](https://github.com/modelcontextprotocol/ext-apps/tree/main/examples/basic-host):
its sandbox proxy, nested iframe, origin checks, and HTTP CSP construction.
We do not use its interactive React tool-picker UI. Our fixture host still
owns tool-result delivery and the simulated #1654 resource policy.

Ports: fixture host 5194; upstream host 5196; upstream sandbox 5197. Playwright
manages the upstream process. Local runs can reuse existing servers; CI starts
fresh servers. The actual Go resource's CSP/permissions metadata is passed to
upstream's sandbox, without locally rewriting policy. Screenshot tests also
assert that no CSP violations occurred.

`specs/sandbox.spec.ts` verifies that parent DOM/storage access raises SecurityError
and an undeclared connection triggers a real connect-src CSP violation. This
isolation does not prohibit app-origin localStorage: upstream allows same-origin
within its separate sandbox origin. The separate strict-profile test below covers
storage denial.

To upgrade, review upstream changes, update the revision and hashes together,
and rerun the suite. Do not edit downloaded sandbox code or loosen its policy
to make tests pass.

## Shared preview host

The host implementation lives in `../harness/`, outside the test directory.
`npm run dev` starts the same host and upstream sandbox that Playwright uses.
`host.ts` owns bridge lifecycle and interactive controls; `scenarios.ts` owns
fixture delivery; `metricsFixtures.ts` retains the legacy Prometheus examples.
E2E only owns specs and baselines.

Upstream's host-side helpers currently hardcode sandbox port 8081 and couple to
its theme/UI modules. We retain a small local bootstrap for fixture delivery;
security policy and sandbox proxy remain unchanged upstream code.

## Focused visualization integration tests

`specs/viz-integration.spec.ts` adds five representative browser journeys:

- Strict `sandbox="allow-scripts"` rendering and refresh with localStorage denied.
  The pinned upstream relay rejects opaque-origin messages, so this profile connects
  AppBridge directly to the app iframe. `/mcp/strict-app` serves the real embedded
  HTML with the CSP header obtained from upstream basic-host, unchanged. This is
  renderer conformance coverage, not a claim that basic-host supports that profile.
- Time-picker selection sends exact range arguments and model context over the bridge.
- A failed refresh preserves the chart; a successful retry clears the error.
- Narrow toolbar, time picker, hover/pinned tooltip and legend color menu render
  their icons without app-frame network requests. This samples browser-only states;
  the comprehensive renderer interaction matrix remains upstream.
- A real Go `render_grafana_app` call and refresh reach a fixture-backed Grafana
  HTTP server. Only `/api/ds/query` is stubbed, using the pinned contract response.
  Assertions cover the HTTP request, actual tool result and rendered canvas.

The harness enables `dsquery` and points its Go child at a loopback fixture server
on an ephemeral port, with no Grafana credentials. Parallel Go-journey tests use
unique datasource UIDs so their request assertions cannot see another test's calls.
The fixture host exposes only `render_grafana_app` with `query_datasource` through
`/mcp/call`; manual previews remain deterministic.

To repeat this spec without retries:

```sh
npm run test:e2e -- viz-integration.spec.ts --repeat-each=10 --workers=2 --retries=0
```

Use `CI=true` with unused harness ports when checking fresh server startup rather
than reusing a running development host. Schema/pick matrices, argument transforms,
timeout permutations and callback races remain in Go/Vitest.

### When a harness port is in use

Like the Docker test stack described in DEVELOPING.md, the browser harness
supports service-specific host port overrides. These are independent of the
Docker stack's variables: browser tests use fixtures, not that stack.

| Variable | Default |
| --- | --- |
| `MCP_APPS_HOST_PORT` | 5194 |
| `MCP_BASIC_HOST_PORT` | 5196 |
| `MCP_SANDBOX_HOST_PORT` | 5197 |

For example, run `MCP_APPS_HOST_PORT=5294 MCP_BASIC_HOST_PORT=5296 MCP_SANDBOX_HOST_PORT=5297 npm run test:e2e`.
The same variables work with `npm run dev`; open `http://127.0.0.1:5294`
for that example. Legacy preview bookmarks still redirect to the default port;
open the configured host directly when overriding it. Ports must be distinct
integers from 1 to 65535. Sandbox URLs and Playwright readiness checks follow
the overrides automatically. The mock Grafana server always uses an ephemeral port.

### When the checkout is under a dot directory

The sandbox server returns 404 for `sandbox.html` when the path of the checkout has a
directory that starts with a dot, such as `.claude/worktrees`. With `npm run dev`, the App
frame then shows "Not Found". Playwright starts the same server. The cause is in the pinned
upstream `serve.ts`: it serves the file with `res.sendFile` and an absolute path, and Express
rejects a path that has a dot directory. We do not edit the downloaded sandbox code.
Use a checkout at a path with no dot directory, for example
`git worktree add --detach /tmp/mcp-grafana-review <ref>`.
