# viz contract Renderer

`renderer.js`, `renderer.manifest.json`, `icons.json` and `package.json` are a build of the Renderer in
[grafana/dataViz](https://github.com/grafana/dataViz) (`plugins/packages/@plugins/grafana-echarts`), which is under
the Apache License 2.0 (`LICENSE` in this directory). The App gives it the viz spec of `query_datasource`. `icons.json` has the SVG of each `@grafana/ui` icon that the Renderer can show; the App puts them in the
icon cache before it draws (`renderer.15`).
`package.json` lists the peers that the App bundles for it (viz-contract `renderer.9`). `renderer.d.ts` is the
`renderer-api.d.ts` of the contract copy in dataViz, so the types match the `contractVersion` of the Renderer.

`RENDERER` pins the copy: the commit of dataViz, the Renderer and contract versions, and the SHA-256 of
`renderer.js` and `icons.json`. A test fails if the bundle or the manifest differs from its pin, so a hand edit does not pass.

## Change the Renderer version

Do not edit these files. Run the sync script with a commit or a tag of dataViz. It needs the `gh` CLI, Node and pnpm:

```sh
scripts/sync-viz-renderer.sh <ref>
cd ui/mcp-apps && npm test -- renderer-pin
```

Then commit `ui/mcp-apps/renderer`.

## Not checked yet

CI does not rebuild the bundle from the pinned commit. Two builds of the same commit give the same SHA-256, so a CI
job can run the sync script and fail on a diff.
