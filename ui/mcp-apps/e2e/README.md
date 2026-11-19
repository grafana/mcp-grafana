# Browser host-contract tests

Run from the repository root:

```sh
mise run build-ui
mise run build
cd ui/mcp-apps
npx playwright install chromium
npm run test:e2e
```

The Vite test host starts the built Go server over stdio, reads its real tool
catalog and embedded resources, then connects the built app iframe through
AppBridge/PostMessageTransport. Query results are deterministic fixtures; no
Grafana, LLM, Docker, credentials, or external MCP host is required. Rebuild the
Go binary after changing UI bundles or resource/tool registration.

The `forward` profile forwards resource reads. The `tool-declared` profile
advertises `serverResources` but rejects URIs absent from the server's actual
tool `_meta.ui.resourceUri` declarations. This models the Claude Desktop Code
behavior reported in #1654, not a universal MCP requirement. It is not a test
of the installed Claude client and cannot detect changes in that client.

## Known compatibility regression

On main at 70b0494, eight tests pass and `renders chart with tool-declared
resource policy` fails: the renderer URI is rejected before forwarding, and
no chart appears. The test runs with a conditional `test.fail` annotation for
[#1654](https://github.com/grafana/mcp-grafana/issues/1654), allowing this coverage
to merge while compatibility work continues. An unexpected pass fails CI: remove
the annotation when the fix lands. The forward profile remains a normal test.
Do not change the chart assertion to an error-banner assertion.

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
# Deliberate baseline update, followed by visual review:
npm run test:e2e:visual -- --update-snapshots
```

`specs/viz-screenshots.spec.ts` iterates every chart in the pinned contract registry
(currently 20) and requires its `smoke-<type>/spec.json` fixture. The host adapts
that spec into query arguments/results and serves the real embedded app and
renderer. The only fixture option override disables animation. Each test waits
for the renderer's `ready` state and loaded fonts, then compares the rendered
chart container (including actual canvas pixels). It also attaches a real page
screenshot and host/browser diagnostics. The restricted-resource #1654 test
remains separate and is annotated as an expected failure.

Use absolute fixture time ranges. The clock starts at a fixed date but advances:
freezing Date.now() stalls Sankey rendering. Seeded Math.random stabilizes graph
layout. Screenshot comparison allows 0.2% differing pixels; do not increase this
to hide a regression. Playwright waits for consecutive stable screenshots.

Baselines are separate for macOS and Linux. CI runs the pinned Playwright 1.65.1
Noble container on ARM64 Linux to match Linux baseline generation. Keep the
Playwright npm version, image, and baselines aligned when upgrading. Recheck
fonts and review every changed image; do not automatically update in CI.

To regenerate Linux baselines, run the same commands inside the pinned CI
container on ARM64 Linux, with both the test host and upstream sandbox servers
inside the container. Do not point a container browser at a macOS host: upstream
basic-host deliberately validates localhost referrers and sandbox origins.

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
within its separate sandbox origin. We do not claim stricter storage denial.

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
