// Grafana Insight Cell — a generic MCP App render surface.
//
// "OSS Grafana outside the UI": one tool, `grafana_render`, returns an insight
// cell (a ui:// HTML resource the host renders in a sandboxed iframe) that can
// draw any core Grafana panel — timeseries, stat, bar, table — plus logs and
// traces, for any question that comes through the Grafana MCP server. The tool
// result carries the human verdict (content), the renderable render-contract
// payload (structuredContent), and the trust metadata (_meta).
//
// Transports:  default -> stdio (Claude Desktop);  MCP_HTTP=1 -> HTTP :3210
// (override with MCP_HTTP_PORT; share links mint against the same port).
// loadenv must be the FIRST import: it reads .env from the project dir before
// ./src/data.js reads process.env (so live creds work under Claude Desktop).
import "./src/loadenv.js";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import {
  registerAppTool,
  registerAppResource,
  RESOURCE_MIME_TYPE,
} from "@modelcontextprotocol/ext-apps/server";
import { z } from "zod";
import fs from "node:fs/promises";
import path from "node:path";
import { getCell, applyAlertRule, isLiveConfigured, type RenderSpec } from "./src/data.js";
import { saveSharedCell, loadSharedCell, shareUrl, httpPort } from "./src/store.js";
import type { InsightCell } from "./src/schema.js";

const log = (...args: unknown[]) => console.error("[insight-cell]", ...args);

const server = new McpServer({ name: "Grafana Insight Cell", version: "0.1.0" });

const resourceUri = "ui://grafana-insight-cell/render-surface.html";

// Build a tool result from a cell. The render payload rides three channels: a
// text summary (fallback), structuredContent (spec channel), and an embedded
// JSON resource block (Claude Desktop drops structuredContent but keeps this,
// converted to a text block, which the app scans for).
function cellResult(cell: InsightCell, uri: string) {
  const text =
    `${cell.renderHint.title} [${cell.renderHint.type}, ${cell.meta.dataMode} data]\n` +
    `Verdict: ${cell.meta.verdict}\n` +
    (cell.callout ? `${cell.callout.title}: ${cell.callout.body}\n` : "") +
    `As of ${cell.meta.attestation.asOf}${cell.meta.attestation.live ? " · live" : " · sample"}.`;
  return {
    content: [
      { type: "text" as const, text },
      {
        type: "resource" as const,
        resource: { uri: "insightcell://payload.json", mimeType: "application/json", text: JSON.stringify(cell) },
      },
    ],
    structuredContent: cell as unknown as Record<string, unknown>,
    _meta: { ui: { resourceUri: uri } } as Record<string, unknown>,
  };
}

// Layer A trust profile, attached to every result that carries a cell.
function withTrustMeta(result: ReturnType<typeof cellResult>, cell: InsightCell) {
  result._meta["grafana.insightCell/v0"] = {
    query: cell.meta.query,
    attestation: cell.meta.attestation,
    provenance: cell.meta.provenance,
    renderHint: cell.renderHint,
    ...(cell.meta.shared ? { shared: cell.meta.shared } : {}),
    reasoning: {
      question: cell.meta.question,
      verdict: cell.meta.verdict,
      confidence: cell.meta.confidence,
      source: "agent",
    },
  };
  return result;
}

