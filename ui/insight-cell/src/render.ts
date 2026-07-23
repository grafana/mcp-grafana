// Generic render surface. renderInto() draws the insight-cell information
// architecture and dispatches the panel body on renderHint.type.
//
// Layout (top → bottom):
//   header        — title + attestation (left) · Share dropdown + Grafana logo (right)
//   viz section   — visualization + hover toolbar (change type / zoom / fullscreen)
//                   + legend, inside a bordered box
//   insight       — the agent's title + explanation
//   follow-ups    — suggested agent actions (left) · open in Grafana (right)
//   divider
//   query & prov  — collapsible; view / edit / copy the query + provenance

import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import grafanaLogo from "./grafana_icon.svg?raw";
import { formatValue, displayValue, resolveColor } from "./format.js";
import type {
  DataFrame,
  InsightCell,
  InsightCellAction,
  LogLine,
  PanelType,
  TracePayload,
  Threshold,
} from "./schema.js";

export type ActionHandler = (a: InsightCellAction, cell: InsightCell, btn: HTMLButtonElement) => void;

// Grafana classic series palette (matches grafana/mcp-grafana PR #825).
const PALETTE = [
  "#7EB26D", "#EAB839", "#6ED0E0", "#EF843C", "#E24D42",
  "#1F78C1", "#BA43A9", "#705DA0", "#508642", "#CCA300",
  "#447EBC", "#C15C17", "#890F02", "#0A437C", "#6D1F62",
];

// Grafana grid/axis theme colors, by host color scheme (matches PR #825).
function grafanaChartTheme() {
  const dark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  return { grid: dark ? "#2c2f3e" : "#e4e7e7", axis: dark ? "#8e8fa1" : "#585959" };
}
const GRAFANA_AXIS_FONT = "11px Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif";

interface PanelToolbar {
  /** Panel types this frame data can also be rendered as. */
  changeTypes?: PanelType[];
  /** Reset zoom to the full range (timeseries). */
  resetZoom?: () => void;
  /** Zoom x-scale into the current drag selection (timeseries). */
  zoomToSelection?: () => void;
  /** Grow/shrink the panel for fullscreen. */
  setFullscreen?: (fs: boolean) => void;
}

/** Context passed to panels so they can route interactions back to the agent. */
interface RenderCtx {
  onAction: ActionHandler;
  cell: InsightCell;
}

interface Panel {
  node: HTMLElement;      // the visualization body
  legend?: HTMLElement;   // optional legend, shown below the viz inside the box
  mount?: () => void;     // run after node is in the DOM (measurement)
  toolbar?: PanelToolbar;
}

export function renderInto(root: HTMLElement, cell: InsightCell, onAction: ActionHandler) {
  root.innerHTML = "";
  const rerender = (next: InsightCell) => renderInto(root, next, onAction);

  const viz = vizSection(cell, rerender, { onAction, cell });
  const fu = followUpRow(cell, onAction);
  root.append(
    headerRow(cell),
    viz.section,
    insightSection(cell),
    ...(fu ? [fu] : []),
    el("hr", "divider"),
    queryProvenance(cell, onAction),
  );
  viz.mount?.();
}

// --- small helpers -----------------------------------------------------------

function el(tag: string, cls?: string, text?: string): HTMLElement {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
}

function svg(markup: string, cls?: string): HTMLElement {
  const span = el("span", cls);
  span.innerHTML = markup;
  return span;
}

/** Minimal inline icons (stroke = currentColor). */
const IC = {
  share: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><line x1="8.6" y1="13.5" x2="15.4" y2="17.5"/><line x1="15.4" y1="6.5" x2="8.6" y2="10.5"/></svg>`,
  chevron: `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"/></svg>`,
  barType: `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="6" y1="20" x2="6" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/><line x1="18" y1="20" x2="18" y2="14"/></svg>`,
  zoom: `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7"/><line x1="21" y1="21" x2="16" y2="16"/></svg>`,
  fullscreen: `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 3 21 3 21 9"/><polyline points="9 21 3 21 3 15"/><line x1="21" y1="3" x2="14" y2="10"/><line x1="3" y1="21" x2="10" y2="14"/></svg>`,
  external: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="5" width="14" height="14" rx="2"/><path d="M14 4h6v6M20 4l-8 8"/></svg>`,
  copy: `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/></svg>`,
  trend: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 17 9 11 13 15 21 6"/><polyline points="15 6 21 6 21 12"/></svg>`,
  refresh: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.3"/><polyline points="21 3 21 9 15 9"/></svg>`,
};

/**
 * Render an action. Labelled soft-blue pill by default (like Share); icon-only
 * for links (open in Grafana) and trend/view actions.
 */
function isIconAction(a: InsightCellAction): boolean {
  return a.kind === "link" || !!a.icon || /trend/i.test(a.label);
}

function actionButton(a: InsightCellAction, cell: InsightCell, onAction: ActionHandler): HTMLButtonElement {
  const iconOnly = isIconAction(a);
  const btn = el("button", iconOnly ? "act-icon" : "act-soft") as HTMLButtonElement;
  if (iconOnly) {
    const ic = a.icon === "trend" || /trend/i.test(a.label) ? IC.trend : IC.external;
    btn.innerHTML = ic;
    btn.title = a.label;
    btn.setAttribute("aria-label", a.label);
  } else {
    btn.textContent = a.label;
  }
  btn.addEventListener("click", () => onAction(a, cell, btn));
  return btn;
}

function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || "#888";
}

