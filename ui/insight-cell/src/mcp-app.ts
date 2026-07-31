// MCP App UI. Runs in the host's sandboxed iframe. Receives the tool result via
// the App bridge, renders the insight cell (any panel type), and routes actions
// back through the host. The cell is structurally read-only: the only server
// tool it ever calls is render_insight_cell (refresh). Everything else is a
// link (host open) or an "ask" (text handed back to the agent) — writes happen
// agent-side, never from inside the cell.

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
      // The agent responds and typically renders a new cell. Writes (e.g.
      // applying a rulediff) also travel this path: the agent performs them
      // with its own write-gated tools — the cell never calls one.
      await app.sendMessage({ role: "user", content: [{ type: "text", text: a.text }] });
      return;
    }
    if (a.kind !== "refresh") return; // refresh is the only server call the cell can make
    const prev = btn.innerHTML; // icon-only buttons store their glyph as markup, not a text label
    btn.disabled = true;
    btn.textContent = "Working…";
    try {
      const res = await app.callServerTool({ name: "render_insight_cell", arguments: specFrom(cell) });
      // If the result somehow carries no cell, re-enable the control instead
      // of leaving it stuck on "Working…".
      if (!applyResult(res)) restoreButton(btn, prev);
    } catch (err) {
      restoreButton(btn, prev);
      throw err;
    }
  } catch (err) {
    console.error(err);
  }
}

/** Re-render if the result carries a cell. Returns whether it did. */
function applyResult(result: any): boolean {
  const next = extractCell(result);
  if (next) {
    renderInto(root, next, runAction);
    return true;
  }
  return false;
}

/**
 * Re-enable an action button after a call that didn't re-render the cell,
 * restoring its original markup (icon-only buttons carry an SVG glyph that
 * a plain-text label would clobber).
 */
function restoreButton(btn: HTMLButtonElement, prevHTML: string) {
  btn.disabled = false;
  btn.innerHTML = prevHTML;
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
    confidence: cell.meta.confidence,
    query: cell.meta.query[0]?.expr,
    datasourceUid: cell.meta.query[0]?.datasourceUid,
    // A refresh replays the data the cell already holds — it does not re-query.
    // Carry the original attestation stamp so the server doesn't restamp old
    // data as fresh (attestation.asOf / timeRange anchor on dataAsOf).
    dataAsOf: cell.meta.attestation.asOf,
    // The contract stores the resolved timeRange, not the rangeHours input;
    // derive it back so a replay doesn't shrink the window to the 1h default.
    rangeHours: rangeHoursFrom(cell.meta.timeRange),
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
    applied: rd?.applied,
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

/** Whole hours spanned by a recorded time range, for replaying as rangeHours. */
function rangeHoursFrom(tr: { from: string; to: string } | undefined): number | undefined {
  if (!tr) return undefined;
  const ms = Date.parse(tr.to) - Date.parse(tr.from);
  if (!Number.isFinite(ms) || ms <= 0) return undefined;
  return Math.max(1, Math.round(ms / 3_600_000));
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