registerAppTool(
  server,
  "grafana_render",
  {
    title: "Render a Grafana insight cell",
    description:
      "Render any Grafana result as an interactive insight cell in the chat: a core panel " +
      "(timeseries, stat, bar, table), a logs view, a trace waterfall, or a 'worklist' — a ranked, " +
      "actionable findings list for alert triage / deprecations / tasks — with a verdict, " +
      "attestation, provenance, and drill actions. Use for any observability question you'd " +
      "answer with a chart. Uses live Grafana Cloud data when configured and a `query` is given " +
      "(timeseries + stat from range/instant queries; bar + table from instant-vector queries; " +
      "logs from a Loki LogQL query_range), otherwise representative sample data. For logs, pass a " +
      "LogQL stream selector as `query` (e.g. '{service_name=\"...\"}'). When you have analyzed the " +
      "data, pass `verdict` " +
      "(a one-line answer) and `insight` (2–4 sentences explaining what the data shows and why " +
      "it matters) so the cell displays your analysis under the title.",
    inputSchema: {
      panel: z
        .enum(["timeseries", "stat", "bar", "table", "logs", "trace", "worklist", "rca", "rulediff", "timeline", "cost", "bullet"])
        .describe("Which panel type to render. 'worklist' = a ranked, actionable findings list (alert triage, deprecations, tasks). 'rca' = a root-cause investigation (findings → root cause → evidence), ideally from Sift / find_slow_requests / find_error_pattern_logs. 'rulediff' = a proposed alert-rule fix shown as a before/after diff with an 'Apply changes' action that writes it via the provisioning API. 'timeline' = a change-correlation timeline (deploys/config/alerts on a time axis, ideally from get_annotations) to decide if an incident lines up with a change vs a resource/anomaly. 'cost' = a cost/cardinality breakdown (what drives active series) that quantifies the 'adding headroom costs money' trade-off. 'bullet' = a compact single value against a target/SLO with qualitative threshold bands (use for one KPI vs a goal; more compact than a gauge)."),
      title: z.string().optional().describe("Panel title / the question being answered."),
      query: z.string().optional().describe("PromQL/LogQL/TraceQL. Used for live timeseries/stat; ignored in sample mode."),
      verdict: z.string().optional().describe("One-line answer shown as the insight title (your conclusion about the data)."),
      insight: z.string().optional().describe("2–4 sentence explanation shown under the title: what the data shows, the trend, and why it matters."),
      service: z.string().optional().describe("Service name to theme sample data around."),
      rangeHours: z.number().optional().describe("Look-back window in hours (default 1)."),
      datasourceUid: z.string().optional().describe("Datasource UID to query (defaults to GRAFANA_PROM_DS_UID)."),
      unit: z.string().optional().describe("Grafana unit id for the values so they format like Grafana: bytes, s, ms, percent, percentunit, reqps, short, Bps, … Drives axis/tooltip/stat formatting. Set this when you know the metric's unit."),
      decimals: z.number().optional().describe("Fixed decimal places; omit for automatic precision."),
      target: z.number().optional().describe("For panel='bullet': the target/SLO marker drawn as a tick."),
      max: z.number().optional().describe("For panel='bullet': axis max; omit to derive from value/target/thresholds."),
      thresholds: z.array(z.object({
        value: z.number(),
        color: z.string().describe("green | orange | red | yellow, or a hex color"),
        label: z.string().optional(),
      })).optional().describe("Threshold steps: a value at/above a step takes its color (colors the stat value and draws a line on timeseries)."),
      mappings: z.array(z.object({
        type: z.enum(["value", "range"]),
        value: z.union([z.number(), z.string()]).optional(),
        from: z.number().optional(),
        to: z.number().optional(),
        text: z.string().optional(),
        color: z.string().optional(),
      })).optional().describe("Value mappings: map a specific value or numeric range to display text/color (e.g. 1 -> 'Critical')."),
      items: z.array(z.object({
        title: z.string(),
        priority: z.enum(["critical", "high", "medium", "low"]).optional(),
        status: z.string().optional().describe("Short state, e.g. 'firing 22m', 'flapping', 'deprecated'."),
        statusTone: z.enum(["ok", "warn", "crit", "neutral"]).optional(),
        why: z.string().optional().describe("Your synthesis/correlation: why it matters and what to do. This is the value — do the reasoning here."),
        actions: z.array(z.object({
          label: z.string(),
          kind: z.enum(["link", "tool", "refresh", "ask"]),
          url: z.string().optional(),
          tool: z.string().optional(),
          args: z.record(z.unknown()).optional(),
          text: z.string().optional(),
          primary: z.boolean().optional(),
        })).optional(),
      })).optional().describe("For panel='worklist': the ranked findings you synthesized (alert triage, deprecations, tasks). Rank by priority and put your correlation in each item's `why`."),
      rootCause: z.object({
        title: z.string(),
        confidence: z.enum(["low", "medium", "high"]),
        detail: z.string().optional(),
      }).optional().describe("For panel='rca': your root-cause hypothesis."),
      checks: z.array(z.string()).optional().describe("For panel='rca': what you examined (error logs, slow requests, recent deploys, …)."),
      findings: z.array(z.object({
        title: z.string(),
        kind: z.string().optional().describe("error pattern | slow request | recent change | resource | correlation"),
        severity: z.enum(["ok", "warn", "crit", "neutral"]).optional(),
        detail: z.string().optional(),
        evidence: z.string().optional().describe("A concrete data point — a log line, metric value, deploy id."),
      })).optional().describe("For panel='rca': ranked findings with evidence, ideally gathered from Sift / find_slow_requests / find_error_pattern_logs / get_annotations."),
      ruleTitle: z.string().optional().describe("For panel='rulediff': the alert rule's name (e.g. 'HighSystemLoad')."),
      ruleUid: z.string().optional().describe("For panel='rulediff': the alert rule UID (from list_alert_rules / provisioning). Required for the Apply action to write the change."),
      ruleSummary: z.string().optional().describe("For panel='rulediff': one line — what the fix does and why."),
      changes: z.array(z.object({
        field: z.string().describe("What's changing, e.g. 'Condition', 'For (grace period)', 'No-data state'."),
        before: z.string(),
        after: z.string(),
        rationale: z.string().optional().describe("One line: why this change."),
      })).optional().describe("For panel='rulediff': the before/after changes you're proposing."),
      proposedRule: z.record(z.unknown()).optional().describe("For panel='rulediff': the full provisioning alert-rule JSON to PUT when applied (get the current rule, edit it, pass it here). Without it, Apply runs in demo mode and writes nothing."),
      events: z.array(z.object({
        time: z.string().describe("ISO timestamp of the change."),
        title: z.string(),
        kind: z.enum(["deploy", "config", "alert", "scale", "incident", "other"]).optional(),
        detail: z.string().optional(),
        tags: z.array(z.string()).optional(),
        correlated: z.boolean().optional().describe("Set true on the change that lines up with the incident — the smoking gun."),
      })).optional().describe("For panel='timeline': change events (deploys/config/alerts) to correlate against an incident, ideally from get_annotations. Mark the correlated one. If omitted, the live path auto-merges annotations + firing alerts' start times and flags any tight alert cluster (≥2 within 3 min) as correlated."),
      drivers: z.array(z.object({
        name: z.string(),
        series: z.number().optional().describe("Active series (cardinality)."),
        value: z.number().optional(),
        unit: z.string().optional(),
        pct: z.number().optional(),
        note: z.string().optional().describe("Why it's costly, e.g. 'high-churn label: pod name'."),
      })).optional().describe("For panel='cost': ranked cost/cardinality drivers. Omit to run a live topk cardinality query."),
      costTotal: z.object({ label: z.string(), value: z.number(), unit: z.string().optional() }).optional().describe("For panel='cost': the headline total, e.g. {label:'1.2M active series', value:1200000}."),
      headroom: z.object({ label: z.string(), detail: z.string() }).optional().describe("For panel='cost': the headroom trade-off — make 'adding headroom costs money' explicit (extra series/$ from scaling vs fixing the rule for free)."),
    },
    _meta: { ui: { resourceUri } },
  },
  async (args) => {
    const spec = args as RenderSpec;
    const cell = await getCell(spec);
    log(`grafana_render(${spec.panel}) -> ${cell.meta.dataMode}`);

    return withTrustMeta(cellResult(cell, resourceUri), cell);
  },
);