function toast(anchor: HTMLElement, msg: string) {
  const card = anchor.closest(".card") ?? anchor;
  const t = el("div", "toast", msg);
  card.append(t);
  setTimeout(() => t.classList.add("show"), 10);
  setTimeout(() => { t.classList.remove("show"); setTimeout(() => t.remove(), 250); }, 2200);
}

/** A click-to-open dropdown. `items` render as a menu below the trigger. */
function dropdown(trigger: HTMLElement, items: Array<{ label: string; onClick: () => void }>): HTMLElement {
  const wrap = el("div", "dropdown");
  const menu = el("div", "menu");
  for (const it of items) {
    const mi = el("button", "menu-item", it.label);
    mi.addEventListener("click", (e) => { e.stopPropagation(); menu.classList.remove("open"); it.onClick(); });
    menu.append(mi);
  }
  trigger.addEventListener("click", (e) => {
    e.stopPropagation();
    const open = menu.classList.toggle("open");
    if (open) {
      const close = () => { menu.classList.remove("open"); document.removeEventListener("click", close); };
      setTimeout(() => document.addEventListener("click", close), 0);
    }
  });
  wrap.append(trigger, menu);
  return wrap;
}

// --- header ------------------------------------------------------------------

function headerRow(cell: InsightCell): HTMLElement {
  const head = el("div", "head");

  const left = el("div", "head-left");
  left.append(el("div", "title", cell.renderHint.title));
  const m = cell.meta;
  const sub = el("div", "attest");
  const ts = el("span", "mono", new Date(m.attestation.asOf).toLocaleString());
  sub.append(ts, document.createTextNode(` · ${m.provenance.datasource} · ${m.provenance.author}`));
  left.append(sub);
  head.append(left);

  const right = el("div", "head-right");
  const shareBtn = el("button", "share-btn");
  shareBtn.append(svg(IC.share, "ic"), document.createTextNode("Share"), svg(IC.chevron, "ic"));
  const share = dropdown(shareBtn, [
    { label: "Share with a user", onClick: () => toast(head, "Sharing with a user isn’t wired in this prototype") },
    { label: "Copy link", onClick: () => copyText(head, cellLink(cell), "Link copied") },
    { label: "Download", onClick: () => downloadJson(head, cell) },
    { label: "Add to Grafana notebook", onClick: () => toast(head, "Add to notebook isn’t wired in this prototype") },
  ]);
  right.append(share);

  const logo = svg(grafanaLogo, "logo");
  right.append(logo);
  head.append(right);
  return head;
}

function cellLink(cell: InsightCell): string {
  const link = cell.actions.find((a) => a.kind === "link");
  return link?.url ?? "https://grafana.net/insight-cell/(prototype)";
}

async function copyText(anchor: HTMLElement, text: string, ok: string) {
  try { await navigator.clipboard.writeText(text); toast(anchor, ok); }
  catch { toast(anchor, "Clipboard blocked by the host sandbox"); }
}

