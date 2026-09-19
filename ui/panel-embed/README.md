# panel-embed MCP App

Renders `run_panel_query` results as **live Grafana panels**, interleaved with the
app's own text, under one time-range control that drives every panel at once.

The predecessor app, `panel-viewer`, shows a PNG from the image renderer plus an
"Open in Grafana" link. This one runs Grafana's real visualization pipeline in the
host: tooltips, legend toggles, drag-to-zoom, correct unit formatting and field
config overrides, all inside the agent's own page.

## How it works

`src/vendor/grafana-embed.mjs` is built from grafana/grafana `embed/` and defines the
`<grafana-panel>` custom element. This app is then ordinary DOM: it creates one
element per panel in the tool result and sets `panel` and `frames` on each.

Three things worth noting.

**Composition.** `run_panel_query` accepts several panel ids and returns results keyed
by panel id. Every one of them gets an element, each with the app's own heading and the
panel's query text above it. A Grafana panel is an element among the host's own.

**Theming.** The host's design tokens are applied to `:root` with
`applyHostStyleVariables`, and `<grafana-panel>` reads the *same* CSS custom property
names off its own computed style, so the panels adopt the host palette with no mapping
code here. `applyHostFonts` installs the host's `@font-face` rules in the document,
which is required because `@font-face` inside a shadow root is ignored by spec.
`onhostcontextchanged` propagates a light/dark switch.

**Data.** Frames are decoded from whatever the tool returned: a Grafana
`/api/ds/query` response when one is available, which covers every datasource, and a
raw Prometheus matrix otherwise, which is what today's tools return. Nothing in the
app or the element fetches anything; data only arrives through the MCP host.

## Build

Committed `dist/` is what `go:embed` ships, so rebuild after changing `src/`:

```bash
make build-ui        # from the repo root, builds every ui/*/ app
# or
INPUT=mcp-app.html npx vite build
```

Open `dist/mcp-app.html` directly in a browser to exercise it against a built-in
fixture; it detects the absence of a host and renders sample panels.

## Known gaps

The element's gaps are documented in grafana/grafana `embed/README.md`. The ones that
show up here:

- **Size.** 2.70 MB of HTML, 650 KB gzipped, embedded in the binary and sent over the
  transport on every resource read. `panel-viewer` is 450 KB. This is the open
  trade-off: real Grafana panels against a smaller purpose-built chart.
- **Fonts** depend on the host supplying them. Without `styles.css.fonts` the panels
  fall back to a system font stack.
- **Tooltip pinning and x-axis drag** rely on click-outside handlers that break under
  shadow-DOM event retargeting. Hover tooltips, legend toggles and zoom are unaffected.
- **One panel type.** Panels that are not timeseries render an "unsupported panel
  type" message rather than falling back to anything.
