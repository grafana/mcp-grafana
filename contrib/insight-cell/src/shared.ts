// Read-only share page — the browser fallback for a share link. The server
// injects the stored cell payload into the /*__CELL_PAYLOAD__*/ script slot in
// shared.html; failing that (e.g. served statically), we fetch the same URL
// with an Accept: application/json header. No MCP host: links open normally,
// every other action explains where to get the interactive version.

import "./styles.css";
import { renderInto } from "./render.js";
import type { InsightCell, InsightCellAction } from "./schema.js";

declare global {
  interface Window { __INSIGHT_CELL__?: unknown }
}

const root = document.getElementById("root")!;

function isCell(obj: unknown): obj is InsightCell {
  return !!obj && typeof obj === "object" && "renderHint" in obj;
}

function readOnlyAction(a: InsightCellAction) {
  if (a.kind === "link" && a.url) {
    window.open(a.url, "_blank", "noopener");
    return;
  }
  if (a.tool === "share_cell") {
    // This page IS the share link.
    navigator.clipboard?.writeText(location.href).catch(() => { /* ignore */ });
    return;
  }
  alert("This is a read-only share page. Paste the link into an MCP Apps host (with the Grafana Insight Cell server connected) to run this action.");
}

async function main() {
  let cell: unknown = window.__INSIGHT_CELL__;
  if (!isCell(cell)) {
    try {
      const res = await fetch(location.pathname, { headers: { accept: "application/json" } });
      cell = (await res.json())?.cell;
    } catch { /* fall through to the error state */ }
  }
  if (!isCell(cell)) {
    root.innerHTML = "";
    const err = document.createElement("div");
    err.className = "empty";
    err.textContent = "Could not load the shared cell — the link may be wrong or the share server is not running.";
    root.append(err);
    return;
  }
  renderInto(root, cell, readOnlyAction);
}

main();
