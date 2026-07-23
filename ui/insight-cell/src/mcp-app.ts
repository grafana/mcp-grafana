// MCP App UI. Runs in the host's sandboxed iframe. Receives the tool result via
// the App bridge, renders the insight cell (any panel type), and routes actions
// (refresh / drill / open link) back through the host.

import { App } from "@modelcontextprotocol/ext-apps";
import "./styles.css";
import { renderInto } from "./render.js";
import type { InsightCell, InsightCellAction } from "./schema.js";

const root = document.getElementById("root")!;

const app = new App({ name: "Grafana Insight Cell", version: "0.1.0" });
app.connect();

app.ontoolresult = (result: any) => {
  const cell = extractCell(result);
  if (!cell) {
    root.innerHTML = "";
    const diag = document.createElement("pre");
    diag.style.cssText = "font-size:11px;white-space:pre-wrap;word-break:break-all;padding:12px;color:var(--muted)";
    const contentTypes = Array.isArray(result?.content) ? result.content.map((c: any) => c?.type).join(", ") : "(no content array)";
    diag.textContent =
      "No cell data — diagnostic of what the iframe received:\n" +
      `top-level keys: ${result ? Object.keys(result).join(", ") : "(null)"}\n` +
      `content types: ${contentTypes}\n` +
      `has structuredContent: ${!!result?.structuredContent}\n` +
      `_meta keys: ${result?._meta ? Object.keys(result._meta).join(", ") : "(none)"}\n\n` +
      `first 600 chars:\n${JSON.stringify(result).slice(0, 600)}`;
    root.append(diag);
    return;
  }
  renderInto(root, cell, runAction);
};

/**
 * Read the render payload from a tool result. Prefers structuredContent (the
 * spec channel), falls back to the embedded JSON resource block in content
 * (which hosts forward reliably even when they drop structuredContent).
 */
function extractCell(result: any): InsightCell | undefined {
  // 1. structuredContent — the spec channel (dropped by some hosts).
  const sc = result?.structuredContent;
  if (sc && typeof sc === "object" && Object.keys(sc).length) return sc as InsightCell;

  // 2. embedded resource block (hosts that keep resource content).
  const res = result?.content?.find(
    (c: any) => c?.type === "resource" && c?.resource?.mimeType === "application/json" && typeof c.resource.text === "string",
  );
  if (res) { try { return asCell(JSON.parse(res.resource.text)); } catch { /* fall through */ } }

  // 3. a JSON payload inside a text block — Claude Desktop converts the resource
  //    block to plain text, so the JSON arrives here.
  for (const c of result?.content ?? []) {
    if (c?.type !== "text" || typeof c.text !== "string") continue;
    const s = c.text.trim();
    if (!s.startsWith("{")) continue;
    try { const cell = asCell(JSON.parse(s)); if (cell) return cell; } catch { /* not our payload */ }
  }
  return undefined;
}

function asCell(obj: any): InsightCell | undefined {
  return obj && typeof obj === "object" && obj.renderHint ? (obj as InsightCell) : undefined;
}

async function runAction(a: InsightCellAction, cell: InsightCell, btn: HTMLButtonElement) {
  try {
    if (a.kind === "link" && a.url) {
      await openLink(a.url);
      return;
    }
    if (a.kind === "ask" && a.text) {
      // Select-and-ask: hand the selection to the agent as the next question.
      // The agent responds and typically renders a new cell.
      await app.sendMessage({ role: "user", content: [{ type: "text", text: a.text }] });
      return;
    }
    btn.disabled = true;
    btn.textContent = "Working…";
    if (a.kind === "refresh") {
      const res = await app.callServerTool({ name: "render_insight_cell", arguments: specFrom(cell) });
      applyResult(res);
    } else if (a.kind === "tool" && a.tool) {
      const res = await app.callServerTool({ name: a.tool, arguments: a.args ?? {} });
      applyResult(res);
    }
  } catch (err) {
    btn.disabled = false;
    btn.textContent = "Failed — retry";
    console.error(err);
  }
}

function applyResult(result: any) {
  const next = extractCell(result);
  if (next) renderInto(root, next, runAction);
}

/**
 * Reconstruct the render_insight_cell arguments from a cell so a refresh
 * reproduces it. render_insight_cell is a render substrate — it repackages the
 * data it's given rather than re-querying — so we pass the full payload back,
 * not just panel/title/query (which would redraw an empty cell).
 */
function specFrom(cell: InsightCell): Record<string, unknown> {
  const rh = cell.renderHint;
  const rd = cell.rulediff;
  const args: Record<string, unknown> = {
    panel: rh.type,
    title: rh.title,
    verdict: cell.meta.verdict,
    insight: rh.description,
    query: cell.meta.query[0]?.expr,
    datasourceUid: cell.meta.query[0]?.datasourceUid,
    unit: rh.unit,
    decimals: rh.decimals,
    thresholds: rh.thresholds,
    mappings: rh.mappings,
    valueField: rh.valueField,
    sort: rh.sort,
    target: rh.target,
    max: rh.max,
    frames: cell.frames,
    logs: cell.logs,
    trace: cell.trace,
    items: cell.worklist,
    rootCause: cell.rca?.rootCause,
    checks: cell.rca?.checks,
    findings: cell.rca?.findings,
    ruleTitle: rd?.ruleTitle,
    ruleUid: rd?.ruleUid,
    ruleSummary: rd?.summary,
    changes: rd?.changes,
    proposedRule: rd?.proposed,
    events: cell.timeline?.events,
    from: cell.timeline?.from,
    to: cell.timeline?.to,
    drivers: cell.cost?.drivers,
    costTotal: cell.cost?.total,
    headroom: cell.cost?.headroom,
    callout: cell.callout,
    actions: cell.actions,
  };
  // Drop undefined so we don't send a wall of null args.
  for (const k of Object.keys(args)) if (args[k] === undefined) delete args[k];
  return args;
}

/** Reject non-http(s) schemes (e.g. javascript:, data:) before linking. */
function safeHttpUrl(raw: string): string | null {
  try {
    const parsed = new URL(raw);
    if (parsed.protocol === "http:" || parsed.protocol === "https:") return parsed.toString();
  } catch {
    // Not a parseable absolute URL.
  }
  return null;
}

async function openLink(url: string) {
  const safe = safeHttpUrl(url);
  if (!safe) {
    console.error("[insight-cell] refusing to open non-http(s) url:", url);
    return;
  }
  const a = app as any;
  if (typeof a.openLink === "function") return a.openLink({ url: safe });
  if (typeof a.sendOpenLink === "function") return a.sendOpenLink({ url: safe });
  if (typeof a.openExternal === "function") return a.openExternal({ url: safe });
  window.open(safe, "_blank", "noopener");
}
