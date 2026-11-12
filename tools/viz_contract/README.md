# viz contract registry

`chart-types.json`, `pick.json` and `constants.json` are copies of `registry/` in
[`@grafana/viz-contract`](https://www.npmjs.com/package/@grafana/viz-contract), which is under the Apache License 2.0
(`LICENSE` in this directory). The `query_datasource` source embeds the first two (`tools/viz_contract.go`), and a test
checks the Go constants against the third. The Source fixtures in `tools/testdata/viz-contract` are copies too.

They are committed because `go:embed` cannot read `node_modules` and `go install` runs no npm.

## Change the contract version

Do not edit these files. Set `VIZ_CONTRACT_VERSION` in the `sync-viz-contract` task of `mise.toml`, then:

```sh
mise run sync-viz-contract
go test -tags unit ./tools -run 'VizContract|QueryDatasource'
```

Commit `mise.toml`, `tools/viz_contract` and `tools/testdata/viz-contract` together, and read the release notes of the
contract for rules that the Source must follow. CI runs the sync and fails if the copy differs from the package.
