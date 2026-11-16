# viz contract Renderer

The App draws the `query_datasource` viz spec with the Renderer of
[grafana/dataViz](https://github.com/grafana/dataViz) (`plugins/packages/@plugins/grafana-echarts`), which is under the
Apache License 2.0. The Renderer files are not in Git. `RENDERER` pins a dataViz release (`grafana-echarts-renderer/v<version>`),
and `scripts/renderer.mjs` downloads its files into this directory.

The release has these files:

- `renderer.js`: the Renderer bundle. `renderer.js.LICENSE.txt` lists the third-party code in it.
- `renderer.manifest.json`: the version, contract version and SHA-256 of `renderer.js`.
- `renderer.d.ts`: the `renderer-api.d.ts` of the contract that the Renderer builds with.
- `icons.json`: the SVG of each `@grafana/ui` icon that the Renderer can show. The App puts them in the icon cache
  before it draws (`renderer.15`).
- `package.json`: the peers that the App bundles for the Renderer (viz-contract `renderer.9`).
- `LICENSE`: the dataViz license.

## Pin

`RENDERER` has the release tag, the signer workflow of the release, and the SHA-256 of each file.

`npm run build`, `npm run typecheck`, `npm run dev` and `npm test` download the files that are missing or that differ
from the pin. They fail if a downloaded file is not its pin. They need no `gh` CLI and no token. If the files on disk
match the pin, they need no network.

CI also runs `node scripts/renderer.mjs --verify`. For each file, it gets the SLSA provenance attestation from the
GitHub attestations API and verifies it with Sigstore. The attestation must be signed by the `signer` workflow of
`RENDERER`, and its subject must be the file. So a pin can only name a file that the dataViz release workflow built.

## Change the Renderer version

Run these commands in `ui/mcp-apps`:

```sh
node scripts/renderer.mjs --bump grafana-echarts-renderer/v<version>
npm test
```

`--bump` downloads all files of the release, verifies the attestation of each file, makes sure that the manifest
version is the tag version, and writes `RENDERER`. Set `GITHUB_TOKEN` if the anonymous GitHub API rate limit
(60 requests per hour) stops it.

Then run `mise run build-ui` in the repository root, and commit `RENDERER` and the rebuilt `dist/` files.
Do not edit `RENDERER` by hand.
