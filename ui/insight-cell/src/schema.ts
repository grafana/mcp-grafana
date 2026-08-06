// The render contract — a protocol-neutral insight-cell payload.
//
// This is the "OSS Grafana outside the UI" data model: one shape that carries
// enough for a generic surface to render ANY core Grafana panel (timeseries,
// stat, bar, table) plus logs and traces, for any question that comes through
// the Grafana MCP server. Per the agentic-visualization docs, the cell is:
//
//   data + query + attestation + provenance + reasoning + a declarative renderHint
//
// Only the last render hop differs per surface; the payload is identical whether
// it ends up drawn by uPlot in an MCP App iframe, a PNG, or an A2UI component.
// The tool emits it three ways: content[].text (fallback), structuredContent
// (this object), and _meta["grafana.insightCell/v0"] (the trust metadata).

export type PanelType = "timeseries" | "stat" | "bar" | "table" | "logs" | "trace" | "worklist" | "rca" | "timeline" | "cost" | "bullet";

export type Tone = "ok" | "warn" | "crit" | "neutral";

// --- Grafana-style columnar data frame (what the query tools return) ---------

export type FieldType = "time" | "number" | "string";

export interface Field {
  name: string;
  type: FieldType;
  values: (number | string | null)[];
  unit?: string;
  /** Per-series color override (else the palette is used). */
  color?: string;
}

export interface DataFrame {
  name?: string;
  refId?: string;
  fields: Field[];
}

export interface Threshold {
  value: number;
  /** Grafana color name (green/red/orange/yellow) or a hex value or a tone. */
  color: string;
  label?: string;
}

/** A Grafana-style value mapping (value -> display text/color). */
export interface ValueMapping {
  type: "value" | "range";
  /** for type "value" */
  value?: number | string;
  /** for type "range" */
  from?: number;
  to?: number;
  text?: string;
  color?: string;
}

/** The declarative "how to draw it" hint — the Backend-B field config. */
export interface RenderHint {
  type: PanelType;
  title: string;
  /** Grafana unit id (bytes, s, ms, percent, percentunit, reqps, short, …). */
  unit?: string;
  /** Fixed decimal places; omit for automatic precision. */
  decimals?: number;
  thresholds?: Threshold[];
  mappings?: ValueMapping[];
  description?: string;
  /** stat: which field to read as the value (default: last numeric field). */
  valueField?: string;
  /** bar: sort direction for ranked bars. */
  sort?: "desc" | "asc" | "none";
  /** bullet: target/SLO marker drawn as a tick. */
  target?: number;
  /** bullet: axis max (else derived from value/target/thresholds). */
  max?: number;
}

// --- Logs & traces get their own payloads (not natural as frames) ------------

export type LogLevel = "debug" | "info" | "warn" | "error";

export interface LogLine {
  time: string; // ISO
  level: LogLevel;
  line: string;
  labels?: Record<string, string>;
}

export interface TraceSpan {
  id: string;
  parentId?: string;
  name: string;
  service: string;
  startMs: number; // offset from trace start
  durationMs: number;
  status?: "ok" | "error";
  tags?: Record<string, string | number>;
  /** Mark the root-cause span. */
  rootCause?: boolean;
}

export interface TracePayload {
  traceId: string;
  durationMs: number;
  spans: TraceSpan[];
}

// --- worklist (ranked, actionable findings: triage, deprecations, tasks) -----

export type ItemPriority = "critical" | "high" | "medium" | "low";

export interface WorklistItem {
  title: string;
  /** Drives sort order and the priority pill colour. */
  priority?: ItemPriority;
  /** Short state, e.g. "firing 22m", "flapping", "deprecated", "resolved". */
  status?: string;
  statusTone?: Tone;
  /** The agent's synthesis / correlation — why this matters and what to do. The differentiator. */
  why?: string;
  /** Per-item actions (investigate, silence, view logs, raise PR, …). */
  actions?: InsightCellAction[];
}

// --- RCA / Sift investigation (findings → root cause → evidence) -------------

export type RcaConfidence = "low" | "medium" | "high";