function downloadJson(anchor: HTMLElement, cell: InsightCell) {
  try {
    const blob = new Blob([JSON.stringify(cell, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `insight-cell-${cell.renderHint.type}.json`;
    a.click();
    URL.revokeObjectURL(url);
    toast(anchor, "Downloaded cell JSON");
  } catch { toast(anchor, "Download blocked by the host sandbox"); }
}

// --- visualization section ---------------------------------------------------

function vizSection(cell: InsightCell, rerender: (next: InsightCell) => void, ctx: RenderCtx): { section: HTMLElement; mount?: () => void } {
  const box = el("div", "viz-box");
  const panel = renderPanel(cell, ctx);

  const body = el("div", "viz-body");
  body.append(panel.node);
  box.append(body);

  box.append(vizToolbar(cell, panel, rerender, box));

  if (panel.legend) {
    const lg = el("div", "viz-legend");
    lg.append(panel.legend);
    box.append(lg);
  }
  return { section: box, mount: panel.mount };
}

function vizToolbar(
  cell: InsightCell,
  panel: Panel,
  rerender: (next: InsightCell) => void,
  box: HTMLElement,
): HTMLElement {
  const t = panel.toolbar ?? {};
  const bar = el("div", "viz-toolbar");

  // Change visualization type (frame-based panels only).
  if (t.changeTypes?.length) {
    const trigger = el("button", "vt-btn");
    trigger.title = "Change visualization type";
    trigger.append(svg(IC.barType, "ic"), svg(IC.chevron, "ic"));
    const items = t.changeTypes.map((type) => ({
      label: labelFor(type),
      onClick: () => rerender({ ...cell, renderHint: { ...cell.renderHint, type } }),
    }));
    bar.append(dropdown(trigger, items));
  }

  // Zoom (timeseries). Handlers are wired during mount(), so call them lazily.
  if (cell.renderHint.type === "timeseries") {
    const trigger = el("button", "vt-btn");
    trigger.title = "Zoom";
    trigger.append(svg(IC.zoom, "ic"), svg(IC.chevron, "ic"));
    bar.append(dropdown(trigger, [
      { label: "Zoom to selection", onClick: () => panel.toolbar?.zoomToSelection?.() },
      { label: "Reset zoom", onClick: () => panel.toolbar?.resetZoom?.() },
    ]));
  }

  // Fullscreen — always available. Widens the card (CSS); panels that need to
  // re-measure (uPlot) hook setFullscreen.
  const fs = el("button", "vt-btn");
  fs.title = "Fullscreen";
  fs.append(svg(IC.fullscreen, "ic"));
  fs.addEventListener("click", () => {
    const card = box.closest(".card") ?? box;
    const on = card.classList.toggle("fs");
    panel.toolbar?.setFullscreen?.(on);
  });
  bar.append(fs);

  return bar;
}

function labelFor(t: PanelType): string {
  return { timeseries: "Time series", stat: "Stat", bar: "Bar chart", table: "Table", logs: "Logs", trace: "Trace", worklist: "Worklist", rca: "Investigation", rulediff: "Rule fix", timeline: "Timeline", cost: "Cost", bullet: "Bullet" }[t];
}

// --- insight (agent explanation) ---------------------------------------------

function insightSection(cell: InsightCell): HTMLElement {
  const c = cell.callout;
  const tone = c?.tone === "crit" ? "crit" : c?.tone === "info" ? "info" : c ? "warn" : "info";
  const wrap = el("div", `insight ${tone}`);
  wrap.append(el("div", "ico", tone === "info" ? "ⓘ" : "⚠"));
  const body = el("div", "insight-body");
  body.append(el("div", "insight-title", c?.title ?? cell.meta.verdict));
  if (c?.body) body.append(el("div", "insight-text", c.body));
  wrap.append(body);
  return wrap;
}

// --- follow-up actions -------------------------------------------------------

function followUpRow(cell: InsightCell, onAction: ActionHandler): HTMLElement | null {
  const acts = cell.actions.filter((a) => a.kind === "tool" || a.kind === "ask");
  const link = cell.actions.find((a) => a.kind === "link");
  if (!acts.length && !link) return null; // refresh moved to the query & provenance line

  const row = el("div", "followups");
  const left = el("div", "fu-left");
  for (const a of acts) left.append(actionButton(a, cell, onAction));
  row.append(left);
  if (link) row.append(actionButton(link, cell, onAction)); // icon button, pushed right
  return row;
}

// --- query & provenance (collapsible) ----------------------------------------

function queryProvenance(cell: InsightCell, onAction: ActionHandler): HTMLElement {
  const wrap = el("div", "qp");
  const head = el("div", "qp-head");
  const toggle = el("button", "qp-toggle");
  toggle.append(svg(IC.chevron, "ic chevron"), document.createTextNode("Query and provenance"));
  head.append(toggle);
  const refresh = cell.actions.find((a) => a.kind === "refresh");
  if (refresh) {
    const rb = el("button", "qp-refresh") as HTMLButtonElement;
    rb.innerHTML = IC.refresh;
    rb.title = refresh.label || "Refresh";
    rb.setAttribute("aria-label", "Refresh");
    rb.addEventListener("click", (e) => { e.stopPropagation(); onAction(refresh, cell, rb); });
    head.append(rb);
  }
  wrap.append(head);
  const body = el("div", "qp-body");

  for (const q of cell.meta.query) {
    const block = el("div", "qp-query");
    const head = el("div", "qp-query-head");
    head.append(el("span", "qp-ref", `${q.ref} · ${q.datasourceUid}`));
    const copy = el("button", "qp-copy") as HTMLButtonElement;
    copy.append(svg(IC.copy, "ic"), document.createTextNode("Copy"));
    head.append(copy);
    block.append(head);

    const ta = document.createElement("textarea");
    ta.className = "qp-expr mono";
    ta.value = q.expr;
    ta.rows = Math.min(6, Math.max(2, q.expr.split("\n").length));
    block.append(ta);
    copy.addEventListener("click", () => copyText(wrap, ta.value, "Query copied"));
    body.append(block);
  }

  const m = cell.meta;
  const prov = el("div", "qp-prov");
  prov.append(provRow("author", m.provenance.author));
  prov.append(provRow("datasource", m.provenance.datasource));
  prov.append(provRow("time range", `${new Date(m.timeRange.from).toLocaleString()} → ${new Date(m.timeRange.to).toLocaleString()}`, true));
  prov.append(provRow("as of", new Date(m.attestation.asOf).toLocaleString(), true));
  if (m.resultInfo) prov.append(provRow("result", m.resultInfo, true));
  prov.append(provRow("live", String(m.attestation.live)));
  if (m.provenance.rbacScope) prov.append(provRow("RBAC scope", m.provenance.rbacScope));
  if (m.provenance.orgId != null) prov.append(provRow("org id", String(m.provenance.orgId), true));
  prov.append(provRow("confidence", m.confidence));
  prov.append(provRow("data mode", m.dataMode));
  body.append(prov);

  toggle.addEventListener("click", () => { const open = wrap.classList.toggle("open"); toggle.classList.toggle("open", open); });
  wrap.append(body);
  return wrap;
}

function provRow(k: string, v: string, mono = false): HTMLElement {
  const r = el("div", "prov-row");
  r.append(el("span", "prov-k", k));
  r.append(el("span", `prov-v${mono ? " mono" : ""}`, v));
  return r;
}

// --- panel dispatch ----------------------------------------------------------

function renderPanel(cell: InsightCell, ctx: RenderCtx): Panel {
  switch (cell.renderHint.type) {
    case "timeseries": return timeseriesPanel(cell, ctx);
    case "stat": return statPanel(cell);
    case "bar": return barPanel(cell);
    case "table": return tablePanel(cell);
    case "logs": return logsPanel(cell);
    case "trace": return tracePanel(cell);
    case "worklist": return worklistPanel(cell, ctx);
    case "rca": return rcaPanel(cell);
    case "rulediff": return ruleDiffPanel(cell);
    case "timeline": return timelinePanel(cell);
    case "cost": return costPanel(cell);
    case "bullet": return bulletPanel(cell);
    default: return { node: el("div", "empty", `Unsupported panel type: ${(cell.renderHint as any).type}`) };
  }
}

function firstFrame(cell: InsightCell): DataFrame | undefined {
  return cell.frames?.[0];
}

/** Which panel types this frame's shape can also render as. */
function switchableTypes(frame: DataFrame | undefined, current: PanelType): PanelType[] {
  if (!frame) return [];
  const hasTime = frame.fields.some((f) => f.type === "time");
  const hasNum = frame.fields.some((f) => f.type === "number");
  const hasStr = frame.fields.some((f) => f.type === "string");
  const set = new Set<PanelType>();
  set.add("table");
  if (hasNum) { set.add("stat"); set.add("bullet"); }
  if (hasTime && hasNum) set.add("timeseries");
  if (hasStr && hasNum) set.add("bar");
  set.delete(current);
  return [...set];
}

// --- timeseries (uPlot) ------------------------------------------------------

function timeseriesPanel(cell: InsightCell, ctx: RenderCtx): Panel {
  const frame = firstFrame(cell);
  const node = el("div", "panel");
  const chart = el("div", "uplot-host");
  const legend = el("div", "legend");
  node.append(chart);
  if (!frame) { node.append(el("div", "empty", "No data")); return { node }; }

  const timeField = frame.fields.find((f) => f.type === "time");
  const seriesFields = frame.fields.filter((f) => f.type === "number");
  const times = (timeField?.values ?? []) as number[];
  const data: uPlot.AlignedData = [times, ...seriesFields.map((f) => f.values as (number | null)[])];

  const toolbar: PanelToolbar = { changeTypes: switchableTypes(frame, "timeseries") };
  let selRange: [number, number] | null = null; // seconds

  const mount = () => {
    let height = 250;
    const width = () => Math.max(320, node.clientWidth || 640);
    const thresholds = cell.renderHint.thresholds ?? [];
    const gt = grafanaChartTheme();
    const hhmm = (s: number) => new Date(s * 1000).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });

    // in-chart tooltip + select-and-ask affordance (appended to the plot over-layer)
    const tip = el("div", "uplot-tip");
    const ask = el("button", "ask-btn") as HTMLButtonElement;
    ask.textContent = "Ask about this window →";
    ask.style.display = "none";

    const opts: uPlot.Options = {
      width: width(), height,
      cursor: { points: { size: 6 }, drag: { x: true, y: false, setScale: false } },
      legend: { show: false },
      scales: { x: { time: true } },
      axes: [
        { stroke: gt.axis, font: GRAFANA_AXIS_FONT, grid: { stroke: gt.grid, width: 1 }, ticks: { stroke: gt.grid, width: 1 },
          // single-line time labels (no second date row)
          values: (_u, splits) => splits.map((v) => hhmm(v as number)) },
        { stroke: gt.axis, font: GRAFANA_AXIS_FONT, size: 60, grid: { stroke: gt.grid, width: 1 }, ticks: { stroke: gt.grid, width: 1 },
          values: (_u, s) => s.map((v) => fmtNum(v, cell.renderHint.unit)) },
      ],
      series: [
        {},
        ...seriesFields.map((f, i) => ({ label: f.name, stroke: f.color ?? PALETTE[i % PALETTE.length], width: 1.5, points: { show: false } })),
      ],
      plugins: thresholds.length ? [thresholdPlugin(thresholds)] : [],
      hooks: {
        init: [(u) => { u.over.appendChild(tip); u.over.appendChild(ask); }],
        setCursor: [(u) => updateTip(u, tip, seriesFields, cell.renderHint.unit)],
        setSelect: [(u) => {
          if (u.select.width > 8) {
            selRange = [u.posToVal(u.select.left, "x"), u.posToVal(u.select.left + u.select.width, "x")];
            ask.style.display = "";
            ask.style.left = `${u.select.left + u.select.width / 2}px`;
          } else {
            selRange = null;
            ask.style.display = "none";
          }
        }],
      },
    };
    const u = new uPlot(opts, data, chart);

    ask.addEventListener("click", () => {
      if (!selRange) return;
      const [from, to] = selRange;
      ctx.onAction(
        {
          kind: "ask",
          label: "ask",
          text: `Investigate "${cell.renderHint.title}" between ${hhmm(from)} and ${hhmm(to)} — what changed in that window and why? Render the most useful follow-up.`,
        },
        ctx.cell, ask,
      );
      ask.style.display = "none";
      u.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false);
    });

    seriesFields.forEach((f, i) => {
      const chip = el("button", "legend-chip") as HTMLButtonElement;
      const dot = el("span", "legend-dot"); dot.style.background = f.color ?? PALETTE[i % PALETTE.length];
      chip.append(dot, document.createTextNode(f.name));
      chip.addEventListener("click", () => { const show = !(u.series[i + 1].show ?? true); u.setSeries(i + 1, { show }); chip.classList.toggle("off", !show); });
      legend.append(chip);
    });

    toolbar.resetZoom = () => u.setScale("x", { min: times[0], max: times[times.length - 1] });
    toolbar.zoomToSelection = () => { if (selRange) u.setScale("x", { min: selRange[0], max: selRange[1] }); };
    toolbar.setFullscreen = (fs) => { height = fs ? 480 : 250; u.setSize({ width: width(), height }); };

    new ResizeObserver(() => u.setSize({ width: width(), height })).observe(node);
  };

  return { node, legend, mount, toolbar };
}

