# Grafana MCP Apps UI

Reusable React shell, a visualization library, and the self-contained trace and metrics MCP Apps. The package uses public npm dependencies only. Its standalone theme values are scoped to each app root, so light and dark previews can appear together.

## Build and check

```sh
npm ci --ignore-scripts
npm run typecheck
npm test
npm run build
```

`npm run build` creates the reusable library (`dist/index.js` and declarations) and the single-file MCP resources `dist/trace.html` and `dist/metrics.html`. The HTML files are committed for the Go server's `go:embed`. Regenerate them after changing app source; do not edit generated HTML. `make build-ui` runs the same install and build sequence. To build one resource, use `npm run build:trace` or `npm run build:metrics`.

Run `npm run dev` for component and host previews. The preview fixtures are synthetic and do not query a Grafana server.

`preview/localhost.html` runs a built app against a real host: an `AppBridge` over
`PostMessageTransport`, with `dist/` served same-origin so the app's console and
errors are reachable. Use it when an app misbehaves inside a real host, which
sandboxes the iframe and may refuse to expose a debugger. `?app=metrics.html`
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

`query_prometheus` renders its own result. The visualization is derived from the
response shape and the inferred unit rather than chosen by the model: a range
query is a time series, an instant query over several series is ranked bars, and
a single value is a stat — or a bullet when the unit bounds it. Alternatives that
suit the same data appear as a toggle. Refresh re-runs the query through the
host's MCP session, and selecting a window sends the agent a follow-up question.

Host state lives in `app/metricsHostState.ts` and is created with the `App`,
before React renders: a host may deliver `tool-input` and `tool-result` before
the first commit and does not replay them, so handlers registered in an effect
can miss them.

## Trace app

`get_tempo_trace` displays a Tempo trace with span search, error filtering, a focused ancestor path, and a virtualized waterfall. Selecting a span shows attributes and events, with exception information first. Wide layouts show the inspector beside the waterfall; narrow layouts switch between Trace and Span views. The MCP host supplies result data, color mode, and navigation. The browser does not query Grafana directly.
