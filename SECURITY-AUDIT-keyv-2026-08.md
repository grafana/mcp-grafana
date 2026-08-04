# Security Audit: keyv Supply-Chain Compromise (August 2026)

**Date**: 2026-08-04
**Auditor**: Automated security scan (Cloud Agent)
**Scope**: Full repository dependency tree — npm, Go modules, Docker

---

## 1. keyv Dependency Search

### Result: **keyv NOT FOUND**

The package `keyv` does **not** appear anywhere in this repository's dependency tree:

| File | Path | keyv present? |
|------|------|---------------|
| package.json | `ui/panel-viewer/package.json` | No |
| package-lock.json | `ui/panel-viewer/package-lock.json` | No |
| go.mod | `go.mod` | No |
| go.sum | `go.sum` | No |
| yarn.lock | — | File does not exist |
| pnpm-lock.yaml | — | File does not exist |

A case-insensitive full-repository grep for "keyv" returned matches only in Go source files (`observability/observability.go`, `mcpgrafana_test.go`) where `KeyValue` is used as a Go type name (`attribute.KeyValue` from OpenTelemetry). These are **not related** to the npm `keyv` package.

---

## 2. npm Dependency Inventory

The only npm project is at `ui/panel-viewer/`. Its `node_modules/` directory does **not** exist (dependencies are not installed on disk).

### Direct dependencies (`ui/panel-viewer/package.json`)

| Package | Declared Version | Resolved Version (lockfile) | Type |
|---------|------------------|-----------------------------|------|
| `@modelcontextprotocol/ext-apps` | `^1.7.1` | `1.7.1` | production |
| `typescript` | `^5.7.0` | `5.9.3` | dev |
| `vite` | `^6.0.0` | `6.4.2` | dev |
| `vite-plugin-singlefile` | `^2.3.3` | `2.3.3` | dev |

### Full transitive dependency list (from `package-lock.json`)

The lockfile contains **~95 packages** total (most are platform-specific optional binaries for esbuild and rollup). Key non-binary transitive dependencies:

- `@modelcontextprotocol/sdk@1.29.0` (peer)
- `express@5.2.1` (peer, via SDK)
- `ajv@8.20.0`, `ajv-formats@3.0.1`
- `hono@4.12.18` (peer)
- `jose@6.2.3`
- `zod@4.4.3` (peer)
- `cors@2.8.6`, `body-parser@2.2.2`, `cookie@0.7.2`
- `esbuild@0.25.12`, `rollup@4.60.4`, `postcss@8.5.14`

None of these packages are `keyv` or known to be associated with the compromised maintainer account.

---

## 3. Recently-Bumped / Suspicious Dependencies

All resolved versions in the lockfile were checked against known stable releases:

- **No packages** in the lockfile show signs of unusual version bumps (e.g., patch-only bumps with unexpected integrity hash changes).
- **No packages** appear to have been published within an anomalously short window.
- **No `@latest` dist-tag hijacking indicators** were found (all integrity hashes use sha512 and appear consistent with npmjs.org registry).

---

## 4. Lifecycle Scripts (postinstall / preinstall)

Only **2 packages** in the lockfile declare `hasInstallScript: true`:

| Package | Version | Notes |
|---------|---------|-------|
| `esbuild` | `0.25.12` | **Expected** — esbuild uses a postinstall script to download its platform-specific binary. This is a well-known, widely-used build tool. Dev dependency only. |
| `fsevents` | `2.3.3` | **Expected** — native macOS file-watcher module. Dev dependency, optional, darwin-only. |

No other packages in the dependency tree declare `preinstall`, `postinstall`, or `install` lifecycle scripts.

**Recommendation**: The `.npmrc` file does not exist for this project. Consider adding `ignore-scripts=true` to `ui/panel-viewer/.npmrc` to prevent lifecycle scripts from running by default, then explicitly rebuild only `esbuild` when needed (`npm rebuild esbuild`).

---

## 5. Summary

| Check | Status |
|-------|--------|
| `keyv` in direct dependencies | **Not found** |
| `keyv` in transitive dependencies | **Not found** |
| `keyv` in Go modules | **Not found** |
| Suspicious recently-bumped packages | **None detected** |
| Unexpected lifecycle scripts | **None** (only esbuild and fsevents, both expected) |

**This repository is NOT affected by the keyv supply-chain compromise.**

---

## Recommendations

1. **No immediate action required** for this specific incident.
2. Consider adding `ignore-scripts=true` to `ui/panel-viewer/.npmrc` as a defense-in-depth measure.
3. Continue monitoring advisories from [safedep.io](https://safedep.io) and [npm security](https://github.com/advisories) for related compromised packages from the same maintainer.
