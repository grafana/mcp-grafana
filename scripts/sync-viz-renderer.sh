#!/usr/bin/env bash
# Copies a release of the viz contract Renderer of grafana/dataViz into this repository, and writes the pin
# ui/mcp-apps/renderer/RENDERER. Run it with the Renderer version when the Renderer changes:
#
#   scripts/sync-viz-renderer.sh 0.1.0
#
# It downloads the release files of grafana-echarts-renderer/v<version>, checks the build provenance of each file
# against the dataViz release workflow and the tagged commit, and checks the manifest. It needs the gh CLI and Node.
# See ui/mcp-apps/renderer/README.md.
set -euo pipefail

version="${1:?usage: scripts/sync-viz-renderer.sh <version, for example 0.1.0>}"
version="${version#v}"
repo="grafana/dataViz"
tag="grafana-echarts-renderer/v$version"
# The certificate identity of the only workflow run that may sign a Renderer release: release-please.yml on main.
signer="https://github.com/$repo/.github/workflows/release-please.yml@refs/heads/main"
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/ui/mcp-apps/renderer"
files=(renderer.js renderer.js.LICENSE.txt renderer.manifest.json icons.json package.json renderer.d.ts LICENSE)

hash() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}
json() { node -e 'console.log(require(process.argv[1])[process.argv[2]])' "$1" "$2"; }
fail() { echo "$*" >&2; exit 1; }

# An immutable release cannot have its files replaced after it is published. A gh error stops the script here.
immutable="$(gh release view "$tag" -R "$repo" --json isImmutable --jq .isImmutable)"
[ "$immutable" = true ] || fail "$tag is a release of $repo, but not an immutable one"

# The commit of the tag ref, not of a branch with the same name. An annotated tag points at a tag object first.
read -r type sha < <(gh api "repos/$repo/git/ref/tags/$tag" --jq '"\(.object.type) \(.object.sha)"')
[ "$type" = tag ] && sha="$(gh api "repos/$repo/git/tags/$sha" --jq .object.sha)"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || fail "no commit for the tag $tag"

# The release files are untrusted data until checked: they go into their own empty directory.
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
gh release download "$tag" -R "$repo" --dir "$work" "${files[@]/#/--pattern=}"
for file in "${files[@]}"; do
  [ -f "$work/$file" ] || fail "$tag has no $file"
  gh attestation verify "$work/$file" -R "$repo" --cert-identity "$signer" --deny-self-hosted-runners \
    --source-digest "$sha" >/dev/null || fail "$file of $tag has no provenance from $signer at $sha"
done

bundle_sha="$(hash "$work/renderer.js")"
# delivery.6: the sha256 of the manifest is the hash of the bundle.
[ "$(json "$work/renderer.manifest.json" sha256)" = "$bundle_sha" ] || fail "the manifest sha256 is not the hash of renderer.js"
for file in renderer.manifest.json package.json; do
  [ "$(json "$work/$file" version)" = "$version" ] || fail "$file of $tag is not version $version"
done
contract="$(json "$work/renderer.manifest.json" contractVersion)"

# The whole pin is written before any file is copied, so a failure up to here changes nothing.
{
  echo "repository: $repo"
  echo "release: $tag"
  echo "commit: $sha"
  echo "version: $version"
  echo "contractVersion: $contract"
  for file in "${files[@]}"; do
    # An assignment, so that a failed hash stops the script; a substitution in an echo would not.
    sum="$(hash "$work/$file")"
    [[ "$sum" =~ ^[0-9a-f]{64}$ ]] || fail "could not hash $file"
    echo "sha256 $file: $sum"
  done
} >"$work/RENDERER"

cp "${files[@]/#/$work/}" "$work/RENDERER" "$out/"
echo "Synced the Renderer $version (contract $contract) of $repo ($sha)."
echo "Next: mise run build-ui, npm test in ui/mcp-apps, then commit ui/mcp-apps."