function thresholdPlugin(thresholds: Threshold[]): uPlot.Plugin {
  return {
    hooks: {
      draw: (u) => {
        const ctx = u.ctx;
        for (const th of thresholds) {
          const y = u.valToPos(th.value, "y", true);
          ctx.save();
          ctx.strokeStyle = resolveColor(th.color);
          ctx.setLineDash([4, 4]); ctx.lineWidth = 1;
          ctx.beginPath(); ctx.moveTo(u.bbox.left, y); ctx.lineTo(u.bbox.left + u.bbox.width, y); ctx.stroke();
          ctx.restore();
        }
      },
    },
  };
}

function updateTip(u: uPlot, tip: HTMLElement, fields: DataFrame["fields"], unit?: string) {
  const idx = u.cursor.idx;
  const left = u.cursor.left ?? -1;
  if (idx == null || left < 0) { tip.style.display = "none"; return; }
  const t = new Date((u.data[0][idx] as number) * 1000).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });

  tip.innerHTML = "";
  const time = el("div", "tip-time mono", t);
  tip.append(time);
  fields.forEach((f, i) => {
    const row = el("div", "tip-row");
    const dot = el("span", "tip-dot");
    dot.style.background = f.color ?? PALETTE[i % PALETTE.length];
    row.append(dot, el("span", "tip-name", f.name), el("span", "tip-val mono", fmtNum(u.data[i + 1][idx] as number, unit)));
    tip.append(row);
  });

  tip.style.display = "";
  tip.style.left = `${left}px`;
  tip.style.top = `${u.cursor.top ?? 0}px`;
  tip.classList.toggle("flip", left > u.over.clientWidth * 0.62);
}

