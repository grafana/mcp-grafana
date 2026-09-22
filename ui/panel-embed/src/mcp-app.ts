import { App, applyHostFonts, applyHostStyleVariables } from "@modelcontextprotocol/ext-apps";

// Defines <grafana-panel>. The element carries its own styles, so there is nothing
// else to import.
import { framesFromPromMatrix, framesFromQueryResponse } from "./vendor/grafana-embed.mjs";

/**
 * Renders run_panel_query results as live Grafana panels, interleaved with this app's
 * own text and one host control that drives every panel at once.
 *
 * The point is not "a chart in an iframe". It is that a Grafana panel is an element
 * among the host's own elements: the agent composes the screen, and the graphs in it
 * are Grafana's.
 */

const el = {
  title: document.getElementById("title")!,
  meta: document.getElementById("meta")!,
  error: document.getElementById("error")!,
  controls: document.getElementById("controls")!,
  note: document.getElementById("note")!,
  panels: document.getElementById("panels")!,
  status: document.getElementById("status")!,
};

const RANGE_PRESETS = ["15m", "1h", "6h", "24h", "7d", "30d"] as const;
const DEFAULT_RANGE = "1h";

/** The window the panels are currently showing, as Grafana raw range values. */
let currentWindow = { from: `now-${DEFAULT_RANGE}`, to: "now" };

/**
 * Panels take their window and their light/dark signal from the host, never from the
 * OS. The element infers mode from the host's background token too, but an explicit
 * signal is better when the host gives us one.
 */
function applyWindow(element: GrafanaPanelElement) {
  element.setAttribute("from", currentWindow.from);
  element.setAttribute("to", currentWindow.to);
  const theme = app?.getHostContext()?.theme;
  if (theme === "light" || theme === "dark") {
    element.setAttribute("theme", theme);
  }
}

/** One of a panel's queries. A panel's queries are layers of the same picture. */
interface PanelQueryExecution {
  refId?: string;
  query?: string;
  results?: unknown;
}

interface PanelResult {
  panelId?: number;
  panelTitle?: string;
  query?: string;
  datasourceType?: string;
  results?: unknown;
  /** Present when the panel has more than one query. */
  queries?: PanelQueryExecution[];
  /** Classic v1 panel JSON from the dashboard: how the panel actually draws. */
  panelSpec?: Record<string, unknown>;
  /** Things the tool could only say in words, e.g. unapplied transformations. */
  hints?: string[];
}

interface RunPanelQueryResult {
  dashboardUid?: string;
  results?: Record<string, PanelResult>;
  errors?: Record<string, string>;
  /** The window the tool actually queried, as Grafana raw range values. */
  timeRange?: { start?: string; end?: string };
}

let app: App | null = null;
let lastToolArgs: Record<string, unknown> = {};
let currentRange: string = DEFAULT_RANGE;
/** Panel id -> element, so a re-query updates in place instead of remounting. */
const mounted = new Map<string, GrafanaPanelElement>();

// --- rendering ------------------------------------------------------------

function showError(message: string) {
  el.error.textContent = message;
  el.error.hidden = false;
}

function clearError() {
  el.error.hidden = true;
}

/**
 * Body height per panel type. A timeseries and a flamegraph do not want the same
 * default; these are the values grafana-assistant-app arrived at for the same job
 * (apps/plugin/src/features/canvas/utils/panelHeights.ts).
 */
const PANEL_HEIGHTS: Record<string, number> = { logs: 420, flamegraph: 420, traces: 560 };
const PANEL_HEIGHT_DEFAULT = 280;

function panelHeight(spec: Record<string, unknown> | undefined) {
  const type = typeof spec?.type === "string" ? spec.type : undefined;
  return (type && PANEL_HEIGHTS[type]) || PANEL_HEIGHT_DEFAULT;
}

/**
 * The panel to draw when the tool gave us no spec: an older server, or a panel whose
 * JSON carries no type.
 *
 * Deliberately plain. It is a stand-in, not a guess at the real panel - inventing
 * thresholds or units here would make a wrong panel look authoritative, and the
 * heading already says which panel this is meant to be.
 */
