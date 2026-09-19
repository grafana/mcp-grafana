# Vendored @grafana/embed

`grafana-embed.mjs` is built from grafana/grafana `embed/` (`vite build`). It registers
the `<grafana-panel>` custom element and carries its own CSS, so there is nothing else
to copy.

Vendoring is prototype-only: the real integration consumes a published
`@grafana/embed` package.