// --- stat --------------------------------------------------------------------

function statPanel(cell: InsightCell): Panel {
  const frame = firstFrame(cell);
  const node = el("div", "panel stat");
  const valueField = frame?.fields.find((f) => f.name === (cell.renderHint.valueField ?? "value")) ??
    frame?.fields.filter((f) => f.type === "number").at(-1);
  const series = (valueField?.values ?? []).map(Number).filter((n) => !Number.isNaN(n));
  const value = series.at(-1);
  const prev = series.at(0);

  // Grafana field-config pipeline: value text + threshold/mapping color.
  const dv = displayValue(value, {
    unit: cell.renderHint.unit,
    decimals: cell.renderHint.decimals,
    thresholds: cell.renderHint.thresholds,
    mappings: cell.renderHint.mappings,
  });

  const big = el("div", "stat-value mono");
  big.textContent = value == null ? "no data" : dv.text;
  if (dv.color) big.style.color = dv.color;
  node.append(big);

  if (value != null && prev != null && prev !== 0) {
    const d = ((value - prev) / Math.abs(prev)) * 100;
    node.append(el("div", `stat-delta ${d >= 0 ? "up" : "down"}`, `${d >= 0 ? "▲" : "▼"} ${Math.abs(d).toFixed(0)}% vs start of window`));
  }
  let mount: (() => void) | undefined;
  if (series.length > 1) {
    const host = el("div", "spark");
    node.append(host);
    mount = () => mountSparkline(host, series, dv.color);
  }
  return { node, mount, toolbar: { changeTypes: switchableTypes(frame, "stat") } };
}

/** A minimal uPlot sparkline (no axes/legend/cursor), on the same stack as timeseries. */
function mountSparkline(host: HTMLElement, series: number[], color?: string) {
  const stroke = color ?? cssVar("--accent");
  const h = 46;
  const width = () => Math.max(120, host.clientWidth || 220);
  const xs = series.map((_, i) => i);
  const opts: uPlot.Options = {
    width: width(), height: h,
    cursor: { show: false },
    legend: { show: false },
    scales: { x: { time: false } },
    axes: [{ show: false }, { show: false }],
    series: [{}, { stroke, width: 2, fill: sparkFill(stroke), points: { show: false } }],
  };
  const u = new uPlot(opts, [xs, series], host);
  new ResizeObserver(() => u.setSize({ width: width(), height: h })).observe(host);
}

/** Translucent area fill derived from a #rrggbb stroke (else no fill). */
function sparkFill(stroke: string): string | undefined {
  const m = /^#([0-9a-f]{6})$/i.exec(stroke.trim());
  if (!m) return undefined;
  const n = parseInt(m[1], 16);
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, 0.15)`;
}

/** #rrggbb -> rgba(...,a); returns the input unchanged if not a hex color. */
function tint(color: string, a: number): string {
  const m = /^#([0-9a-f]{6})$/i.exec(color.trim());
  if (!m) return color;
  const n = parseInt(m[1], 16);
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${a})`;
}

// --- bullet (compact measure vs target, with qualitative bands) --------------

