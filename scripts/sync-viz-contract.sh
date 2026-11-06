#!/usr/bin/env bash
# Copies the registry and the Source fixtures of grafana/viz-contract into this repository, and writes the pin
# tools/viz_contract/CONTRACT. Run it with a ref (a commit or a tag) when the contract changes:
#
#   scripts/sync-viz-contract.sh <ref>
#
# It needs the gh CLI with read access to grafana/viz-contract. The copies are in tools/viz_contract (registry) and
# tools/testdata/viz-contract (fixtures). See tools/viz_contract/README.md.
set -euo pipefail

ref="${1:?usage: scripts/sync-viz-contract.sh <commit-or-tag>}"
root="$(cd "$(dirname "$0")/.." && pwd)"
registry="$root/tools/viz_contract"
fixtures="$root/tools/testdata/viz-contract"

hash() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# The archive is untrusted data: it goes into its own empty directory.
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
sha="$(gh api "repos/grafana/viz-contract/commits/$ref" --jq .sha)"
gh api "repos/grafana/viz-contract/tarball/$sha" >"$work/contract.tgz"
mkdir "$work/src"
tar -xzf "$work/contract.tgz" -C "$work/src" --strip-components=1
src="$work/src"
version="$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$src/package.json" | head -n1)"
[ -n "$version" ] || { echo "no version in package.json" >&2; exit 1; }

mkdir -p "$registry"
cp "$src/registry/chart-types.json" "$src/registry/pick.json" "$src/LICENSE" "$registry/"

# The Source fixtures: the sets that have a response and a result. Other files of a set are for the App and the Renderer.
rm -rf "$fixtures"
mkdir -p "$fixtures"
for dir in "$src"/fixtures/*/; do
  name="$(basename "$dir")"
  if [ ! -f "$dir/response.json" ] || [ ! -f "$dir/result.json" ]; then
    continue
  fi
  mkdir -p "$fixtures/$name"
  for file in args meta request response result; do
    if [ -f "$dir/$file.json" ]; then
      cp "$dir/$file.json" "$fixtures/$name/"
    fi
  done
done

cat >"$registry/CONTRACT" <<PIN
repository: grafana/viz-contract
commit: $sha
version: $version
sha256 chart-types.json: $(hash "$registry/chart-types.json")
sha256 pick.json: $(hash "$registry/pick.json")
PIN

echo "Synced grafana/viz-contract $version ($sha)."
echo "Next: go test -tags unit ./tools -run 'VizContract|QueryDatasource', then commit tools/viz_contract and tools/testdata/viz-contract."
