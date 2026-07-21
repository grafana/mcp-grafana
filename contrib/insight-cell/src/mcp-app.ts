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
      const res = await app.callServerTool({ name: "grafana_render", arguments: specFrom(cell) });
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

/** Reconstruct the render spec from a cell so refresh reproduces it. */
function specFrom(cell: InsightCell): Record<string, unknown> {
  const from = new Date(cell.meta.timeRange.from).getTime();
  const to = new Date(cell.meta.timeRange.to).getTime();
  const rangeHours = Math.max(1, Math.round((to - from) / 3600_000));
  return {
    panel: cell.renderHint.type,
    title: cell.renderHint.title,
    query: cell.meta.query[0]?.expr,
    datasourceUid: cell.meta.query[0]?.datasourceUid,
    rangeHours,
  };
}

async function openLink(url: string) {
  const a = app as any;
  if (typeof a.openLink === "function") return a.openLink({ url });
  if (typeof a.sendOpenLink === "function") return a.sendOpenLink({ url });
  if (typeof a.openExternal === "function") return a.openExternal({ url });
  window.open(url, "_blank", "noopener");
}