function bulletPanel(cell: InsightCell): Panel {
  const node = el("div", "panel");
  const frame = firstFrame(cell);
  const rh = cell.renderHint;
  const valueField = frame?.fields.find((f) => f.name === (rh.valueField ?? "value")) ??
    frame?.fields.filter((f) => f.type === "number").at(-1);
  const series = (valueField?.values ?? []).map(Number).filter((n) => !Number.isNaN(n));
  const value = series.at(-1);
  const toolbar = { changeTypes: switchableTypes(frame, "bullet") };
  if (value == null) { node.append(el("div", "empty", "No value")); return { node, toolbar }; }

  const thresholds = [...(rh.thresholds ?? [])].sort((a, b) => a.value - b.value);
  const target = rh.target;
  const dv = displayValue(value, { unit: rh.unit, decimals: rh.decimals, thresholds: rh.thresholds, mappings: rh.mappings });
  const nums = [value, ...(target != null ? [target] : []), ...thresholds.map((t) => t.value)].filter((n) => Number.isFinite(n));
  const max = rh.max ?? Math.max(...nums, 1) * 1.1;
  const pct = (n: number) => Math.max(0, Math.min(100, (n / max) * 100));

  const wrap = el("div", "bullet");
  const head = el("div", "bullet-head");
  head.append(el("span", "bullet-value mono", dv.text));
  if (target != null) head.append(el("span", "bullet-target-label mono", `target ${fmtNum(target, rh.unit)}`));
  if (dv.color) (head.firstChild as HTMLElement).style.color = dv.color;
  wrap.append(head);

  // qualitative bands from thresholds (faint threshold colors), square corners.
  const track = el("div", "bullet-track");
  const bounds = [0, ...thresholds.map((t) => t.value), max];
  const bandColors = [resolveColor("ok"), ...thresholds.map((t) => resolveColor(t.color))];
  for (let i = 0; i < bounds.length - 1; i++) {
    const seg = el("div", "bullet-band");
    seg.style.left = `${pct(bounds[i])}%`;
    seg.style.width = `${pct(bounds[i + 1]) - pct(bounds[i])}%`;
    seg.style.background = tint(bandColors[i] ?? cssVar("--track"), 0.18);
    track.append(seg);
  }
  // measure bar (thin, overlaid) + target tick
  const measure = el("div", "bullet-measure");
  measure.style.width = `${pct(value)}%`;
  measure.style.background = dv.color ?? cssVar("--ink");
  track.append(measure);
  if (target != null) {
    const tk = el("div", "bullet-target");
    tk.style.left = `${pct(target)}%`;
    track.append(tk);
  }
  wrap.append(track);
  node.append(wrap);
  return { node, toolbar };
}

// --- bar ---------------------------------------------------------------------

function barPanel(cell: InsightCell): Panel {
  const frame = firstFrame(cell);
  const node = el("div", "panel");
  if (!frame) { node.append(el("div", "empty", "No data")); return { node }; }
  const cat = frame.fields.find((f) => f.type === "string");
  const val = frame.fields.find((f) => f.type === "number");
  if (!cat || !val) { node.append(el("div", "empty", "Bar needs a label and a value field")); return { node, toolbar: { changeTypes: switchableTypes(frame, "bar") } }; }

  let rows = cat.values.map((label, i) => ({ label: String(label), value: Number(val.values[i]) }));
  if (cell.renderHint.sort !== "none") rows.sort((a, b) => cell.renderHint.sort === "asc" ? a.value - b.value : b.value - a.value);
  const max = Math.max(...rows.map((r) => r.value), 1);

  // Grafana bar-gauge style: one consistent series color (field color, else the
  // classic green), not a per-bar rainbow.
  const barColor = val.color ?? PALETTE[0];
  const list = el("div", "bars");
  rows.forEach((r) => {
    const row = el("div", "bar-row");
    row.append(el("div", "bar-label", r.label));
    const track = el("div", "bar-track");
    const fill = el("div", "bar-fill");
    fill.style.width = `${(r.value / max) * 100}%`;
    fill.style.background = barColor;
    track.append(fill); row.append(track);
    row.append(el("div", "bar-value mono", fmtNum(r.value, val.unit)));
    list.append(row);
  });
  node.append(list);
  return { node, toolbar: { changeTypes: switchableTypes(frame, "bar") } };
}

// --- table -------------------------------------------------------------------

function tablePanel(cell: InsightCell): Panel {
  const frame = firstFrame(cell);
  const node = el("div", "panel");
  if (!frame) { node.append(el("div", "empty", "No data")); return { node }; }
  const cols = frame.fields;
  const nRows = Math.max(...cols.map((c) => c.values.length));

  let sortCol = -1, sortDir: 1 | -1 = 1;
  const table = document.createElement("table");
  table.className = "grid";
  const build = () => {
    table.innerHTML = "";
    const thead = document.createElement("thead");
    const htr = document.createElement("tr");
    cols.forEach((c, ci) => {
      const th = document.createElement("th");
      th.textContent = c.name + (sortCol === ci ? (sortDir === 1 ? " ▲" : " ▼") : "");
      if (c.type === "number") th.classList.add("num");
      th.addEventListener("click", () => { if (sortCol === ci) sortDir = (sortDir === 1 ? -1 : 1) as any; else { sortCol = ci; sortDir = 1; } build(); });
      htr.append(th);
    });
    thead.append(htr); table.append(thead);

    let order = Array.from({ length: nRows }, (_, i) => i);
    if (sortCol >= 0) {
      const c = cols[sortCol];
      order.sort((a, b) => (c.type === "number" ? Number(c.values[a]) - Number(c.values[b]) : String(c.values[a]).localeCompare(String(c.values[b]))) * sortDir);
    }
    const tbody = document.createElement("tbody");
    for (const r of order) {
      const tr = document.createElement("tr");
      cols.forEach((c) => {
        const td = document.createElement("td");
        td.textContent = c.type === "number" ? fmtNum(Number(c.values[r]), c.unit) : String(c.values[r] ?? "");
        if (c.type === "number") td.classList.add("num");
        tr.append(td);
      });
      tbody.append(tr);
    }
    table.append(tbody);
  };
  build();
  node.append(table);
  return { node, toolbar: { changeTypes: switchableTypes(frame, "table") } };
}

// --- logs --------------------------------------------------------------------

function logsPanel(cell: InsightCell): Panel {
  const node = el("div", "panel");
  const logs = cell.logs ?? [];
  const levels = ["error", "warn", "info", "debug"] as const;
  const active = new Set<string>(levels);
  const filter = el("div", "log-filter");
  const listWrap = el("div", "log-list");
  const draw = () => {
    listWrap.innerHTML = "";
    for (const l of logs) if (active.has(l.level)) listWrap.append(logRow(l));
    if (!listWrap.childElementCount) listWrap.append(el("div", "empty", "No lines match the filter"));
  };
  levels.forEach((lv) => {
    const chip = el("button", `log-chip ${lv}`, `${lv} ${logs.filter((l) => l.level === lv).length}`) as HTMLButtonElement;
    chip.addEventListener("click", () => { active.has(lv) ? active.delete(lv) : active.add(lv); chip.classList.toggle("off"); draw(); });
    filter.append(chip);
  });
  draw();
  node.append(filter, listWrap);
  return { node };
}

