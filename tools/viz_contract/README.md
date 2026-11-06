# viz contract registry

`chart-types.json` and `pick.json` are copies of `registry/` in [grafana/viz-contract](https://github.com/grafana/viz-contract),
which is under the Apache License 2.0 (`LICENSE` in this directory). The `query_datasource` source reads them
(`tools/viz_contract.go`). The Source fixtures in `tools/testdata/viz-contract` are copies too.

`CONTRACT` pins the copy: the commit and the version of the contract, and the SHA-256 of each registry file. A unit
test fails if a file differs from its pin, so a hand edit does not pass.

## Change the contract version

Do not edit these files. Run the sync script with a commit or a tag of the contract. It needs the `gh` CLI with read
access to `grafana/viz-contract`:

```sh
scripts/sync-viz-contract.sh <ref>
go test -tags unit ./tools -run 'VizContract|QueryDatasource'
```

Then commit `tools/viz_contract` and `tools/testdata/viz-contract` together, and read the `CHANGELOG.md` of the
contract for rules that the Source must follow.

## Not checked yet

Nothing checks the pin against the contract itself, because the contract is not public and `@grafana/viz-contract` is
not on npm yet. When the package is published, a test can compare the copies with the package at the pinned version.