function fallbackPanelJson(title: string, unit?: string) {
  return {
    type: "timeseries",
    title,
    fieldConfig: {
      defaults: {
        color: { mode: "palette-classic" },
        ...(unit ? { unit } : {}),
        custom: { drawStyle: "line", lineWidth: 1.5, fillOpacity: 8, showPoints: "never", spanNulls: true },
      },
      overrides: [],
    },
    options: {
      legend: { showLegend: true, displayMode: "list", placement: "bottom", calcs: [] },
      tooltip: { mode: "multi", sort: "desc" },
    },
  };
}

/**
 * Decode whatever the tool returned into frames.
 *
 * A Grafana /api/ds/query response works for every datasource and is the durable
 * path. A raw Prometheus matrix is the shape today's tools return, so both are
 * accepted and the response shape decides.
 */
/** A Grafana /api/ds/query response body, as opposed to a raw Prometheus matrix. */
function isQueryResponseBody(value: unknown): value is { results: Record<string, { frames?: unknown[] }> } {
  return typeof value === "object" && value !== null && !Array.isArray(value) && "results" in value;
}

function framesFor(results: unknown, title: string): unknown[] {
  if (isQueryResponseBody(results)) {
    try {
      return framesFromQueryResponse(results);
    } catch {
      // fall through to the Prometheus path
    }
  }
  const matrix = Array.isArray(results)
    ? results
    : ((results as { data?: unknown })?.data as unknown[] | undefined) ?? [];
  return Array.isArray(matrix) ? framesFromPromMatrix(matrix as never, { fallbackName: title }) : [];
}

function renderPanels(payload: RunPanelQueryResult) {
  // The agent chose the window, not this app: a panel whose dashboard defaults to
  // 7 days must not be drawn inside a 1-hour axis.
  const { start, end } = payload.timeRange ?? {};
  if (start && end) {
    currentWindow = { from: start, to: end };
    const preset = RANGE_PRESETS.find((r) => `now-${r}` === start && end === "now") ?? null;
    currentRange = preset ?? currentRange;
    pressRange(preset);
  }

  const entries = Object.entries(payload.results ?? {});
  if (entries.length === 0) {
    el.status.textContent = "Query returned no panels";
    el.status.hidden = false;
    return;
  }

  el.status.hidden = true;
  el.controls.hidden = false;

  for (const element of mounted.values()) {
    applyWindow(element);
  }

  const rendered: string[] = [];
  for (const [key, panel] of entries) {
    const title = panel.panelTitle || panel.query || `Panel ${key}`;
    // Every query the panel declares, concatenated. Grafana draws a multi-query panel
    // by putting all of its series on one set of axes and letting the panel's own
    // overrides style them, which is exactly what handing the element one frame list
    // does - the `^avg - .*` style override finds its series by name either way.
    const executions = panel.queries?.length ? panel.queries : [{ query: panel.query, results: panel.results }];
    const frames = executions.flatMap((execution) => framesFor(execution.results, title));
    if (frames.length === 0) {
      continue;
    }
    rendered.push(key);

    let element = mounted.get(key);
    if (!element) {
      const wrapper = document.createElement("div");
      wrapper.className = "panel";

      const heading = document.createElement("h2");
      heading.textContent = title;
      wrapper.appendChild(heading);

      for (const execution of executions) {
        if (!execution.query) {
          continue;
        }
        const query = document.createElement("p");
        query.className = "q";
        query.textContent = execution.refId ? `${execution.refId}: ${execution.query}` : execution.query;
        wrapper.appendChild(query);
      }

      const hint = document.createElement("p");
      hint.className = "hint";
      hint.hidden = true;
      wrapper.appendChild(hint);

      element = document.createElement("grafana-panel") as GrafanaPanelElement;
      applyWindow(element);
      // A user zoom on any panel re-queries every panel, so they stay comparable.
      element.addEventListener("timerangechange", (event) => {
        const detail = (event as CustomEvent<{ from: number; to: number }>).detail;
        void requery(new Date(detail.from).toISOString(), new Date(detail.to).toISOString(), null);
      });
      wrapper.appendChild(element);

      el.panels.appendChild(wrapper);
      mounted.set(key, element);
    }

    // Shown rather than swallowed: the tool says here when what it returned does
    // not match what Grafana draws, and a panel that looks right while being wrong
    // is worse than one that admits it.
    const hint = element.closest(".panel")?.querySelector<HTMLElement>(".hint");
    if (hint) {
      hint.textContent = (panel.hints ?? []).join(" ");
      hint.hidden = !hint.textContent;
    }

    // The panel the user clicked, not this app's idea of it: units, thresholds,
    // axis placement and per-series overrides all live in the dashboard's own JSON.
    element.panel = panel.panelSpec ?? fallbackPanelJson(title);
    element.setAttribute("height", String(panelHeight(panel.panelSpec)));
    element.frames = frames;
  }

  // Drop panels that are no longer in the result.
  for (const [key, element] of mounted) {
    if (!rendered.includes(key)) {
      element.closest(".panel")?.remove();
      mounted.delete(key);
    }
  }

  const skipped = entries.length - rendered.length;
  el.meta.textContent =
    `${rendered.length} panel${rendered.length === 1 ? "" : "s"}` + (skipped > 0 ? `, ${skipped} with no data` : "");

  if (payload.dashboardUid) {
    el.note.textContent = `Panels executed from dashboard ${payload.dashboardUid}, rendered with Grafana's own visualization pipeline.`;
    el.note.hidden = false;
  }
}