// Share a cell: persist the full payload and mint a link. Invoked by the share
// icon on the cell chrome (the iframe passes the current cell), or directly by
// the agent with a cell it just rendered.
registerAppTool(
  server,
  "share_cell",
  {
    title: "Share an insight cell",
    description:
      "Persist an insight cell and return a share link another user can open. Their agent opens it " +
      "with open_shared_cell (rendered live in their MCP Apps host); in a plain browser the same " +
      "link is a read-only page. Called by the share button on a rendered cell, which passes the " +
      "current cell payload; can also be called directly with the structuredContent of a previous " +
      "grafana_render result.",
    inputSchema: {
      cell: z.record(z.unknown()).optional().describe("The full insight-cell payload to share (the share button passes it automatically)."),
    },
    _meta: { ui: { resourceUri } },
  },
  async (args) => {
    const cell = args.cell as InsightCell | undefined;
    if (!cell?.renderHint || !cell.meta) {
      return { content: [{ type: "text" as const, text: "share_cell needs the full cell payload — use the share button on a rendered cell, or pass the structuredContent of a grafana_render result as `cell`." }] };
    }
    // Re-sharing a shared cell should mint a clean record, not nest share chrome.
    delete cell.meta.shared;
    if (cell.callout?.title.startsWith("Shared")) delete cell.callout;
    cell.actions = cell.actions.filter((a) => a.label !== "Open share page");

    const rec = saveSharedCell(cell, process.env.USER);
    const url = shareUrl(rec.id);
    log(`share_cell -> ${url}`);

    const out: InsightCell = {
      ...cell,
      callout: {
        tone: "info",
        title: "Shared — link ready",
        body: `${url} — send it to a teammate. Pasted into an MCP Apps host with this server connected, their agent renders it live; in a plain browser it opens as a read-only page.`,
      },
      actions: [...cell.actions, { label: "Open share page", kind: "link", icon: "external", url }],
      meta: { ...cell.meta, shared: { id: rec.id, at: rec.at, by: rec.by, url } },
    };
    const result = withTrustMeta(cellResult(out, resourceUri), out);
    result.content[0] = { type: "text" as const, text: `Shared: ${url}\nGive this link to the recipient (Slack, etc.). ${result.content[0].type === "text" ? (result.content[0] as { text: string }).text : ""}` };
    return result;
  },
);

