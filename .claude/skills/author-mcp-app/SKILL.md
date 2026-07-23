---
name: author-mcp-app
description: Scaffold a new MCP App (an interactive HTML render surface) in mcp-grafana — create the ui/<name> bundle, embed it, register it in the app registry, and wire it to a tool via WithUIResource.
argument-hint: <app-name>
allowed-tools: Bash(make *), Bash(go *), Bash(npm *), Bash(cp *), Bash(mkdir *), Read, Write, Edit, Glob
---

# Author a new MCP App

You are adding a new MCP App to mcp-grafana. An MCP App is a self-contained HTML bundle that a
host renders inline in a sandboxed iframe when a tool returns `_meta.ui.resourceUri` pointing at
it. The existing apps are `ui/panel-viewer/` (for `get_panel_image`) and `ui/insight-cell/` (the
generic render surface). Read `docs/mcp-apps.md` first — it documents the framework these steps wire into.

The user gives an app name (kebab-case), e.g. `log-viewer`. Use it consistently below.

## Step 1: Create the UI bundle

Scaffold `ui/<name>/` mirroring an existing app (copy `ui/insight-cell/` for a rich app, or
`ui/panel-viewer/` for a minimal one):

```
mkdir -p ui/<name>/src
```

Create these files (see `ui/insight-cell/` for reference):
- `package.json` — `"@mcp-grafana/<name>-app"`, private, `build` script `INPUT=mcp-app.html vite build`.
- `vite.config.ts` — `viteSingleFile()` plugin (single-file output → no external requests, so the
  MCP Apps deny-by-default CSP needs no config).
- `tsconfig.json`.
- `mcp-app.html` — a `<div id="root">` and `<script type="module" src="/src/mcp-app.ts">`.
- `src/mcp-app.ts` — construct `new App(...)`, `app.connect()`, and in `app.ontoolresult` read the
  tool result (prefer `structuredContent`, fall back to the embedded JSON resource block, then to a
  JSON text block — hosts vary in what they preserve) and render it.

Do NOT copy any Node server files (`server.ts`, `data.ts`, `loadenv.ts`) — an MCP App is
client-only; the Go server serves the bundle.

## Step 2: Build the bundle

```
cd ui/<name> && npm install && npm run build && cd ../..
```

This produces `ui/<name>/dist/mcp-app.html`. It is committed (the root `.gitignore` re-includes
`!ui/*/dist/`) so `go build` needs no Node. `node_modules/` is ignored.

## Step 3: Embed and register it (Go side)

All in the repo root package `mcpgrafana`:

1. **`ui_embed.go`** — add:
   ```go
   //go:embed ui/<name>/dist/mcp-app.html
   var <name>AppHTML string
   ```
2. **`ui_apps.go`** — add a resource-URI const next to the others:
   ```go
   <Name>ResourceURI = "ui://mcp-grafana/<name>.html"
   ```
   and append an entry to the `appResources` registry:
   ```go
   {URI: <Name>ResourceURI, Name: "<Human Name>", Description: "...", HTML: <name>AppHTML},
   ```
   `RegisterAppResources` already loops the registry, so nothing else changes.

## Step 4: Wire it to a tool

The producing tool advertises the app in its definition and emits `_meta.ui.resourceUri` on its
result. In the tool's `MustTool(...)` options add:

```go
mcpgrafana.WithUIResource(mcpgrafana.<Name>ResourceURI),
```

and in the handler return a `*mcp.CallToolResult` whose `Result.Meta.AdditionalFields["ui"]` is
`map[string]any{"resourceUri": mcpgrafana.<Name>ResourceURI}`. For best host compatibility, also
set `structuredContent` and include the data as an embedded `application/json` resource block in
`Content` (see `tools/insight_cell.go:insightCellResult` for the three-channel pattern).

## Step 5: Verify

```
make build-ui && make build      # embeds the new bundle
go test . -run AppResources -v   # assert the new app is in the registry
go test ./tools/ -run <ToolMeta> # assert the tool advertises the resource URI
```

Add a test mirroring `TestAppResourcesRegistry` (root) and the `*ToolMeta` tests in `tools/` for
the new app + tool. Then render it in a host (see the `test-insight-cell` skill, Step 4).

## Step 6: Report

Summarize the files added/changed, the resource URI, and the tool it's wired to.