/** `_meta.ui.kind` of the content item carrying the payload, set by the server. */
const PANEL_QUERY_KIND = "panel-query";

type ToolResultContent = { type: string; text?: string; _meta?: { ui?: { kind?: string } } };

/**
 * Pick the payload out of a tool result.
 *
 * Three channels, because hosts disagree about which survive: some drop
 * structuredContent, and Claude Desktop turns an embedded json block into text. The
 * result also leads with a summary written for the model, so "the first text block"
 * is no longer the payload - the tagged item is found by its kind, and the untagged
 * fallback tries every block rather than assuming a position.
 */
function payloadFromToolResult(result: { content?: ToolResultContent[]; structuredContent?: unknown }): unknown {
  if (result.structuredContent && typeof result.structuredContent === "object") {
    return result.structuredContent;
  }

  const items = result.content ?? [];
  const tagged = items.find((item) => item._meta?.ui?.kind === PANEL_QUERY_KIND);
  for (const item of tagged ? [tagged] : items) {
    if (item.type !== "text" || !item.text) {
      continue;
    }
    try {
      const parsed = JSON.parse(item.text);
      if (parsed && typeof parsed === "object") {
        return parsed;
      }
    } catch {
      // A summary rather than the payload; keep looking.
    }
  }

  return undefined;
}

function handleToolResult(result: { content?: ToolResultContent[]; structuredContent?: unknown; isError?: boolean }) {
  if (result.isError) {
    const text = result.content?.find((item) => item.type === "text")?.text ?? "";
    showError(`Tool error: ${text.slice(0, 400)}`);
    return;
  }
  clearError();

  const payload = payloadFromToolResult(result);
  if (!payload) {
    el.status.textContent = "Could not read the tool result";
    return;
  }

  renderPanels(payload as RunPanelQueryResult);
}

// --- host control ---------------------------------------------------------

function pressRange(range: string | null) {
  for (const button of el.controls.querySelectorAll("button")) {
    button.setAttribute("aria-pressed", String(button.dataset.range === range));
  }
}

