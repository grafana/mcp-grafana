# Grafana MCP Apps UI

Reusable React shell, a visualization library, and the shared, self-contained Grafana MCP App. The package uses public npm dependencies only. Its standalone theme values are scoped to each app root, so light and dark previews can appear together.

## Build and check

```sh
npm ci --ignore-scripts
npm run typecheck
npm test
npm run build
```

`npm run build` creates the reusable library (`dist/index.js` and declarations) and the two resources embedded by Go: the shared `dist/app.html` and the viz Renderer script `dist/viz-renderer.js`, with `dist/viz-renderer.hash` for its URI. The App build reads the hash, so `build:apps` builds the Renderer first. Regenerate and commit all three after app source changes. `npm run build:apps` builds only the resources; `mise run build-ui` builds all UI packages.

Run `npm run dev` for component and host previews. The preview fixtures are synthetic and do not query a Grafana server.

Run `npm run storybook` (port 6006) to browse every view of `render_grafana_app`: the shell, the metrics views, the
trace waterfall, and the `query_datasource` viz view, each in light and dark (toolbar). The stories in `stories/` use
the same synthetic fixtures as the tests and need no Grafana server. The App stories (`GrafanaApp`, `VizApplication`)
run the real `GrafanaApplication` against a scripted fake MCP host (`stories/fakeHost.tsx`), so the waiting, error,
cancelled and closed states are the real ones. The viz stories draw with the committed `dist/viz-renderer.js`. Storybook
is a local tool: `npm run build` does not run it and its config in `.storybook/` is separate from the bundle builds,
so it cannot change the committed `dist/` files. `npm run build-storybook` writes `storybook-static/` (git-ignored).
Check the stories' types with `npx tsc --noEmit -p stories/tsconfig.json`.

The **Design delta** section compares the App with the design canvas (Freya, the Visual Refresh theme). *Tokens*
reads the Freya theme from `@grafana/data` (`getThemeById('visual_refresh_dark' | 'visual_refresh_light')`) and the
`--mcp-*` variables from the page, so it needs no copied values and cannot go stale. *Components* shows each component
as it is today beside a table of what separates it from the design, and *Overview* rolls the tables up and lists the
open design decisions. The tables are written by hand in `stories/design-delta/deltas.ts`: update it when a delta is
closed or the design changes.

`preview/localhost.html` runs a built app against a real host: an `AppBridge` over
`PostMessageTransport`, with `dist/` served same-origin so the app's console and
errors are reachable. Use it when an app misbehaves inside a real host, which
sandboxes the iframe and may refuse to expose a debugger. `?app=app.html`
selects the bundle and `?mode=dark` the colour mode.

## Use the shell

```tsx
import { McpAppSection, McpAppShell } from '@mcp-grafana/mcp-apps';
import '@mcp-grafana/mcp-apps/styles.css';

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
