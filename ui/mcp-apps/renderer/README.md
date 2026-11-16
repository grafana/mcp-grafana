# viz contract Renderer

`renderer.js`, `renderer.manifest.json`, `icons.json`, `package.json` and `renderer.d.ts` are the files of a
`grafana-echarts-renderer` release of [grafana/dataViz](https://github.com/grafana/dataViz)
(`plugins/packages/@plugins/grafana-echarts`), which is under the Apache License 2.0 (`LICENSE` in this directory).
`renderer.js.LICENSE.txt` has the license notices of the packages bundled into `renderer.js`.
The App gives it the viz spec of `query_datasource`. `icons.json` has the SVG of each `@grafana/ui` icon that the
Renderer can show; the Renderer resource puts them in the icon cache before it draws (`renderer.15`).
`package.json` lists the peers that the Renderer resource bundles or takes from the App (viz-contract `renderer.9`).
`renderer.d.ts` is the Renderer API of the contract that the Renderer builds with, so the types match its
`contractVersion`.

`RENDERER` pins the copy: the release tag and its commit, the Renderer and contract versions, and the SHA-256 of
each file. A test fails if a file differs from its pin, or if a file is added or missing.

## Change the Renderer version

Do not edit these files. Run the sync script with a Renderer version. It needs the `gh` CLI and Node:

```sh
scripts/sync-viz-renderer.sh 0.1.0
mise run build-ui
cd ui/mcp-apps && npm test
```

Then commit `ui/mcp-apps`. The script takes only an immutable release, and checks the build provenance of each file
with `gh attestation verify`: the signer must be the dataViz `release-please.yml` workflow on `main` on a
GitHub-hosted runner, and the source commit of that run must be the commit of the tag. A Renderer change that is not
released yet cannot be synced; release it in dataViz first.

## Not checked yet

The provenance check runs only in the sync script. CI does not verify the committed files against the release
attestation, so the pin test catches a mismatch between files and `RENDERER`, not an edit to both. Review a change to
`RENDERER` as you would a change to the files.