async function requery(start: string, end: string, range: string | null) {
  if (!app) {
    return;
  }
  if (!app.getHostCapabilities()?.serverTools) {
    showError("This host does not support widget-initiated tool calls, so the time range cannot be changed here.");
    return;
  }
  currentRange = range ?? currentRange;
  currentWindow = { from: start, to: end };
  for (const element of mounted.values()) {
    applyWindow(element);
  }
  pressRange(range);
  el.status.textContent = "Loading…";
  el.status.hidden = false;

  // The tool-input notification carries no tool name per the MCP Apps spec, so the
  // host context is where the originating tool lives. Hosts may namespace it.
  const raw = app.getHostContext()?.toolInfo?.tool?.name ?? "run_panel_query";
  const name = raw.split("__").pop()!.split(":").pop()!;

  try {
    const result = await app.callServerTool({
      name,
      arguments: { ...lastToolArgs, start, end },
    });
    handleToolResult(result as never);
  } catch (err) {
    showError(`Re-query failed: ${(err as Error).message}`);
  }
}

for (const button of el.controls.querySelectorAll("button")) {
  button.addEventListener("click", () => {
    const range = button.dataset.range!;
    void requery(`now-${range}`, "now", range);
  });
}

// --- host theming ---------------------------------------------------------

function applyHostTheme(styles: { variables?: Record<string, string | undefined>; css?: { fonts?: string } } | undefined) {
  if (styles?.variables) {
    // Writes the host's tokens onto :root. <grafana-panel> reads the same names off
    // its own computed style, so the panels adopt the host palette with no mapping
    // here at all.
    applyHostStyleVariables(styles.variables as never);
  }
  if (styles?.css?.fonts) {
    // @font-face is ignored inside a shadow root, so the host's font CSS has to live
    // in this document. That is also why the embed ships no fonts of its own.
    applyHostFonts(styles.css.fonts);
  }
  for (const element of mounted.values()) {
    applyWindow(element);
    element.refreshTheme();
  }
}

// --- bootstrap ------------------------------------------------------------

function isDevMode() {
  return window.parent === window;
}

/**
 * Dev-only: `?fixture=<url>` renders a saved tool result, which is how this app is
 * checked against real Grafana output without a host. Never reachable under a host,
 * and a sandboxed MCP iframe blocks the fetch anyway.
 */
async function loadFixtureFromUrl(url: string) {
  try {
    const response = await fetch(url);
    handleToolResult({ content: [{ type: "text", text: await response.text() }] });
  } catch (err) {
    showError(`Could not load fixture ${url}: ${(err as Error).message}`);
  }
}

const fixtureUrl = isDevMode() ? new URLSearchParams(location.search).get("fixture") : null;

if (fixtureUrl) {
  void loadFixtureFromUrl(fixtureUrl);
  pressRange(DEFAULT_RANGE);
} else if (isDevMode()) {
  // Standalone dev fixture so the app can be opened without a host.
  const now = Math.floor(Date.now() / 1000);
  const mk = (status: string, base: number) => ({
    metric: { __name__: "http_requests_total", status },
    values: Array.from({ length: 120 }, (_, i) => [now - (120 - i) * 30, (base + Math.sin(i / 8) * base * 0.25).toFixed(2)] as [number, string]),
  });
  handleToolResult({
    content: [
      {
        type: "text",
        text: JSON.stringify({
          dashboardUid: "dev-fixture",
          results: {
            "1": { panelId: 1, panelTitle: "Request rate by status", query: 'sum by (status) (rate(http_requests_total[5m]))', results: [mk("200", 120), mk("500", 8)] },
            "2": { panelId: 2, panelTitle: "Error rate", query: 'sum(rate(http_requests_total{status="500"}[5m]))', results: [mk("500", 8)] },
          },
        }),
      },
    ],
  });
  pressRange(DEFAULT_RANGE);
} else {
  app = new App({ name: "Grafana Panels", version: "1.0.0" }, {}, { autoResize: true });

  app.ontoolinput = (params: { arguments?: Record<string, unknown> }) => {
    lastToolArgs = params.arguments ?? {};
  };

  app.ontoolresult = (result) => {
    pressRange(currentRange);
    handleToolResult(result as never);
  };

  app.onhostcontextchanged = (context) => {
    applyHostTheme(context?.styles as never);
  };

  void app.connect().then(() => {
    applyHostTheme(app?.getHostContext()?.styles as never);
  });
}
