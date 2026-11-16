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

`RENDERER` has the release tag, its version and contract version, and the SHA-256 of each file. It also shows the
trusted repository and signer workflow, but `scripts/renderer.mjs` does not take them from `RENDERER`. They are
constants in the script, and a pin that names another repository or signer fails.

`npm run build`, `npm run typecheck`, `npm run dev` and `npm test` download the files that are missing or that differ
from the pin. They fail if a downloaded file is not its pin. They need no `gh` CLI and no token. A fresh clone needs
network for the first of these commands. After that, if the files on disk match the pin, they need no network.

## What is checked

- Every build: each file is the SHA-256 of `RENDERER`. The pin tests also make sure that the manifest has the version
  and contract version of `RENDERER`.
- When `RENDERER`, the script or the lockfile changes: the `viz Renderer provenance` workflow runs
  `node scripts/renderer.mjs --verify`. For each file, it gets the SLSA provenance attestation from the GitHub
  attestations API and verifies it with Sigstore. The attestation must be signed by the
  `grafana/dataViz/.github/workflows/release-please.yml` workflow on `main`, and its subject must be the file.
- `--bump` checks the same attestations. It also makes sure that the release is immutable, that each file was built
  from the commit of the tag, and that the manifest has the tag version and the hash of `renderer.js`.

So a pin can name only files that the dataViz release workflow built. To trust another repository or workflow, change
the constants in `scripts/renderer.mjs`.

## Change the Renderer version

Run these commands in `ui/mcp-apps`:

```sh
node scripts/renderer.mjs --bump grafana-echarts-renderer/v<version>
npm test
```

`--bump` writes `RENDERER`. Set `GITHUB_TOKEN` if the anonymous GitHub API rate limit (60 requests per hour) stops it.
If the contract version changes, the `contractVersion:` line of `RENDERER` changes in the diff.

Then run `mise run build-ui` in the repository root, and commit `RENDERER` and the rebuilt `dist/` files.
Do not edit `RENDERER` by hand.

## Try an unreleased Renderer build

The scripts replace a file that does not match the pin. To use a local build instead, set `RENDERER_LOCAL_DIR` to
the dataViz `dist-renderer/` directory:

```sh
RENDERER_LOCAL_DIR=../../../dataViz/plugins/packages/@plugins/grafana-echarts/dist-renderer npm run dev
```

The scripts then copy that build into this directory and check no hashes. The renderer-pin tests fail while a local
build is in place. The next command without `RENDERER_LOCAL_DIR` replaces it with the pinned release. Do not commit
`dist/` files built from a local Renderer.

## Proxy

The download reads `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`. If your network allows traffic only through a proxy,
set them before the first `npm test` or `npm run build`. npm uses its own proxy configuration for `npm ci`.
