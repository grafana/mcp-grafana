# MCP Apps in mcp-grafana

An **MCP App** is a self-contained HTML UI that a tool returns for a host to render inline, in a
sandboxed iframe, using the [MCP Apps extension](https://modelcontextprotocol.io/extensions/apps/overview).
Hosts that support it (Claude Desktop, VS Code Copilot, Goose, …) render the app; hosts that don't
ignore the metadata and fall back to the tool's text result. This is the mechanism behind
"OSS Grafana outside the UI": a render surface that travels with the MCP server.

This doc covers the framework that serves these apps, how a tool links to one, and the render
contract used by the **insight cell**. It builds on the foundation from
[#825](https://github.com/grafana/mcp-grafana/pull/825) (the original `WithUIResource` /
`RegisterAppResources` / `//go:embed` / `make build-ui` pattern and the first app, `ui/timeseries/`).

## The framework

Apps are registered in a single registry and served over the MCP Resources API.

- **`ui_apps.go`** — the `UIApp` struct and the `appResources` registry. `RegisterAppResources(s)`
  (called once from `cmd/mcp-grafana/main.go`) loops the registry and serves each app's HTML at its
  `ui://` URI with MIME type `text/html;profile=mcp-app`. Adding an app is one registry entry.
- **`ui_embed.go`** — each built bundle is embedded into the binary with `//go:embed`.
- **`ui/<name>/`** — one directory per app, a Vite + `vite-plugin-singlefile` project that builds a
  single `dist/mcp-app.html` (everything inlined, so the deny-by-default CSP needs no config). The
  built file is committed (root `.gitignore` re-includes `!ui/*/dist/`); `node_modules/` is ignored.
- **`make build-ui`** — rebuilds every `ui/*/` bundle. Run it when an app's `src/` changes; the
  committed `dist/mcp-app.html` is what `go build` embeds, so Node is not needed to build the server.

Current apps:

| App | Resource URI | Wired to |
|-----|--------------|----------|
| Panel Viewer | `ui://mcp-grafana/panel-viewer.html` | `get_panel_image` |
| Insight Cell | `ui://mcp-grafana/insight-cell.html` | `render_insight_cell` |

To add a new one, use the **`author-mcp-app`** skill (`.claude/skills/author-mcp-app/`).

## Linking a tool to an app

A tool advertises its app in its definition and marks the app on its result:

```go
// definition — advertises the resource URI in the tool's _meta.ui
mcpgrafana.MustTool(name, desc, handler,
    mcpgrafana.WithUIResource(mcpgrafana.InsightCellResourceURI),
)
```

The host reads `_meta.ui.resourceUri` from the tool listing, fetches the resource, and renders it.
The result's own `_meta.ui.resourceUri` tells the host which app to hand this specific result to.

## The three output channels

Hosts differ in what they preserve, so a result carries the render payload three ways (see
`tools/insight_cell.go:insightCellResult`). An app should read them in this order:

1. **`structuredContent`** — the payload object (the spec channel). Some hosts drop this.
2. **an embedded `application/json` resource block** in `content[]` — kept by hosts that drop
   `structuredContent`. Claude Desktop converts it to a text block, so the app also scans text
   blocks for JSON starting with `{`.
3. **`content[0]` text** — a human-readable verdict; the fallback when there is no app at all.

## The insight-cell render contract

The insight cell is a generic surface: **one contract + one renderer that dispatches on
`renderHint.type`**. Adding a visualization is a new branch in the renderer, not a new app. The Go
types in `tools/insight_cell.go` mirror `contrib/insight-cell/src/schema.ts` (the source of truth
for field names — they must match, since the embedded UI reads them).

`render_insight_cell` is a **render substrate**: the agent gathers data with the existing query
tools (`query_prometheus`, `query_loki_logs`, `list_alert_rules`, `get_annotations`, Sift, …), does
the analysis, and passes the results here. The tool does not query datasources or fabricate data.

Render types:

- **Core panels:** `timeseries`, `stat`, `bullet` (a value vs a target/SLO with qualitative
  bands), `bar`, `table` (read `frames`), `logs` (read `logs`), `trace` (read `trace`).
- **Synthesis views:** `worklist` (ranked triage), `rca` (root cause → evidence), `timeline`
  (change correlation), `cost` (cardinality/spend drivers).
- **Guardrailed write:** `rulediff` renders a proposed alert-rule change as a before/after diff.
  Applying it routes to the existing **write-gated** `update_alert_rule` tool — the cell proposes,
  it does not write.

### The trust profile: `_meta["grafana.insightCell/v0"]`

Every result carries a trust profile alongside the data, so the cell is auditable regardless of
host. It contains:

- `query` — the expressions and datasource UIDs the data came from.
- `attestation` — `{ asOf, live }`: when it was produced and whether it reflects a live datasource.
- `provenance` — `{ author, datasource, orgId?, rbacScope? }`.
- `renderHint` — the declarative "how to draw it" (type, unit, thresholds, mappings, …).
- `reasoning` — `{ question, verdict, confidence, source }`; `source: "agent"` marks agent analysis.

`dataMode` is `"live"` only when both a `query` and a `datasourceUid` are supplied; otherwise
`"mock"` (representative/sample content).

## Testing

Use the **`test-insight-cell`** skill (`.claude/skills/test-insight-cell/`) to build, run the
contract tests, drive every panel type over the protocol, and render in a host. The fastest visual
loop is `--dev-cors` + the MCP Apps basic-host, matching #825's development flow.