// Open a cell someone shared: load the snapshot and re-emit it through the same
// three-channel result, so the recipient's host renders it like a fresh cell.
registerAppTool(
  server,
  "open_shared_cell",
  {
    title: "Open a shared insight cell",
    description:
      "Open an insight cell another user shared. Call this whenever the user pastes a share link " +
      "(http://…/cells/<id> or insightcell://cells/<id>) or asks to open a shared cell; pass the " +
      "link or bare id. Renders the snapshot exactly as the sender saw it, with the share " +
      "provenance attached; the cell's refresh action re-runs its query with the current user's " +
      "credentials, so datasource permissions still apply on re-materialize.",
    inputSchema: {
      link: z.string().describe("The share link or bare cell id."),
    },
    _meta: { ui: { resourceUri } },
  },
  async ({ link }) => {
    const rec = loadSharedCell(link);
    if (!rec) {
      return { content: [{ type: "text" as const, text: `No shared cell found for "${link}". The id may be wrong, or the cell was shared into a different store (this prototype's store is local: ~/.grafana-insight-cell/cells).` }] };
    }
    log(`open_shared_cell(${rec.id}) <- shared by ${rec.by ?? "unknown"} at ${rec.at}`);
    const cell: InsightCell = {
      ...rec.cell,
      meta: { ...rec.cell.meta, shared: { id: rec.id, at: rec.at, by: rec.by, url: shareUrl(rec.id) } },
    };
    const result = withTrustMeta(cellResult(cell, resourceUri), cell);
    result.content[0] = {
      type: "text" as const,
      text:
        `Shared insight cell from ${rec.by ?? "unknown"}, shared ${rec.at}. Snapshot data as the sender saw it ` +
        `(as of ${cell.meta.attestation.asOf}); the refresh action re-runs the query with your credentials.\n` +
        (result.content[0].type === "text" ? (result.content[0] as { text: string }).text : ""),
    };
    return result;
  },
);

// Apply a proposed alert-rule fix: PUT it via the provisioning API and re-render
// the diff as "applied". Invoked by the "Apply changes" action on a rulediff cell.
registerAppTool(
  server,
  "apply_alert_rule",
  {
    title: "Apply an alert-rule change",
    description:
      "Write a proposed alert-rule change to Grafana via the provisioning API " +
      "(PUT /api/v1/provisioning/alert-rules/{uid}) and re-render the rule diff as applied. " +
      "Called by the 'Apply changes' action on a rulediff insight cell; can also be called " +
      "directly with a rule UID and the full updated rule payload.",
    inputSchema: {
      uid: z.string().optional().describe("Alert rule UID to update."),
      rule: z.record(z.unknown()).optional().describe("Full provisioning alert-rule payload to PUT. Omit for demo mode (nothing is written)."),
      ruleTitle: z.string().optional(),
      summary: z.string().optional(),
      changes: z.array(z.object({
        field: z.string(),
        before: z.string(),
        after: z.string(),
        rationale: z.string().optional(),
      })).optional(),
    },
    _meta: { ui: { resourceUri } },
  },
  async (args) => {
    const a = args as Parameters<typeof applyAlertRule>[0];
    const cell = await applyAlertRule(a);
    log(`apply_alert_rule(${a.uid ?? "-"}) -> applied=${cell.rulediff?.applied} mode=${cell.meta.dataMode}`);
    return cellResult(cell, resourceUri);
  },
);

registerAppResource(
  server,
  resourceUri,
  resourceUri,
  { mimeType: RESOURCE_MIME_TYPE },
  async () => {
    const html = await fs.readFile(path.join(import.meta.dirname, "dist", "mcp-app.html"), "utf-8");
    return { contents: [{ uri: resourceUri, mimeType: RESOURCE_MIME_TYPE, text: html }] };
  },
);

