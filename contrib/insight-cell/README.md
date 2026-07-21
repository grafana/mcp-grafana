# Grafana Insight Cell — a generic MCP App render surface

An **MCP App** that renders an **insight cell** — an interactive, trustworthy
card drawn inline in an MCP host (e.g. Claude Desktop) via a `ui://` HTML
resource. One tool, `grafana_render`, can draw **any core Grafana panel**
(timeseries, stat, bar, table) plus **logs** and **traces**, and a family of
**synthesis views** (alert-triage worklist, root-cause investigation,
change-correlation timeline, cost/cardinality), plus a guardrailed **write**
action (propose an alert-rule fix → before/after diff → apply).

Think of it as **"Grafana outside its UI"**: a single generic rendering surface
for anything that comes through the Grafana MCP server, wrapped in the
insight-cell trust metadata (verdict, attestation, provenance, reasoning).

It works on representative **sample data** out of the box, and on **live
Grafana** data once you add a token.

### In Claude Desktop, on live Grafana Cloud data

A live time-series **correlation** — two metrics reconciled onto a shared axis, with the agent's synthesis (memory-bound, not CPU-bound), carrying its query and "as of" line:

<img width="720" alt="Live correlation time-series insight cell" src="https://github.com/user-attachments/assets/b828cc64-1921-4093-8645-aef19b2783d7" />

A live alert-triage **worklist** with the query & provenance footer expanded — attestation, datasource, time range, RBAC scope, live/confidence/data-mode:

<img width="720" alt="Live worklist insight cell with provenance footer" src="https://github.com/user-attachments/assets/9cb4e5c2-acdb-48a5-ba54-dd769a9db3c3" />

A triage **worklist** that synthesizes three firing alerts into ranked findings — each with the *why* and a recommended fix action, not just raw metrics:

<img width="720" alt="Triage worklist with recommended fix actions" src="https://github.com/user-attachments/assets/65be7c15-30e5-4ee0-93e0-65deee846ee0" />

The full render-type gallery (sample data) is in [`docs/preview.png`](docs/preview.png).

