# Developing mcp-grafana

## Prerequisites

- Go (latest stable)
- Docker and docker-compose (for integration/E2E tests)
- [uv](https://docs.astral.sh/uv/) (for Python E2E tests)
- [golangci-lint](https://golangci-lint.run/) (for linting)

## Building

```bash
make build          # builds dist/mcp-grafana
make build-image    # builds Docker image
```

## Running locally

Start the test services (Grafana, Prometheus, etc.):

```bash
make run-test-services
```

Then run the server in your preferred transport mode:

```bash
make run                 # stdio mode
make run-sse             # SSE mode (with debug logging + metrics)
make run-streamable-http # Streamable HTTP mode (with debug logging + metrics)
```

## Testing

```bash
make test-unit           # unit tests (no external deps)
make test-integration    # integration tests (requires docker-compose services)
make test-cloud          # cloud tests (requires GRAFANA_SERVICE_ACCOUNT_TOKEN)
make test-python-e2e     # Python E2E tests (requires docker-compose + SSE server)
```

## Linting

```bash
make lint                # Go lint + JSON schema lint
```

## Publishing a release

Releases follow a branch-based workflow with automated tagging and publishing.

### Overview

```
/draft-release minor
  → creates release/v0.11.0 branch with CHANGELOG.md update
    → opens PR for review
      → merge PR into main
        → auto-tag.yml creates v0.11.0 tag on the merge commit
          → release.yml: goreleaser builds binaries + creates GitHub Release
          → docker.yml: builds and pushes Docker images + publishes to MCP Registry
```

### Step by step

1. **Create the release branch and PR.** If you use Claude Code, run:

   ```
   /draft-release <major|minor|patch>
   ```

   This determines the next version from the latest git tag, gathers commits since the last release, generates CHANGELOG.md entries in [Keep a Changelog](https://keepachangelog.com/) format, and opens a PR.

   To do it manually:

   ```bash
   # Determine the new version from the latest tag
   git tag --sort=-version:refname | head -1   # e.g. v0.10.0 → next minor is v0.11.0

   git checkout main && git pull
   git checkout -b release/v0.11.0

   # Update CHANGELOG.md with the new version's entries
   # (see existing entries for format)

   git add CHANGELOG.md
   git commit -m "docs: add CHANGELOG.md for v0.11.0 release"
   git push -u origin release/v0.11.0
   gh pr create --title "Release v0.11.0"
   ```

2. **Review the PR.** Verify the CHANGELOG entries are accurate and complete.

3. **Merge the PR.** Once merged, the `auto-tag.yml` workflow automatically creates a `v0.11.0` tag on the merge commit.

   > **Note:** The auto-tag workflow requires a GitHub App token to trigger downstream workflows. Until that is configured, you'll need to manually push the tag after merging:
   >
   > ```bash
   > git checkout main && git pull
   > git tag v0.11.0 HEAD
   > git push origin v0.11.0
   > ```

4. **Automated publishing.** The tag triggers:
   - **goreleaser** — builds binaries for Linux/macOS/Windows and creates a GitHub Release with notes extracted from CHANGELOG.md
   - **Docker** — builds and pushes `grafana/mcp-grafana:0.11.0` (+ `:latest` for stable releases) and Alpine variants
   - **MCP Registry** — publishes the updated server metadata

### CHANGELOG format

The project uses [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) with these sections (only include sections that have entries):

- **Added** — new features (`feat` commits)
- **Fixed** — bug fixes (`fix` commits)
- **Changed** — refactors and performance improvements (`refactor`/`perf` commits)
- **Security** — security-related fixes
- **Removed** — removed features or breaking changes

Each entry should be a concise, human-readable description with a link to the PR:

```markdown
- Description of the change ([#123](https://github.com/grafana/mcp-grafana/pull/123))
```
