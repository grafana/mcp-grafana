# Grafana MCP Apps UI

Reusable React shell and self-contained trace and service-health MCP Apps. The package uses public npm dependencies only. Its standalone theme values are scoped to each app root, so light and dark previews can appear together.

## Build and check

```sh
npm ci --ignore-scripts
npm run typecheck
npm test
npm run build
```

`npm run build` creates the reusable library (`dist/index.js` and declarations) and the single-file MCP resources `dist/trace.html` and `dist/service-health.html`. The two HTML files are committed for the Go server's `go:embed`. Regenerate them after changing app source; do not edit generated HTML. `make build-ui` runs the same install and build sequence. To build one resource, use `npm run build:trace` or `npm run build:service-health`.

Run `npm run dev` for component and host previews. The preview fixtures are synthetic and do not query a Grafana server.

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

## Trace app

`render_trace` displays a Tempo trace with span search, error filtering, a focused ancestor path, and a virtualized waterfall. Selecting a span shows attributes and events, with exception information first. Wide layouts show the inspector beside the waterfall; narrow layouts switch between Trace and Span views. The MCP host supplies result data, color mode, and navigation. The browser does not query Grafana directly.

## Service health app

`render_service_health` displays p95 latency, error ratio, request rate, and outbound dependency metrics. Range changes request fresh data through the MCP host, while failed refreshes preserve the previous result. Missing metrics remain unavailable rather than being displayed as zero. RED metrics alone do not establish service health; the app shows an unknown status when the available evidence is insufficient. Navigation appears only when the server supplies a Grafana URL.

Serve either generated HTML with MIME type `text/html;profile=mcp-app` and reference its `ui://` URI from the tool's `_meta.ui.resourceUri`.
