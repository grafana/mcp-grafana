# Vendored Grafana ECharts panel renderer

`grafana-echarts-viz.js` is the standalone build of the Grafana ECharts panel plugin. It draws Grafana panels
outside Grafana: `window.GrafanaViz.render(element, spec, options)`. The any-datasource view of
`render_grafana_app` uses it (`app/DatasourceApplication.tsx`).

Only `dist/app-datasource.html` embeds it, as gzip + base64. The App decodes it when the first
`query_datasource` result arrives. `dist/app.html`, which the server serves by default, does not contain it.

`renderer.json` pins the version and the SHA-256 of the file. The build fails if they do not match.

To update the renderer:

1. Replace `grafana-echarts-viz.js` and `grafana-echarts-viz.js.LICENSE.txt` with the files of the new release.
2. Set `version` and `sha256` in `renderer.json` (`shasum -a 256 grafana-echarts-viz.js`).
3. Copy the release's `viz-pick.json` to `tools/app_datasource_pick.json` and its pick fixtures to
   `tools/testdata/app_datasource_pick/`. The server picks the panel type with the same rules as the renderer.
4. Run `mise run build-ui` and commit `dist/`.

License: `LICENSE` (AGPL-3.0-only) covers `grafana-echarts-viz.js`. Third-party notices are in
`grafana-echarts-viz.js.LICENSE.txt`.
