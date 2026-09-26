// Shared-cell store — the persistence + addressing layer behind share_cell /
// open_shared_cell (server-side only; uses node:fs).
//
// A shared cell is the full InsightCell JSON written to
// ~/.grafana-insight-cell/cells/<id>.json (override with INSIGHT_CELL_STORE).
// Because the cell is a self-contained render contract, "persist" is just the
// payload plus who/when — no extra modeling. The snapshot carries the data the
// sender saw; the recipient's Refresh action re-runs meta.query with THEIR
// credentials (recipe mode), so datasource RBAC is enforced on re-materialize.

import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import crypto from "node:crypto";
import type { InsightCell } from "./schema.js";

export interface SharedCellRecord {
  id: string;
  at: string; // ISO timestamp of the share
  by?: string;
  cell: InsightCell;
}

function storeDir(): string {
  return process.env.INSIGHT_CELL_STORE ?? path.join(os.homedir(), ".grafana-insight-cell", "cells");
}

/** Port the HTTP transport / share pages listen on (MCP_HTTP mode).
 * Default 3210 — NOT 3001, which the local Grafana docker stack tends to own. */
export function httpPort(): number {
  return Number(process.env.MCP_HTTP_PORT ?? 3210);
}

/** Base URL share links are minted under — the HTTP server that serves /cells/<id>. */
export function shareBaseUrl(): string {
  return (process.env.INSIGHT_CELL_SHARE_URL ?? `http://localhost:${httpPort()}`).replace(/\/+$/, "");
}

export function shareUrl(id: string): string {
  return `${shareBaseUrl()}/cells/${id}`;
}

/** Accepts a bare id, a share URL (…/cells/<id>), or insightcell://cells/<id>. */
export function parseShareId(input: string): string | null {
  const m = input.trim().replace(/[?#].*$/, "").match(/([a-f0-9]{6,32})\/?$/i);
  return m ? m[1].toLowerCase() : null;
}

export function saveSharedCell(cell: InsightCell, by?: string): SharedCellRecord {
  const dir = storeDir();
  fs.mkdirSync(dir, { recursive: true });
  const rec: SharedCellRecord = {
    id: crypto.randomBytes(5).toString("hex"),
    at: new Date().toISOString(),
    by,
    cell,
  };
  fs.writeFileSync(path.join(dir, `${rec.id}.json`), JSON.stringify(rec, null, 2));
  return rec;
}

export function loadSharedCell(idOrLink: string): SharedCellRecord | null {
  const id = parseShareId(idOrLink);
  if (!id) return null;
  try {
    const raw = fs.readFileSync(path.join(storeDir(), `${id}.json`), "utf-8");
    const rec = JSON.parse(raw) as SharedCellRecord;
    return rec?.cell?.renderHint ? rec : null;
  } catch {
    return null;
  }
}