export interface RcaFinding {
  title: string;
  /** e.g. "error pattern", "slow request", "recent change", "resource", "correlation". */
  kind?: string;
  severity?: Tone;
  detail?: string;
  /** A concrete piece of evidence — a log line, metric value, deploy id. */
  evidence?: string;
}

export interface RcaPayload {
  rootCause?: { title: string; confidence: RcaConfidence; detail?: string };
  /** What was examined (shown as chips) — e.g. "error logs", "slow requests", "recent deploys". */
  checks?: string[];
  findings: RcaFinding[];
}

// --- timeline (change-correlation: deploys/config/alerts on a time axis) ------

export type ChangeKind = "deploy" | "config" | "alert" | "scale" | "incident" | "other";

export interface ChangeEvent {
  time: string; // ISO
  title: string;
  kind?: ChangeKind;
  detail?: string;
  /** e.g. version, service, user. */
  tags?: string[];
  /** The change that lines up with the incident — the smoking gun. */
  correlated?: boolean;
}

export interface TimelinePayload {
  from: string; // ISO window start
  to: string;   // ISO window end
  events: ChangeEvent[];
}

// --- cost / cardinality (what drives spend; the "headroom costs money" loop) --

export interface CostDriver {
  name: string;
  /** Active series (cardinality). */
  series?: number;
  /** Cost value, if known. */
  value?: number;
  unit?: string;
  /** Share of the total (0–100). */
  pct?: number;
  /** e.g. "high-churn label: pod name". */
  note?: string;
}

export interface CostPayload {
  /** Headline total — "1.24M active series" or "$4,200 / mo". */
  total?: { label: string; value: number; unit?: string };
  /** Ranked cost / cardinality drivers. */
  drivers: CostDriver[];
  /** The trade-off that makes "adding headroom costs money" explicit. */
  headroom?: { label: string; detail: string };
}

// --- Chrome (shared across every panel type) ---------------------------------

export interface InsightCellAction {
  label: string;
  /**
   * link    -> opens `url` via the host
   * refresh -> re-runs render_insight_cell with the same payload (reproduce)
   * ask     -> sends `text` to the agent as the next question (select-and-ask)
   *
   * Deliberately no kind that calls an arbitrary MCP tool: the cell is
   * structurally read-only (the tool is annotated ReadOnlyHint). Writes go
   * through the agent — use an "ask" action whose text requests them.
   */
  kind: "link" | "refresh" | "ask";
  url?: string;
  text?: string;
  primary?: boolean;
  /** "trend" | "external" — render as an icon-only button instead of a labelled pill. */
  icon?: string;
}

export interface InsightCellCallout {
  tone: "warn" | "crit" | "info";
  title: string;
  body: string;
}

/** Layer A observability metadata — the "insight" in insight cell. */
export interface InsightCellMeta {
  question: string;
  verdict: string;
  confidence: "low" | "medium" | "high";
  timeRange: { from: string; to: string };
  // Agent-declared, not verified: the server repackages what the agent
  // gathered; renderedBy names who rendered the cell, not who produced the data.
  attestation: { asOf: string; live: boolean };
  provenance: { renderedBy: string; datasource: string; orgId?: number; rbacScope?: string };
  query: Array<{ ref: string; expr: string; datasourceUid: string }>;
  // "agent-supplied": a query + datasource were declared for the data (should
  // be real; unverifiable). "synthesized": no declaration — sample or
  // synthesis-view content assembled by the agent.
  dataMode: "agent-supplied" | "synthesized";
}

// --- The cell ----------------------------------------------------------------

export interface InsightCell {
  renderHint: RenderHint;
  /** Populated for timeseries / stat / bar / table. */
  frames?: DataFrame[];
  /** Populated for logs. */
  logs?: LogLine[];
  /** Populated for trace. */
  trace?: TracePayload;
  /** Populated for worklist. */
  worklist?: WorklistItem[];
  /** Populated for rca. */
  rca?: RcaPayload;
  /** Populated for timeline. */
  timeline?: TimelinePayload;
  /** Populated for cost. */
  cost?: CostPayload;
  /** Verdict caption shown above the panel (the agent's answer). */
  callout?: InsightCellCallout;
  actions: InsightCellAction[];
  meta: InsightCellMeta;
}