This builds on the approach Grafana is prototyping OSS-first in
[`mcp-grafana` PR #825](https://github.com/grafana/mcp-grafana/pull/825), which
attaches a uPlot time-series MCP App to `query_prometheus`. The insight cell
generalizes that single chart into **one render contract + one renderer that
dispatches on a render hint**, and adds the trust metadata
(`grafana.insightCell/v0`: attestation, provenance, reasoning) carried in
`_meta`. It's a standalone Node/TypeScript prototype so the renderer and contract
can be iterated on quickly; the same bundle could be embedded (`//go:embed`) into
`mcp-grafana` the way #825 embeds its viewer.

---

## The idea: a render contract, one surface

The payload is a **protocol-neutral render contract**: `data + query +
attestation + provenance + reasoning + a declarative renderHint`. The renderer
dispatches on `renderHint.type` — so adding a type is a new branch, not a new app.

| `renderHint.type` | Drawn as | Data |
|---|---|---|
| `timeseries` | uPlot line chart, threshold bands, legend toggles, hover readout, drag-to-ask | `frames[]` |
| `stat` | Big number + delta + sparkline, threshold coloring | `frames[]` |
| `bar` | Ranked horizontal bars | `frames[]` |
| `table` | Sortable grid | `frames[]` |
| `logs` | Level-filtered log stream | `logs[]` |
| `trace` | Span waterfall with root-cause highlight | `trace` |
| `worklist` | Ranked, actionable findings (alert triage, deprecations, tasks): priority + status + "why" + per-item actions | `worklist[]` |
| `rca` | Root-cause investigation: root cause + confidence, checks, evidence-backed findings | `rca` |
| `timeline` | Change-correlation: deploys + alert transitions on a time axis, correlated event highlighted | `timeline` |
| `cost` | Cost / cardinality drivers with a headroom trade-off | `cost` |
| `rulediff` | Proposed alert-rule fix as a before/after diff, with an **Apply** write action | `rulediff` |

Every cell shares the same **chrome**: title, an attestation line
(`as of … · live/sample · datasource · author`), an optional verdict callout,
drill/refresh/open-in-Grafana **actions**, and a collapsible query & provenance
footer.

The tool result is emitted three ways so it degrades gracefully across hosts:

- `content[0].text` — the human/LLM verdict (what non-App hosts show)
- `structuredContent` — the render contract (what the UI draws)
- `_meta["grafana.insightCell/v0"]` — the trust profile

> **Host note:** some hosts strip `structuredContent`/`_meta` from the tool
> result forwarded to the iframe and keep only text content. To stay robust, the
> cell payload is also embedded as JSON in a text content block, which the app
> scans (`extractCell` in `src/mcp-app.ts`).

---

## Where the insight cell fits vs. panel-reuse

Another approach to rendering Grafana in an MCP app is **panel-reuse**: import a
real Grafana panel component (e.g. `TimeSeriesPanel.tsx`) and drive it with
`PanelProps` + panel JSON through `applyFieldOverrides`. That gives
**pixel-identical** fidelity for free. The two approaches sit on a
**fidelity ↔ reach** frontier and are complementary — not competing.

The decisive structural difference: **panel-reuse can only render things that are
already a Grafana panel.** The highest-value agentic outputs aren't panels — a
ranked alert-triage worklist, a root-cause narrative with evidence, a
change-correlation timeline, a cost/cardinality breakdown, a before/after rule
diff. Those have no panel to reuse; the render contract produces them natively.
Put simply: *panel-reuse ports Grafana's **panels** out of the UI; the insight
cell ports Grafana's **reasoning** out of the UI — and can embed the panels when
fidelity calls for it.*

| Dimension | Panel-reuse | Insight cell (this) |
|---|---|---|
| What it can render | Existing Grafana panel types (each extracted first) | Any `renderHint` — panels **plus** non-panel synthesis (worklist, RCA, timeline, cost, rule-diff) |
| Fidelity | Pixel-identical (real component) | Grafana-styled; values formatted via `@grafana/data`; not pixel-identical |
| Coupling | Grafana panel internals + `@grafana/ui` + React; version-pinned | Render contract + light libs; decoupled from Grafana's release cadence |
| Portability | Runs where the Grafana React stack runs | Protocol-neutral: HTML / PNG / native / **text fallback**; any MCP host |
| Trust/provenance | Added separately | First-class in `_meta` (attestation, provenance, reasoning) |
| Adding a new output | Extract another React panel | One branch on the contract (or wrap a panel for fidelity) |

**The insight cell is better suited when** the output isn't a standard panel
(the synthesis views), needs to travel outside Grafana (any MCP host, degrading
to text), or must be trustworthy/reconcilable/shareable on its own.

**Panel-reuse is better suited when** pixel-fidelity of an existing panel matters
— especially the exotic long tail (heatmaps, geomaps, node graphs, flame graphs),
where re-drawing in a generic renderer isn't worth it.

They compose: as core panels are **externalized** into standalone React
components, the insight cell can consume panel-reuse as a high-fidelity renderer
for specific types — so it's the **layer above** (the contract + trust surface +
non-panel synthesis), able to embed real panels where they help.

---

## Architecture

```
MCP host (e.g. Claude Desktop)  ──stdio──▶  server.ts (MCP server)
                                              ├─ tool: grafana_render(panel, query?, …)
                                              │     → InsightCell + _meta.ui.resourceUri
                                              ├─ tool: apply_alert_rule(uid, rule)   (write)
                                              ├─ resource: ui://…/render-surface.html  (the generic renderer)
                                              └─ src/data.ts → Grafana HTTP API  (or sample data)
```

> The stock Grafana MCP server (`mcp-grafana`) returns data, not UI, so it can't
> render a cell on its own. This server is the thin piece that turns Grafana data
> into a rendered MCP App. It queries Grafana over the HTTP API directly.

