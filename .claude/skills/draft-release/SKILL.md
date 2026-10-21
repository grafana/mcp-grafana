---
name: draft-release
description: Draft a new release. Bumps version, generates CHANGELOG.md from git history, and creates a PR.
argument-hint: <major|minor|patch>
allowed-tools: Bash(git *), Bash(gh *), Bash(date *), Read, Write, Edit, Glob
---

# Draft Release

You are creating a new release for the mcp-grafana project. The user has provided a bump level: `major`, `minor`, or `patch`.

## How the release pipeline works

1. This skill creates a `release/v*` branch with a CHANGELOG.md update and opens a PR
2. When the PR is merged, the `auto-tag.yml` workflow automatically creates the git tag
3. The tag triggers `release.yml` (goreleaser → GitHub Release + binaries) and `docker.yml` (Docker images + MCP Registry)

Your job is step 1 only. Do NOT create tags manually.

## Step 1: Determine the new version

1. Get the latest semver tag:
   ```
   git tag --sort=-version:refname | head -1
   ```
2. Parse the tag as `vMAJOR.MINOR.PATCH` and bump according to the argument:
   - `major` → increment MAJOR, reset MINOR and PATCH to 0
   - `minor` → increment MINOR, reset PATCH to 0
   - `patch` → increment PATCH
3. The new version string is `v<MAJOR>.<MINOR>.<PATCH>` (e.g. `v0.11.0`). The previous tag is the "from" ref.

## Step 2: Create the release branch

```
git checkout main
git pull origin main
git checkout -b release/v<NEW_VERSION>
```

## Step 3: Gather and categorize commits

Run:
```
git log <PREV_TAG>..HEAD --no-merges --format="%H %s"
```

For each commit, categorize by its conventional commit prefix:

| Prefix | CHANGELOG Section |
|--------|------------------|
| `feat` | **Added** |
| `fix` (security-related) | **Security** |
| `fix` | **Fixed** |
| `refactor`, `perf` | **Changed** |
| `BREAKING CHANGE` or `!:` | **Removed** (or note as breaking in relevant section) |

**Skip these commits entirely:**
- `chore(deps)` — dependency bumps
- `chore` commits that only touch CI config, GitHub Actions, or similar
- `docs` commits that only update README or non-user-facing docs
- `test` commits that only add/fix tests
- `ci` commits

## Step 4: Write human-readable entries

For each significant commit:

1. Read the full commit message: `git log <hash> -1 --format="%B"`
2. Extract the PR number from the commit message (look for `(#NNN)` in the subject or a `PR:` line)
3. Derive the repo URL: `git remote get-url origin` → extract `https://github.com/<org>/<repo>`
4. Write a concise, human-readable description of the change (not just the commit subject). Focus on what the user gains.
5. Format as: `- Description of the change ([#NNN](https://github.com/<org>/<repo>/pull/NNN))`

If a commit lacks a PR number, omit the link.

## Step 5: Write CHANGELOG.md

The CHANGELOG follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format.

### If CHANGELOG.md already exists

Read the existing file. Insert the new version section **after** the header block and **before** any existing version sections. Preserve all existing content below.

### If CHANGELOG.md does not exist

Create it with the full header.

### Exact format

```markdown
# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [<NEW_VERSION_WITHOUT_V>] - <YYYY-MM-DD>

### Added

- Entry one ([#123](https://github.com/<org>/<repo>/pull/123))
- Entry two ([#456](https://github.com/<org>/<repo>/pull/456))

### Fixed

- Entry three ([#789](https://github.com/<org>/<repo>/pull/789))

### Changed

- Entry four ([#101](https://github.com/<org>/<repo>/pull/101))

### Security

- Entry five ([#102](https://github.com/<org>/<repo>/pull/102))

## [<PREV_VERSION_WITHOUT_V>] - <PREV_DATE>
...existing entries...

[<NEW_VERSION_WITHOUT_V>]: https://github.com/<org>/<repo>/compare/<PREV_TAG>...<NEW_TAG>
[<PREV_VERSION_WITHOUT_V>]: https://github.com/<org>/<repo>/compare/...
```

**Rules:**
- Version numbers in headings and link labels do NOT have the `v` prefix (e.g. `[0.11.0]`)
- Tags in compare URLs DO have the `v` prefix (e.g. `v0.11.0`)
- Date format is `YYYY-MM-DD` (use `date +%Y-%m-%d`)
- Only include sections that have entries (omit empty sections like **Changed** if there are no changes)
- The compare link for the new version goes at the bottom, before existing compare links
- Order sections: Added, Fixed, Changed, Security, Removed

## Step 6: Customer communications

For every major release, or minor release containing breaking changes, prepare customer communications:

- Use [grafana-product:whats-new](https://github.com/grafana/ai-kit/tree/main/plugins/grafana-product/skills/whats-new) for new or improved capabilities.
- Use [grafana-product:whats-next](https://github.com/grafana/ai-kit/tree/main/plugins/grafana-product/skills/whats-next) for breaking changes, deprecations, removals, or disruptive behaviour changes (e.g. renamed or removed tools, changed flags or env vars, changed tool arguments).
- If both apply, prepare separate entries. If neither applies (e.g. a patch release), skip this step and say why in the report.

Identify the required entries by inspecting the source PRs and compatibility changes, not just the commit prefixes or the CHANGELOG you just wrote. Reuse existing contributions where appropriate.

Read the relevant upstream skill and its required references, using `gh` to retrieve them if they are not installed. Follow their content and formatting guidance, but override their advance-notice timing: prepare the content during the release, without requiring a 2–4 week lead time or delaying the release to create one. Describe the actual release timing; do not present an already available change as upcoming.

Include affected users, exact before/after behaviour, and concrete migration instructions for breaking changes. Verify CMS metadata rather than treating an mcp-grafana version as a Grafana self-managed release version.

Do not commit the drafts to the release branch.

## Step 7: Commit

```
git add CHANGELOG.md
git commit -m "docs: add CHANGELOG.md for v<NEW_VERSION> release"
```

## Step 8: Push and create PR

```
git push -u origin release/v<NEW_VERSION>
```

Create a PR with:
```
gh pr create --title "Release v<NEW_VERSION>" --body "$(cat <<'EOF'
## Summary

- Bump version to v<NEW_VERSION>
- Add CHANGELOG.md entries for this release

## Checklist

- [ ] CHANGELOG entries are accurate and complete
- [ ] Version number is correct
- [ ] All significant changes are documented

> [!NOTE]
> Merging this PR will automatically create the `v<NEW_VERSION>` tag,
> which triggers goreleaser (GitHub Release + binaries) and Docker image builds.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

## Step 9: Report

Print a summary:
- New version: `v<NEW_VERSION>`
- Branch: `release/v<NEW_VERSION>`
- PR URL (from `gh pr create` output)
- Number of changelog entries added, broken down by section
- Remind the user: merging the PR will automatically tag and release
- Customer communications: draft paths and CMS submission/publication status for each required What's new / What's next entry. If submission is outstanding, name the comms owner and remaining action; a local draft alone is not a submitted contribution. If none was required, state why.
