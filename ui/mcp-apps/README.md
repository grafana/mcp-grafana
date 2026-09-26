# Grafana MCP Apps UI

Reusable React shell for MCP Apps, with public npm dependencies, explicit light/dark themes, accessible feedback and composable sections.

## Build and check

```sh
npm ci --ignore-scripts
npm run typecheck
npm test
npm run build
```

The build creates the reusable library at `dist/index.js` with TypeScript declarations. Run `npm run dev` for the interactive shell preview. `npm pack` packages the built library for consumers; it is not yet published to npm.

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
