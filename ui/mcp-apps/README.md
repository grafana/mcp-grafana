# Grafana MCP Apps UI

Reusable React shell, a visualization library, and the shared, self-contained Grafana MCP App. The package uses public npm dependencies only. Its standalone theme values are scoped to each app root, so light and dark previews can appear together.

The shell and the visualization library are on npm as `@grafana/mcp-shell`. The Grafana MCP App is not in the npm package: the Go server embeds it.

## Build and check

```sh
npm ci --ignore-scripts
npm run typecheck
npm test
npm run build
```

`npm run build` creates the reusable library (`dist/index.js` and declarations) and the two resources embedded by Go: the shared `dist/app.html` and the viz Renderer script `dist/viz-renderer.js`, with `dist/viz-renderer.hash` for its URI. The App build reads the hash, so `build:apps` builds the Renderer first. Regenerate and commit all three after app source changes. `npm run build:apps` builds only the resources; `mise run build-ui` builds all UI packages.

Run `npm run dev` for component and host previews. The preview fixtures are synthetic and do not query a Grafana server.

`preview/localhost.html` runs a built app against a real host: an `AppBridge` over
`PostMessageTransport`, with `dist/` served same-origin so the app's console and
errors are reachable. Use it when an app misbehaves inside a real host, which
sandboxes the iframe and may refuse to expose a debugger. `?app=app.html`
selects the bundle and `?mode=dark` the colour mode.

## Use the shell

```sh
npm install @grafana/mcp-shell
```

The package root exports only the shell: `McpAppShell`, `McpAppSection` and their types. It needs React 19 and does
not load ECharts.

```tsx
import { McpAppSection, McpAppShell } from '@grafana/mcp-shell';
import '@grafana/mcp-shell/styles.css';

<McpAppShell
  product="Grafana IRM"
  scope="namespace/checkoutservice"
  colorMode={hostTheme}
  summary={{ label: 'Proposed rule', title: proposal.title }}
  primaryAction={{ label: 'Create alert', onClick: createAlert, pending: saving }}
>
  <McpAppSection title="Modify threshold">{thresholdControls}</McpAppSection>
</McpAppShell>;
```

The consumer owns data, tool calls, permissions, sharing controls, and MCP host connection. `colorMode` is required and should be updated when host context changes. `openInGrafana` renders a link and accepts an optional click handler for `app.openLink`. Action callbacks are real controls; `pending` disables repeat clicks while preserving the label. Errors are announced as alerts. Pass the stylesheet once at the app entry. The library build externalizes public dependencies; an app build must bundle them into its final HTML resource.

## Visualization library

The visualization library and the trace and metrics views are experimental. Import them from
`@grafana/mcp-shell/experimental`. Their API can change in a minor version, so pin an exact version of the package if
you use them. The chart components need `echarts`, which is an optional peer dependency: install it in your project.
The library build fails if the package root loads `echarts` (`entryPoints.config.ts`).

`src/viz` holds the chart components and the pieces they share: value formatting
with units and thresholds (`format.ts`), a Grafana ECharts theme derived from the
shell's `--mcp-*` custom properties plus a categorical series palette
(`theme.ts`), series naming that strips labels common to every series
(`types.ts`), and `TimeSeriesChart`, `BarChart`, `Heatmap`, `Bullet`, `Stat` and
`Table`.

`Stat`, `Bullet` and `Table` are intentionally free of any chart dependency —
DOM, plus an inline SVG sparkline — so a bottom-line-first app can use them
without bundling ECharts. `Bullet` replaces a radial gauge: it is denser, it
stacks so several values compare against one scale, and it needs no chart
engine. Both scale only against bounds a unit actually implies.

`Heatmap` handles what a line chart cannot — dozens of series, or cumulative
histogram buckets, which it de-cumulates so each row shows its own count rather
than a running total.

ECharts features are registered once, in `src/viz/echartsSetup.ts`.

## Metrics app

`render_grafana_app` with `toolName: "query_prometheus"` renders the source query's result. `query_prometheus` returns data without opening a view and handles refresh requests from an existing App. The visualization is derived from the
response shape and the inferred unit rather than chosen by the model: a range
query is a time series, an instant query over several series is ranked bars, and
a single value is a stat — or a bullet when the unit bounds it. Alternatives that
suit the same data appear as a toggle. Refresh re-runs the query through the
host's MCP session, and selecting a window sends the agent a follow-up question.

Host state lives in `app/appHostState.ts` and is created with the `App`,
before React renders: a host may deliver `tool-input` and `tool-result` before
the first commit and does not replay them, so handlers registered in an effect
can miss them.

## Trace app

`render_grafana_app` with `toolName: "get_tempo_trace"` displays a Tempo trace with span search, error filtering, a focused ancestor path, and a virtualized waterfall. Selecting a span shows attributes and events, with exception information first. Wide layouts show the inspector beside the waterfall; narrow layouts switch between Trace and Span views. The MCP host supplies result data, color mode, and navigation. The browser does not query Grafana directly.

## Publish the npm package

The `Publish MCP shell` workflow publishes `@grafana/mcp-shell` to npm with provenance (a signed record of the
workflow that built the package). It runs for a tag `mcp-shell/v<version>`. The version of the package is not the
version of the server, and a server release does not publish the package.

1. Change `version` in `package.json`, run `npm install --package-lock-only --ignore-scripts`, and merge the change
   to `main`. A change that breaks an export of the package root needs a new major version, or a new minor version
   before `1.0.0`.
2. Create the tag `mcp-shell/v<version>` on the merge commit and push the tag to `origin`.

The workflow fails if the tag is not on `main` or if its version is not the version in `package.json`. It runs the
type check, the tests and the build before it publishes. A version with a pre-release part, for example
`0.2.0-rc.1`, gets the npm tag `next` and does not become the latest version.

The workflow has no npm token. npm trusted publishing gives access to the `publish-mcp-shell.yml` workflow in the
`npm-publish` environment of this repository, so do not rename the workflow file or the environment.
