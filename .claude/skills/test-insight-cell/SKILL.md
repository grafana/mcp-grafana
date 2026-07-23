---
name: test-insight-cell
description: Exercise the render_insight_cell MCP App tool end-to-end. Builds the server, drives every panel type, and verifies the render contract (three output channels + the grafana.insightCell/v0 trust block).
allowed-tools: Bash(make *), Bash(go *), Bash(./dist/mcp-grafana*), Bash(jq *), Bash(npx *), Read, Glob
---

# Test the insight cell

You are verifying the `render_insight_cell` tool and its MCP App render surface in the
mcp-grafana repo. The tool packages agent-gathered data into an "insight cell" that a host
renders inline; the UI lives in `ui/insight-cell/` and the contract in `tools/insight_cell.go`
(mirrored from `contrib/insight-cell/src/schema.ts`). See `docs/mcp-apps.md` for the full contract.

The tool does NOT query datasources — it renders data the agent already has. So testing it means
checking that, for each `panel` type, the result carries all three channels and the trust `_meta`.

## Step 1: Build

```
make build-ui        # rebuild ui/insight-cell/dist/mcp-app.html if src changed
make build           # go build -> dist/mcp-grafana (embeds the bundle)
```

If `make build-ui` reports no Node, that's fine as long as `ui/insight-cell/dist/mcp-app.html`
already exists (it's committed for `//go:embed`).

## Step 2: Unit + contract tests

```
go test ./tools/ -run InsightCell -v
go test . -run 'AppResources|RegisterAppResources' -v
```

These assert `_meta.ui.resourceUri`, `structuredContent`, the embedded JSON resource block, the
`grafana.insightCell/v0` trust block, and that both apps (panel-viewer + insight-cell) register.

## Step 3: Drive the tool over the protocol

Run the built server over stdio and call the tool for each panel type. The simplest driver is an
MCP `tools/call` JSON-RPC exchange. For each `panel`, confirm the result contains:

1. `content[0].type == "text"` — the verdict fallback.
2. `content[1]` — an embedded `application/json` resource whose text parses to an object with a
   `renderHint`.
3. `structuredContent.renderHint.type` == the requested `panel`.
4. `_meta.ui.resourceUri == "ui://mcp-grafana/insight-cell.html"`.
5. `_meta["grafana.insightCell/v0"]` present, with `reasoning.source == "agent"`.

Panel types to cover and a representative payload for each:

| panel       | minimal args |
|-------------|--------------|
| `stat`      | `frames:[{fields:[{name:"value",type:"number",values:[0.42]}]}]`, `unit:"percent"` |
| `timeseries`| `frames` with a `time` field + one number field |
| `bar`       | `frames` with a string field + a number field, `sort:"desc"` |
| `table`     | `frames` with several fields |
| `logs`      | `logs:[{time,level,line}]` |
| `worklist`  | `items:[{title,priority,status,why}]` |
| `rca`       | `rootCause:{title,confidence}`, `findings:[{title,evidence}]` |
| `rulediff`  | `ruleTitle`, `changes:[{field,before,after}]` |
| `timeline`  | `events:[{time,title,kind,correlated:true}]` |
| `cost`      | `drivers:[{name,series}]`, `costTotal:{label,value}` |

Verify `dataMode`: it is `"live"` only when both `query` and `datasourceUid` are supplied,
otherwise `"mock"` (representative/sample content).

## Step 4: Render in a host (visual check)

The render surface only draws in a host that supports the MCP Apps extension. Two options:

- **Basic-host (fastest, matches #825's dev loop):** run the server with `--dev-cors` on the
  streamable-HTTP transport (`make run-streamable-http` or the equivalent flags), connect the
  [MCP Apps basic-host](https://github.com/modelcontextprotocol/ext-apps/tree/main/examples/basic-host),
  call `render_insight_cell`, and confirm the iframe renders and the action buttons
  (refresh / drill / open link) round-trip.
- **Claude Desktop:** load the plugin (`.claude-plugin/plugin.json`), ask a question that leads
  the agent to call `render_insight_cell`, and confirm the cell renders inline.

## Step 5: Report

Summarize: which panel types passed the channel/`_meta` checks, whether the host render worked,
and any contract drift between `tools/insight_cell.go` and `ui/insight-cell/src/schema.ts`.
