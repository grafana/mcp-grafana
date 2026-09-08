# Apollo fork operations

This fork adds strict, fail-closed Loki query guardrails for autonomous clients. Keep this patch small, deploy it from an Apollo-owned image, and review it whenever upstream changes Loki tools or query paths.

## Runtime contract

- Grafana datasource UID `ffuyoihkmhiwwf` uses the Cloud Access Policy token with `lokiQueryPolicy.maxQueryBytesRead: "5GB"`.
- The MCP server continues to use its Grafana service-account token. The CAP token remains in the datasource and is not copied into the MCP deployment.
- Strict mode accepts only the allowlisted Loki datasource, requires a real line filter, adds a semantics-preserving non-empty filter for CAP enforcement, applies local byte and time-range checks, and fails closed when validation cannot complete.
- The local byte check uses Loki's estimate and is intentionally set below the 5 GB CAP limit. The CAP remains the backend enforcement layer for filtered queries.

## Deployment

A deployment change is required. Merging this repository does not update the running MCP service.

Build and publish this fork to Apollo's existing container repository, `us-docker.pkg.dev/apollo-ops/us.gcr.io/mcp/grafana`, with an immutable Git SHA tag. Do not use `grafana/mcp-grafana:latest`, because it will not contain Apollo's guardrail.

In `apolloio/deployments`, update `gke/clusters/ops/grafana-mcp/values.helm`:

```yaml
image:
  registry: us-docker.pkg.dev/apollo-ops/us.gcr.io/mcp
  repository: grafana
  tag: <immutable-fork-image-tag>
```

Remove `--disable-loki`, retain the existing Grafana URL and service-account secret, and add:

```yaml
extraArgs:
  # Keep the existing transport and unrelated disable flags.
  - --disable-write
  - --disable-api
  - --disable-rendering
  - --disable-runpanelquery
  - --loki-guardrail-mode
  - strict
  - --loki-guardrail-max-bytes
  - "4000000000"
  - --loki-guardrail-max-range
  - 24h
  - --loki-allowed-datasource-uids
  - ffuyoihkmhiwwf
```

Do not add `--dynamic-multi-org`. No Helm chart version change is required because the existing chart already supports the image override and `extraArgs`. The rollout needs a second PR in `apolloio/deployments`; this code PR alone cannot activate the guardrail.

## Rollout verification

1. Deploy the new image to a canary or non-production instance.
2. Confirm the server starts and exposes the expected read-only Grafana tools.
3. Confirm a filterless query such as `{cluster="prod"}` is rejected by MCP with guidance to add a line filter.
4. Confirm a narrow query such as `{cluster="prod"} |= "error"` over a short window succeeds when it is below both limits.
5. Confirm a query using any Loki datasource UID other than `ffuyoihkmhiwwf` is rejected.
6. Confirm a broad filtered query is rejected by the 5 GB CAP. The known [Grafana Explore verification query](https://apolloio.grafana.net/explore?schemaVersion=1&panes=%7B%22wxo%22%3A%7B%22datasource%22%3A%22ffuyoihkmhiwwf%22%2C%22queries%22%3A%5B%7B%22refId%22%3A%22A%22%2C%22expr%22%3A%22%7Bcluster%3D%5C%22prod%5C%22%7D%20%7C~%20%5C%22(%3Fs).*%5C%22%22%2C%22queryType%22%3A%22range%22%2C%22datasource%22%3A%7B%22type%22%3A%22loki%22%2C%22uid%22%3A%22ffuyoihkmhiwwf%22%7D%2C%22editorMode%22%3A%22code%22%2C%22direction%22%3A%22backward%22%7D%5D%2C%22range%22%3A%7B%22from%22%3A%22now-24h%22%2C%22to%22%3A%22now%22%7D%2C%22panelsState%22%3A%7B%22logs%22%3A%7B%22visualisationType%22%3A%22logs%22%7D%7D%2C%22compact%22%3Afalse%7D%7D) currently demonstrates this behavior.
7. Roll out broadly only after the canary checks pass.

## Syncing upstream

Add the upstream remote once:

```bash
git remote add upstream https://github.com/grafana/mcp-grafana.git
```

For each upstream bump, use a short-lived branch and PR. Do not rewrite the shared Apollo `main` branch:

```bash
git fetch upstream --tags
git switch main
git pull --ff-only origin main
git switch -c sync-upstream-YYYYMMDD
git merge --no-ff upstream/main
make test-unit
make lint
make build-image
```

Before merging an upstream bump:

- Review conflicts in `cmd/mcp-grafana/main.go`, `mcpgrafana.go`, and `tools/loki*.go` carefully.
- Check for new Loki execution tools or generic Grafana API paths that could bypass strict validation. Guard or disable them before deployment.
- Repeat the rollout verification above.
- Publish a new immutable image and update the deployment image reference in a separate deployment PR.
- Record the upstream commit and deployed image SHA in that PR so the next bump has a clear baseline.
