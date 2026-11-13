#!/usr/bin/env bash
# Builds the viz contract Renderer of grafana/dataViz at a ref, copies it into this repository, and writes the pin
# ui/mcp-apps/renderer/RENDERER. Run it with a ref (a commit or a tag) when the Renderer changes:
#
#   scripts/sync-viz-renderer.sh <ref>
#
# It needs the gh CLI, Node and pnpm. See ui/mcp-apps/renderer/README.md.
set -euo pipefail

ref="${1:?usage: scripts/sync-viz-renderer.sh <commit-or-tag>}"
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/ui/mcp-apps/renderer"
package="plugins/packages/@plugins/grafana-echarts"

hash() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# The archive is untrusted data: it goes into its own empty directory.
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cd "$work"
sha="$(gh api "repos/grafana/dataViz/commits/$ref" --jq .sha)"
gh api "repos/grafana/dataViz/tarball/$sha" >"$work/dataviz.tgz"
mkdir "$work/src"
tar -xzf "$work/dataviz.tgz" -C "$work/src" --strip-components=1

# The lockfile fixes each dependency. No install script runs: the build needs none.
(cd "$work/src/plugins" && pnpm install --frozen-lockfile --ignore-scripts --filter "@plugins/grafana-echarts...")
(cd "$work/src/$package" && pnpm run build:renderer)

dist="$work/src/$package/dist-renderer"
manifest() { node -e 'console.log(require(process.argv[1])[process.argv[2]])' "$dist/renderer.manifest.json" "$1"; }
bundle_sha="$(hash "$dist/renderer.js")"
# delivery.6: the sha256 of the manifest is the hash of the bundle.
[ "$(manifest sha256)" = "$bundle_sha" ] || { echo "the manifest sha256 is not the hash of renderer.js" >&2; exit 1; }

cp "$dist/renderer.js" "$dist/renderer.manifest.json" "$dist/package.json" "$work/src/LICENSE" "$out/"
# The types of the Renderer API, from the contract copy of the Renderer, so they match its contractVersion.
cp "$work/src/$package/src/renderer/contract/renderer-api.d.ts" "$out/renderer.d.ts"
cat >"$out/RENDERER" <<PIN
repository: grafana/dataViz
commit: $sha
version: $(manifest version)
contractVersion: $(manifest contractVersion)
sha256 renderer.js: $bundle_sha
PIN

echo "Synced the Renderer $(manifest version) (contract $(manifest contractVersion)) of grafana/dataViz ($sha)."
echo "Next: npm test -- renderer-pin in ui/mcp-apps, then commit ui/mcp-apps/renderer."