// A skill/prompt (IC-7) that encodes the triage flow: read → correlate → render.
// Surfaced by the host (e.g. Claude Desktop) as an invocable command.
server.registerPrompt(
  "triage-alerts",
  {
    title: "Triage alerts",
    description:
      "Read the firing alerts, correlate them (flapping vs anomaly, resource vs transient), and render a prioritized, actionable triage worklist with your reasoning.",
    argsSchema: { focus: z.string().optional().describe("Optional service or area to focus on.") },
  },
  ({ focus }) => ({
    messages: [
      {
        role: "user",
        content: {
          type: "text",
          text:
            "You are triaging on-call alerts for the start of a shift. Produce a prioritized, actionable worklist — not a dump of metrics.\n\n" +
            "1. Get the current firing alerts: call the `grafana_render` tool with { \"panel\": \"worklist\" } and read the returned items (alert name, severity, status, summary)." +
            (focus ? ` Focus on: ${focus}.` : "") + "\n" +
            "2. For each alert, correlate and decide — don't just restate it:\n" +
            "   - Is it FLAPPING (oscillating in and out of firing)? Inspect the underlying series over time (render a timeseries of its metric, or check alert state history). If it flaps around a threshold AND the resource is near its limit, it's a RESOURCE issue → recommend adding headroom, not silencing. If it's a one-off spike that self-recovered, it's likely an ANOMALY → no action / silence.\n" +
            "   - Is it SUSTAINED and worsening (e.g. error rate climbing, correlated with a dependency)? That needs action now → critical.\n" +
            "   - Correlate related alerts (a service's errors + an upstream latency alert are usually one incident).\n" +
            "3. Assign each a priority: critical (act now) / high / medium / low (no action).\n" +
            "4. Write a one–two sentence `why` for each: what it means and the recommended next step.\n" +
            "5. Render the finished triage by calling `grafana_render` again with:\n" +
            "   - panel: \"worklist\"\n" +
            "   - title: \"On-call triage\"\n" +
            "   - verdict: one line, e.g. \"1 needs action now, 1 flapping (add headroom), rest can wait\"\n" +
            "   - items: [{ title, priority, status (e.g. \"flapping\" or \"firing 22m\"), statusTone, why, actions }] sorted by priority, with your synthesis in `why`.\n\n" +
            "Keep it to what the on-call engineer should do first. Prefer correlation and recommended actions over raw numbers.",
        },
      },
    ],
  }),
);

async function main() {
  log(`data mode: ${isLiveConfigured() ? "LIVE (Grafana Cloud) for timeseries/stat" : "MOCK (sample data)"}`);
  if (process.env.MCP_HTTP === "1") {
    const { default: express } = await import("express");
    const { default: cors } = await import("cors");
    const app = express();
    app.use(cors());
    app.use(express.json());
    app.post("/mcp", async (req, res) => {
      const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true });
      res.on("close", () => transport.close());
      await server.connect(transport);
      await transport.handleRequest(req, res, req.body);
    });
    // Read-only share page: the same link a recipient's agent opens via
    // open_shared_cell renders in a plain browser too (graceful degradation).
    app.get("/cells/:id", async (req, res) => {
      const rec = loadSharedCell(req.params.id);
      if (!rec) { res.status(404).type("text").send("Unknown or expired share id."); return; }
      if (req.query.json != null || (req.headers.accept ?? "").includes("application/json")) { res.json(rec); return; }
      let html: string;
      try {
        html = await fs.readFile(path.join(import.meta.dirname, "dist", "shared.html"), "utf-8");
      } catch {
        res.status(500).type("text").send("shared.html not built — run `npm run build`.");
        return;
      }
      const cell = { ...rec.cell, meta: { ...rec.cell.meta, shared: { id: rec.id, at: rec.at, by: rec.by } } };
      const payload = JSON.stringify(cell).replace(/</g, "\\u003c");
      res.type("html").send(html.replace("/*__CELL_PAYLOAD__*/", `window.__INSIGHT_CELL__ = ${payload};`));
    });
    const port = httpPort();
    app.listen(port, () => log(`HTTP transport on http://localhost:${port}/mcp · share pages on http://localhost:${port}/cells/<id>`));
  } else {
    const transport = new StdioServerTransport();
    await server.connect(transport);
    log("stdio transport ready");
  }
}

main().catch((err) => { log("fatal:", err); process.exit(1); });