function logRow(l: LogLine): HTMLElement {
  const row = el("div", `log-row ${l.level}`);
  row.append(el("span", "log-time mono", new Date(l.time).toLocaleTimeString()));
  row.append(el("span", `log-level ${l.level}`, l.level.toUpperCase()));
  row.append(el("span", "log-msg mono", l.line));
  return row;
}

// --- trace -------------------------------------------------------------------

function tracePanel(cell: InsightCell): Panel {
  const node = el("div", "panel");
  const trace = cell.trace as TracePayload | undefined;
  if (!trace) { node.append(el("div", "empty", "No trace")); return { node }; }
  node.append(el("div", "trace-dur", `Trace ${trace.traceId} · ${trace.durationMs} ms`));
  const total = trace.durationMs || 1;
  for (const s of trace.spans) {
    const row = el("div", `trace-row${s.rootCause ? " root" : ""}`);
    const label = el("div", "trace-label", s.name); label.title = `${s.service} · ${s.name}`;
    row.append(label);
    const lane = el("div", "trace-lane");
    const barc = el("div", `trace-bar ${s.status === "error" ? "error" : "ok"}${s.rootCause ? " root" : ""}`);
    barc.style.left = `${(s.startMs / total) * 100}%`;
    barc.style.width = `${Math.max(1, (s.durationMs / total) * 100)}%`;
    lane.append(barc); row.append(lane);
    row.append(el("div", "trace-ms mono", `${s.durationMs} ms`));
    node.append(row);
    if (s.rootCause && s.tags) {
      node.append(el("div", "trace-tags mono", Object.entries(s.tags).map(([k, v]) => `${k}=${v}`).join("  ·  ")));
    }
  }
  return { node };
}

// --- worklist (ranked, actionable findings) ----------------------------------

const PRIO_RANK: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3 };

function worklistPanel(cell: InsightCell, ctx: RenderCtx): Panel {
  const node = el("div", "panel");
  const items = [...(cell.worklist ?? [])].sort(
    (a, b) => (PRIO_RANK[a.priority ?? "low"] ?? 9) - (PRIO_RANK[b.priority ?? "low"] ?? 9),
  );
  if (!items.length) { node.append(el("div", "empty", "No items")); return { node }; }

  const list = el("div", "worklist");
  for (const it of items) {
    const row = el("div", "wl-item");
    row.append(el("span", `wl-prio ${it.priority ?? "low"}`, (it.priority ?? "").toUpperCase() || "—"));

    const main = el("div", "wl-main");
    const titleRow = el("div", "wl-titlerow");
    titleRow.append(el("span", "wl-title", it.title));
    if (it.status) titleRow.append(el("span", `wl-status ${it.statusTone ?? ""}`, it.status));
    main.append(titleRow);
    if (it.why) main.append(el("div", "wl-why", it.why));
    if (it.actions?.length) {
      const acts = el("div", "wl-actions");
      // Pills (actions) first, then icon buttons (view trend / open in Grafana).
      const ordered = [...it.actions].sort((a, b) => Number(isIconAction(a)) - Number(isIconAction(b)));
      for (const a of ordered) acts.append(actionButton(a, cell, ctx.onAction));
      main.append(acts);
    }
    row.append(main);
    list.append(row);
  }
  node.append(list);
  return { node };
}

// --- RCA / Sift investigation ------------------------------------------------

function rcaPanel(cell: InsightCell): Panel {
  const node = el("div", "panel");
  const rca = cell.rca;
  if (!rca) { node.append(el("div", "empty", "No investigation")); return { node }; }

  // Root-cause banner
  if (rca.rootCause) {
    const rc = el("div", "rca-root");
    const head = el("div", "rca-root-head");
    head.append(el("span", "rca-root-label", "Root cause"));
    head.append(el("span", `rca-conf ${rca.rootCause.confidence}`, `${rca.rootCause.confidence} confidence`));
    rc.append(head);
    rc.append(el("div", "rca-root-title", rca.rootCause.title));
    if (rca.rootCause.detail) rc.append(el("div", "rca-root-detail", rca.rootCause.detail));
    node.append(rc);
  }

  // Checks run
  if (rca.checks?.length) {
    const chks = el("div", "rca-checks");
    chks.append(el("span", "rca-checks-label", "Checked"));
    for (const c of rca.checks) chks.append(el("span", "rca-check", c));
    node.append(chks);
  }

  // Findings
  const list = el("div", "rca-findings");
  for (const f of rca.findings) {
    const row = el("div", "rca-finding");
    const dot = el("span", `rca-dot ${f.severity ?? "neutral"}`);
    row.append(dot);
    const main = el("div", "rca-main");
    const titleRow = el("div", "rca-finding-head");
    if (f.kind) titleRow.append(el("span", "rca-kind", f.kind));
    titleRow.append(el("span", "rca-finding-title", f.title));
    main.append(titleRow);
    if (f.detail) main.append(el("div", "rca-detail", f.detail));
    if (f.evidence) main.append(el("div", "rca-evidence mono", f.evidence));
    row.append(main);
    list.append(row);
  }
  node.append(list);
  return { node };
}

