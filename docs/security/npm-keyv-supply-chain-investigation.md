# NPM Supply-Chain Investigation: keyv and Related Packages

**Date**: 2026-08-04
**Scope**: Sept–Nov 2025 npm supply-chain compromise wave (keyv, chalk, debug, ansi-styles, cacheable, cache-manager, etc.)
**Repository**: grafana/mcp-grafana

## Summary

**This repository is NOT affected by the keyv supply-chain compromise.**

The `keyv` package is not present anywhere in this repository's dependency tree — neither as a direct nor transitive dependency. The repository is primarily a Go project; its only npm sub-project (`ui/panel-viewer/`) has a small dependency footprint (166 packages) and does not include `keyv` or most of the other packages targeted in the compromise campaign.

## Detailed Findings

### 1. keyv — NOT PRESENT

| Search location | Result |
|---|---|
| `ui/panel-viewer/package.json` (direct deps) | Not found |
| `ui/panel-viewer/package-lock.json` (full tree) | Not found |
| `go.mod` / `go.sum` | Not applicable (Go deps) |
| No `yarn.lock` or `pnpm-lock.yaml` files exist | N/A |

### 2. Related Compromised Packages — Status

Of the 10 related packages checked, only `debug` is present:

| Package | Present | Version | Type | Notes |
|---|---|---|---|---|
| **keyv** | No | — | — | — |
| **debug** | **Yes** | 4.4.3 | Transitive | See analysis below |
| chalk | No | — | — | — |
| ansi-styles | No | — | — | — |
| supports-color | No | — | — | Referenced only as optional peerDep of debug; not installed |
| strip-ansi | No | — | — | — |
| color-convert | No | — | — | — |
| color-name | No | — | — | — |
| wrap-ansi | No | — | — | — |
| cacheable | No | — | — | — |
| cache-manager | No | — | — | — |

### 3. debug@4.4.3 — Analysis

**Location**: `ui/panel-viewer/package-lock.json` (transitive dependency)

**Parent packages** (depend on debug transitively):
- `body-parser@2.2.2` → debug ^4.4.3
- `express@5.2.1` → debug ^4.4.0
- `finalhandler@2.1.1` → debug ^4.4.0
- `router@2.2.0` → debug ^4.4.0
- `send@1.2.1` → debug ^4.4.3

**Integrity verification**:
- Lockfile integrity: `sha512-RGwwWnwQvkVfavKVt22FGLw+xYSdzARwm0ru6DhTVA3umU5hZc28V3kO4stgYryrTlLpuvgI9GiijltAjNbcqA==`
- npm registry integrity: `sha512-RGwwWnwQvkVfavKVt22FGLw+xYSdzARwm0ru6DhTVA3umU5hZc28V3kO4stgYryrTlLpuvgI9GiijltAjNbcqA==`
- **Match: YES** — the lockfile hash matches the current npm registry hash.

**Lifecycle scripts**: `debug@4.4.3` has **no** postinstall, preinstall, install, or prepare scripts.

**Published**: 2025-09-13 — falls within the compromise window, but the package integrity is intact and no malicious scripts are present.

### 4. Postinstall Script Audit

A full scan of all 166 packages in `ui/panel-viewer/package-lock.json` found **zero** packages with `postinstall`, `preinstall`, `install`, or `prepare` lifecycle scripts.

### 5. Lockfile Change History (Last 60 Days)

Recent commits touching `ui/panel-viewer/package-lock.json`:

| Commit | Description |
|---|---|
| `14dd8c8` | fix(deps): update all non-major dependencies |
| `d4cd2df` | chore(deps): update dependency typescript to v7 |
| `265fa1a` | Add MCP App panel viewer for get_panel_image (#882) |
| `0867314` | npm i |
| `770575f` | add MCP App panel viewer for get_panel_image |

No commits introduced `keyv` into the lockfile. The `14dd8c8` commit bumped `vite` from 6.4.2 to 6.4.3 and `debug` remained at 4.4.3.

## Conclusion

| Check | Status |
|---|---|
| keyv present in dependency tree | **No** |
| Compromised package versions detected | **No** |
| Suspicious postinstall scripts | **No** |
| Lockfile integrity mismatch | **No** |
| Recent suspicious version bumps | **No** |

**Risk level: NONE** — This repository is not affected by the keyv/cacheable npm supply-chain compromise. The only related package present (`debug@4.4.3`) has a verified integrity hash matching the npm registry, contains no lifecycle scripts, and shows no signs of tampering.