| File | Purpose |
|---|---|
| `server.ts` | MCP server: `grafana_render` + `apply_alert_rule` tools, `ui://` resource, `triage-alerts` prompt; stdio/HTTP transports |
| `src/schema.ts` | The render contract (`InsightCell`, `DataFrame`, `RenderHint`, `WorklistItem`, …) |
| `src/data.ts` | Sample generators (all types) + live Grafana queries |
| `src/render.ts` | Generic renderer: shared chrome + one sub-renderer per type |
| `src/format.ts` | Value formatting via `@grafana/data`'s field-config pipeline (correct units/thresholds/mappings) |
| `src/mcp-app.ts` | App-bridge wiring: receive tool result, render, route actions |
| `src/preview.ts` / `preview.html` | Standalone browser gallery (no host needed) |
| `smoketest.mjs` | End-to-end protocol test |

---

## Setup

```bash
npm install
npm run build          # bundles the renderer → dist/mcp-app.html  (re-run after UI edits)
```

Verify without a host:

```bash
npm run smoke                        # protocol: renders every panel type over stdio
npm run build:preview                # then open dist/preview.html in a browser
```

### Register in Claude Desktop

Add this to `claude_desktop_config.json`
(`~/Library/Application Support/Claude/` on macOS), using **absolute paths**
(GUI apps don't inherit your shell `PATH`), then fully quit and reopen the app:

```json
{
  "mcpServers": {
    "grafana-insight-cell": {
      "command": "/absolute/path/to/node",
      "args": [
        "/absolute/path/to/grafana-insight-cell/node_modules/tsx/dist/cli.mjs",
        "/absolute/path/to/grafana-insight-cell/server.ts"
      ]
    }
  }
}
```

### Try it

In a chat:

> Render a timeseries insight cell for request latency
> Show me the top endpoints by errors as a bar chart
> Triage my firing alerts
> Show a change-correlation timeline for the incident

The host calls `grafana_render` with the matching `panel`, and the cell renders
inline. Buttons drill (produce a new cell), refresh (reproduce), apply a change,
or open Grafana.

---

## Go live (real Grafana data)

1. `cp .env.example .env` and fill `GRAFANA_URL`, `GRAFANA_TOKEN`
   (service-account), and the datasource UIDs. The server loads `.env` from the
   project directory (`src/loadenv.ts`), so it works under Claude Desktop with no
   config `env` block.
2. Restart the host, then ask with a query, e.g. *"render a timeseries of
   `node_load1`"* or *"stat of `count(up)`"*.

When live data is used the footer flips to **live** and `attestation.live`
becomes `true`. If a live query fails or returns nothing, the cell degrades to
sample data and says so — never a blank or a silent fake; `meta.dataMode` never
lies.

Live coverage:

| Type | Live source |
|---|---|
| `timeseries`, `stat` | Prometheus range / instant query |
| `bar`, `table` | Prometheus instant-vector |
| `logs` | Loki LogQL `query_range` |
| `worklist` | Grafana Alerting (firing alerts → triage) |
| `timeline` | Grafana annotations + alert-state transitions |
| `cost` | Prometheus cardinality (`topk(count by (__name__))`) |
| `rulediff` | Grafana Alerting provisioning API (read + write) |
| `rca` | agent-supplied (sample otherwise) |
| `trace` | sample only (Tempo not wired yet) |

Synthesis cells (`worklist`, `rca`, `timeline`, `cost`) can also be filled
directly by the agent, which does the correlation and passes the findings.

### The write action

`apply_alert_rule` is a guardrailed write: the `rulediff` cell shows a before/after
diff of a proposed alert-rule change, and **Apply** `PUT`s it via the Alerting
provisioning API. It's read-only until the user clicks; without a rule payload or
credentials it runs in demo mode and writes nothing.

---

## Alternative: a Streamable HTTP transport

```bash
npm run serve:http    # Streamable HTTP on :3001
```

Useful for hosts that connect over HTTP instead of stdio (e.g. via a tunnel as a
custom connector).

---

## Status

This is a prototype for iterating on the render contract and the generic
renderer. It's intentionally standalone (Node/TypeScript) rather than integrated
into `mcp-grafana`'s Go server; porting the embedded bundle into `mcp-grafana`
(the `//go:embed` + `WithUIResource` pattern from #825) is a separate step.