// --- rulediff (propose an alert-rule fix → before/after → apply) -------------

function ruleDiffPanel(cell: InsightCell): Panel {
  const node = el("div", "panel");
  const rd = cell.rulediff;
  if (!rd) { node.append(el("div", "empty", "No rule change")); return { node }; }

  const head = el("div", "rd-head");
  const title = el("div", "rd-rule");
  title.append(el("span", "rd-rule-name", rd.ruleTitle));
  if (rd.ruleUid) title.append(el("span", "rd-uid mono", rd.ruleUid));
  head.append(title);
  head.append(el("span", `rd-badge ${rd.applied ? "applied" : "proposed"}`, rd.applied ? "Applied" : "Proposed"));
  node.append(head);

  if (rd.summary) node.append(el("div", "rd-summary", rd.summary));

  const list = el("div", "rd-changes");
  for (const c of rd.changes) {
    const chg = el("div", "rd-change");
    chg.append(el("div", "rd-field", c.field));
    const diff = el("div", "rd-diff");
    const before = el("div", "rd-line before");
    before.append(el("span", "rd-mark", "−"), el("span", "rd-code mono", c.before));
    const after = el("div", "rd-line after");
    after.append(el("span", "rd-mark", "+"), el("span", "rd-code mono", c.after));
    diff.append(before, after);
    chg.append(diff);
    if (c.rationale) chg.append(el("div", "rd-rationale", c.rationale));
    list.append(chg);
  }
  node.append(list);
  return { node };
}

// --- timeline (change-correlation) -------------------------------------------

function hhmm(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

function timelinePanel(cell: InsightCell): Panel {
  const node = el("div", "panel");
  const tl = cell.timeline;
  if (!tl || !tl.events.length) { node.append(el("div", "empty", "No change events")); return { node }; }
  const from = new Date(tl.from).getTime();
  const to = new Date(tl.to).getTime();
  const span = Math.max(1, to - from);
  const pos = (iso: string) => Math.min(100, Math.max(0, ((new Date(iso).getTime() - from) / span) * 100));

  // Axis track with a pin per event.
  const track = el("div", "tl-track");
  track.append(el("div", "tl-axis"));
  for (const e of tl.events) {
    const pin = el("div", `tl-pin ${e.kind ?? "other"}${e.correlated ? " correlated" : ""}`);
    pin.style.left = `${pos(e.time)}%`;
    pin.title = `${new Date(e.time).toLocaleString()} — ${e.title}`;
    track.append(pin);
  }
  node.append(track);
  const axisLabels = el("div", "tl-axis-labels");
  axisLabels.append(el("span", "mono", hhmm(from)), el("span", "mono", hhmm(to)));
  node.append(axisLabels);

  // Ordered event list (stands alone when pins overlap).
  const list = el("div", "tl-list");
  for (const e of tl.events) {
    const row = el("div", `tl-item${e.correlated ? " correlated" : ""}`);
    row.append(el("span", "tl-time mono", hhmm(new Date(e.time).getTime())));
    row.append(el("span", `tl-dot ${e.kind ?? "other"}`));
    const main = el("div", "tl-main");
    const head = el("div", "tl-head");
    head.append(el("span", `tl-kind ${e.kind ?? "other"}`, e.kind ?? "event"));
    head.append(el("span", "tl-title", e.title));
    if (e.correlated) head.append(el("span", "tl-corr", "correlated"));
    main.append(head);
    if (e.detail) main.append(el("div", "tl-detail", e.detail));
    if (e.tags?.length) {
      const tg = el("div", "tl-tags");
      for (const t of e.tags) tg.append(el("span", "tl-tag mono", t));
      main.append(tg);
    }
    row.append(main);
    list.append(row);
  }
  node.append(list);
  return { node };
}

// --- cost / cardinality ------------------------------------------------------

function costPanel(cell: InsightCell): Panel {
  const node = el("div", "panel");
  const c = cell.cost;
  if (!c || !c.drivers.length) { node.append(el("div", "empty", "No cost data")); return { node }; }

  if (c.total) {
    const tot = el("div", "cost-total");
    tot.append(el("span", "cost-total-val mono", c.total.label));
    node.append(tot);
  }

  const max = Math.max(...c.drivers.map((d) => d.series ?? d.value ?? 0), 1);
  const list = el("div", "cost-list");
  c.drivers.forEach((d, i) => {
    const row = el("div", "cost-row");
    const head = el("div", "cost-head");
    head.append(el("span", "cost-name mono", d.name));
    const primary = d.series != null ? `${fmtNum(d.series, "short")} series` : d.value != null ? fmtNum(d.value, d.unit) : "";
    head.append(el("span", "cost-val mono", primary + (d.pct != null ? `  ·  ${d.pct.toFixed(0)}%` : "")));
    row.append(head);
    const trk = el("div", "cost-track");
    const fill = el("div", "cost-fill");
    fill.style.width = `${((d.series ?? d.value ?? 0) / max) * 100}%`;
    fill.style.background = PALETTE[i % PALETTE.length];
    trk.append(fill); row.append(trk);
    if (d.note) row.append(el("div", "cost-note", d.note));
    list.append(row);
  });
  node.append(list);

  if (c.headroom) {
    const hr = el("div", "cost-headroom");
    hr.append(el("div", "cost-headroom-label", c.headroom.label));
    hr.append(el("div", "cost-headroom-detail", c.headroom.detail));
    node.append(hr);
  }
  return { node };
}

// --- number formatting -------------------------------------------------------

function fmtNum(n: number, unit?: string, decimals?: number): string {
  return formatValue(n, unit, decimals);
}
