// Data layer. getCell(spec) returns a fully-built InsightCell for any panel
// type. It uses LIVE Grafana Cloud data when GRAFANA_URL + GRAFANA_TOKEN +
// GRAFANA_PROM_DS_UID are set (timeseries + stat today), and realistic MOCK data
// otherwise. meta.dataMode records which path ran, so the cell never lies about
// being real.
//
// Live wiring status:
//   timeseries -> Prometheus range query    (live)
//   stat       -> Prometheus instant query  (live)
//   bar/table  -> Prometheus instant vector (live)
//   logs       -> Loki query_range          (live)
//   trace      -> mock (next: Tempo /api/traces/{id})
// Formatting (Backend B / IC-9): values render through @grafana/data's
// getDisplayProcessor in the renderer (src/format.ts). Agent-supplied unit /
// decimals / thresholds / mappings drive it via renderHint.

import type {
  DataFrame,
  Field,
  InsightCell,
  InsightCellMeta,
  LogLevel,
  LogLine,
  PanelType,
  Threshold,
  TraceSpan,
  ValueMapping,
  WorklistItem,
  RcaFinding,
  RcaPayload,
  RuleDiffChange,
  RuleDiffPayload,
  InsightCellAction,
  ChangeEvent,
  ChangeKind,
  TimelinePayload,
  CostDriver,
  CostPayload,
} from "./schema.js";

// Browser-safe env access (process is undefined in the iframe bundle).
const env: Record<string, string | undefined> =
  typeof process !== "undefined" && process.env ? process.env : {};
const GRAFANA_URL = env.GRAFANA_URL?.replace(/\/$/, "");
const GRAFANA_TOKEN = env.GRAFANA_TOKEN;
const PROM_DS_UID = env.GRAFANA_PROM_DS_UID;
const LOKI_DS_UID = env.GRAFANA_LOKI_DS_UID ?? "grafanacloud-logs";
const ORG_ID = env.GRAFANA_ORG_ID ? Number(env.GRAFANA_ORG_ID) : undefined;

export interface RenderSpec {
  panel: PanelType;
  title?: string;
  query?: string;
  service?: string;
  rangeHours?: number;
  datasourceUid?: string;
  /** Agent-authored one-line verdict shown as the insight title. */
  verdict?: string;
  /** Agent-authored detailed explanation shown under the insight title. */
  insight?: string;
  /** Field config (Backend B): Grafana unit id, decimals, thresholds, mappings. */
  unit?: string;
  decimals?: number;
  thresholds?: Threshold[];
  mappings?: ValueMapping[];
  /** For panel="bullet": target/SLO marker and optional axis max. */
  target?: number;
  max?: number;
  /** For panel="worklist": the agent's synthesized, ranked findings. */
  items?: WorklistItem[];
  /** For panel="rca": the agent's (or Sift's) investigation result. */
  rootCause?: { title: string; confidence: "low" | "medium" | "high"; detail?: string };
  checks?: string[];
  findings?: RcaFinding[];
  /** For panel="rulediff": the proposed alert-rule fix. */
  ruleTitle?: string;
  ruleUid?: string;
  ruleSummary?: string;
  changes?: RuleDiffChange[];
  /** Full provisioning payload to PUT on apply. */
  proposedRule?: Record<string, unknown>;
  /** For panel="timeline": change events to correlate against an incident. */
  events?: ChangeEvent[];
  /** For panel="cost": ranked drivers + headline total + headroom trade-off. */
  drivers?: CostDriver[];
  costTotal?: { label: string; value: number; unit?: string };
  headroom?: { label: string; detail: string };
}

export function isLiveConfigured(): boolean {
  return Boolean(GRAFANA_URL && GRAFANA_TOKEN && PROM_DS_UID);
}

const PROM_PANELS: PanelType[] = ["timeseries", "stat", "bar", "table", "bullet"];

export async function getCell(spec: RenderSpec): Promise<InsightCell> {
  const base = Boolean(GRAFANA_URL && GRAFANA_TOKEN);

  // Worklist: agent-supplied items win; else live triage from Grafana Alerting; else mock.
  if (spec.panel === "worklist") {
    let cell: InsightCell;
    if (spec.items?.length) {
      cell = buildWorklist(spec, spec.items, {
        live: base,
        expr: "agent-synthesized findings",
        dsUid: base ? "agent · Grafana Alerting" : undefined,
      });
    } else if (base) {
      try { cell = await liveTriage(spec); }
      catch (err) {
        cell = mockWorklist(spec);
        cell.callout = { tone: "warn", title: "Showing sample triage — live alerts unavailable", body: `${(err as Error).message}` };
      }
    } else {
      cell = mockWorklist(spec);
    }
    applyAgentNarrative(cell, spec);
    if (cell.worklist) cell.worklist = cell.worklist.map(withDefaultActions);
    return cell;
  }

  // RCA / Sift investigation: agent-supplied findings, else a mock investigation.
  if (spec.panel === "rca") {
    const cell = (spec.findings?.length || spec.rootCause)
      ? buildRca(spec, { rootCause: spec.rootCause, checks: spec.checks, findings: spec.findings ?? [] }, base)
      : mockRca(spec);
    applyAgentNarrative(cell, spec);
    return cell;
  }

  // Rule diff: agent-supplied before/after change, else a mock HighSystemLoad fix.
  if (spec.panel === "rulediff") {
    const cell = spec.changes?.length
      ? buildRuleDiff(spec, {
          ruleTitle: spec.ruleTitle ?? spec.title ?? "Alert rule",
          ruleUid: spec.ruleUid,
          summary: spec.ruleSummary,
          changes: spec.changes,
          proposed: spec.proposedRule,
        }, base)
      : mockRuleDiff(spec);
    applyAgentNarrative(cell, spec);
    return cell;
  }

  // Change-correlation timeline: agent events > live annotations > mock.
  if (spec.panel === "timeline") {
    let cell: InsightCell;
    if (spec.events?.length) {
      cell = buildTimeline(spec, spec.events, { live: base, expr: "agent-supplied change events", dsUid: base ? "agent · Grafana annotations" : undefined });
    } else if (base) {
      try { cell = await liveTimeline(spec); }
      catch (err) {
        cell = mockTimeline(spec);
        cell.callout = { tone: "warn", title: "Showing sample timeline — annotations unavailable", body: `${(err as Error).message}` };
      }
    } else {
      cell = mockTimeline(spec);
    }
    applyAgentNarrative(cell, spec);
    return cell;
  }

  // Cost / cardinality: agent drivers > live cardinality query > mock.
  if (spec.panel === "cost") {
    let cell: InsightCell;
    if (spec.drivers?.length) {
      cell = buildCost(spec, { total: spec.costTotal, drivers: spec.drivers, headroom: spec.headroom }, base);
    } else if (base && !!PROM_DS_UID) {
      try { cell = await liveCost(spec); }
      catch (err) {
        cell = mockCost(spec);
        cell.callout = { tone: "warn", title: "Showing sample cost — cardinality query failed", body: `${(err as Error).message}` };
      }
    } else {
      cell = mockCost(spec);
    }
    applyAgentNarrative(cell, spec);
    return cell;
  }

  const canLive = Boolean(spec.query) && (
    (PROM_PANELS.includes(spec.panel) && base && !!PROM_DS_UID) ||
    (spec.panel === "logs" && base && !!LOKI_DS_UID)
  );
  let cell: InsightCell;
  if (canLive) {
    try {
      cell = await liveCell(spec);
    } catch (err) {
      cell = mockCell(spec);
      cell.callout = {
        tone: "warn",
        title: "Showing sample data — live query failed",
        body: `${(err as Error).message}. Check GRAFANA_URL / GRAFANA_TOKEN / GRAFANA_PROM_DS_UID and the query.`,
      };
    }
  } else {
    cell = mockCell(spec);
  }
  applyAgentNarrative(cell, spec);
  return cell;
}

/** Let the agent override the insight verdict/description it wrote for this data. */
function applyAgentNarrative(cell: InsightCell, spec: RenderSpec) {
  if (spec.verdict) cell.meta.verdict = spec.verdict;
  if (spec.verdict || spec.insight) {
    cell.callout = {
      tone: cell.callout?.tone ?? "info",
      title: spec.verdict ?? cell.callout?.title ?? cell.meta.verdict,
      body: spec.insight ?? cell.callout?.body ?? "",
    };
  }
  // Agent-supplied field config (Backend B) overrides inferred renderHint.
  if (spec.unit != null) cell.renderHint.unit = spec.unit;
  if (spec.decimals != null) cell.renderHint.decimals = spec.decimals;
  if (spec.target != null) cell.renderHint.target = spec.target;
  if (spec.max != null) cell.renderHint.max = spec.max;
  if (spec.thresholds?.length) cell.renderHint.thresholds = spec.thresholds;
  if (spec.mappings?.length) cell.renderHint.mappings = spec.mappings;
  if (spec.items?.length) cell.worklist = spec.items;
}

interface SeriesStats { min: number; max: number; avg: number; first: number; last: number; }
function seriesStats(values: (number | null)[]): SeriesStats | null {
  const nums = values.filter((v): v is number => v != null && !Number.isNaN(v));
  if (!nums.length) return null;
  return {
    min: Math.min(...nums),
    max: Math.max(...nums),
    avg: nums.reduce((a, b) => a + b, 0) / nums.length,
    first: nums[0],
    last: nums[nums.length - 1],
  };
}
function fmtVal(n: number, unit?: string): string {
  const s = Math.abs(n) >= 100 ? n.toFixed(0) : n.toFixed(2);
  return unit ? `${s}${unit === "percent" ? "%" : " " + unit}` : s;
}

// ---------------------------------------------------------------------------
// Shared metadata builder
// ---------------------------------------------------------------------------

function baseMeta(spec: RenderSpec, opts: { verdict: string; live?: boolean; expr?: string; dsUid?: string }): InsightCellMeta {
  const live = opts.live ?? false;
  const now = new Date();
  const rangeH = spec.rangeHours ?? 1;
  const from = new Date(now.getTime() - rangeH * 3600_000);
  // Use the datasource actually queried (e.g. Loki for logs), not the Prom default.
  const dsUid = opts.dsUid ?? spec.datasourceUid ?? PROM_DS_UID ?? "grafanacloud-prom";
  return {
    question: spec.title ?? `Show me a ${spec.panel}`,
    verdict: opts.verdict,
    confidence: (live ? "high" : "medium") as "high" | "medium",
    timeRange: { from: from.toISOString(), to: now.toISOString() },
    attestation: { asOf: now.toISOString(), live: live },
    provenance: {
      author: "agent:claude-desktop",
      datasource: live ? `${GRAFANA_URL} (${dsUid})` : "sample (no live datasource)",
      orgId: ORG_ID,
      rbacScope: live ? "service-account token scope" : "n/a (mock)",
    },
    query: opts.expr
      ? [{ ref: "A", expr: opts.expr, datasourceUid: dsUid }]
      : [{ ref: "A", expr: spec.query ?? `# sample ${spec.panel}`, datasourceUid: dsUid }],
    dataMode: (live ? "live" : "mock") as "live" | "mock",
  };
}

// ---------------------------------------------------------------------------
// MOCK generators (one per panel type)
// ---------------------------------------------------------------------------

export function mockCell(spec: RenderSpec): InsightCell {
  switch (spec.panel) {
    case "timeseries": return mockTimeseries(spec);
    case "stat": return mockStat(spec);
    case "bar": return mockBar(spec);
    case "table": return mockTable(spec);
    case "logs": return mockLogs(spec);
    case "trace": return mockTrace(spec);
    case "worklist": return mockWorklist(spec);
    case "rca": return mockRca(spec);
    case "rulediff": return mockRuleDiff(spec);
    case "timeline": return mockTimeline(spec);
    case "cost": return mockCost(spec);
    case "bullet": return mockBullet(spec);
  }
}

function mockWorklist(spec: RenderSpec): InsightCell {
  const items: WorklistItem[] = [
    {
      title: "checkout-service — high error rate",
      priority: "critical",
      status: "firing 22m",
      statusTone: "crit",
      why: "5xx at ~17% and climbing, correlated with payment-provider latency. Not an anomaly — needs action now.",
      actions: [
        { label: "Investigate", kind: "tool", tool: "grafana_render", args: { panel: "trace", title: "checkout root cause" }, primary: true },
        { label: "Open alert", kind: "link", url: exploreUrl(spec) },
      ],
    },
    {
      title: "api-gateway — p99 latency SLO",
      priority: "high",
      status: "flapping ·6× / 1h",
      statusTone: "warn",
      why: "Flapping around GC pauses; heap is near limit. This is a resource issue — add headroom, don't silence.",
      actions: [
        { label: "View trend", kind: "tool", tool: "grafana_render", args: { panel: "timeseries", title: "api-gateway p99" } },
        { label: "Silence 1h", kind: "tool", tool: "grafana_render", args: { panel: "worklist" } },
      ],
    },
    {
      title: "loki-ingester — disk usage 85%",
      priority: "medium",
      status: "firing 3h",
      statusTone: "warn",
      why: "Steady growth, ~2 days to full. Not urgent today; plan capacity this week.",
      actions: [{ label: "View trend", kind: "tool", tool: "grafana_render", args: { panel: "timeseries", title: "loki disk" } }],
    },
    {
      title: "frontend — synthetic check failed",
      priority: "low",
      status: "resolved",
      statusTone: "ok",
      why: "Single failed probe, self-recovered in 2m. Likely transient — no action.",
      actions: [],
    },
  ];
  return {
    renderHint: { type: "worklist", title: spec.title ?? "On-call triage — start of shift" },
    worklist: items.map(withDefaultActions),
    callout: {
      tone: "crit",
      title: "4 alerts — 1 critical, 1 flapping that needs headroom",
      body: "Sorted by priority. checkout-service needs action now; api-gateway is flapping from GC pressure (add headroom, don't silence); the rest can wait.",
    },
    actions: [{ label: "Refresh", kind: "refresh" }],
    meta: baseMeta(spec, { verdict: "1 critical, 1 flapping (resource), 2 low — checkout needs action now." }),
  };
}

// --- RCA / Sift investigation ------------------------------------------------

function buildRca(spec: RenderSpec, rca: RcaPayload, live: boolean): InsightCell {
  const verdict = rca.rootCause ? `Root cause: ${rca.rootCause.title}` : `${rca.findings.length} findings`;
  const meta = baseMeta(spec, {
    verdict, live,
    expr: "Sift / find_slow_requests / find_error_pattern_logs / get_annotations",
    dsUid: "Grafana Sift",
  });
  meta.resultInfo = `${rca.findings.length} findings${rca.checks?.length ? ` · ${rca.checks.length} checks` : ""}`;
  return {
    renderHint: { type: "rca", title: spec.title ?? "Root-cause investigation" },
    rca,
    actions: [
      { label: "Open in Grafana", kind: "link", icon: "external", url: `${GRAFANA_URL ?? "https://your-stack.grafana.net"}/a/grafana-ml-app/investigations` },
      { label: "Refresh", kind: "refresh" },
    ],
    meta,
  };
}

function mockRca(spec: RenderSpec): InsightCell {
  const rca: RcaPayload = {
    rootCause: {
      title: "Upstream payment-provider degradation",
      confidence: "high",
      detail: "checkout-service errors and latency track a degraded payment-provider dependency that started ~25 min ago. No deploy in the window; 12 other traces show the same pattern.",
    },
    checks: ["error logs", "slow requests", "recent deploys", "dependency health", "traces"],
    findings: [
      { kind: "error pattern", severity: "crit", title: "Repeated payment-provider timeouts", detail: "17 errors/min, all 'upstream timeout after 2 retries' on the payment-provider call.", evidence: 'checkout failed order_id=8842 reason=payment_unavailable' },
      { kind: "slow request", severity: "warn", title: "POST /checkout p95 = 820ms", detail: "The payment-provider span accounts for 720ms of an 842ms trace.", evidence: "trace a1b2c9f · payment-provider 720ms · retry.count=2" },
      { kind: "recent change", severity: "ok", title: "No deploys in the window", detail: "Last checkout-service deploy was 3 days ago (v2.4.1) — not a factor.", evidence: "annotations: none in last 1h" },
      { kind: "correlation", severity: "warn", title: "12 other traces share the pattern", detail: "The same payment-provider timeout appears across the checkout path, not an isolated request.", evidence: "12/14 sampled traces" },
    ],
  };
  return buildRca(spec, rca, false);
}

// --- rulediff: propose an alert-rule fix, diff it, apply via provisioning -----

function buildRuleDiff(spec: RenderSpec, rd: RuleDiffPayload, live: boolean): InsightCell {
  const verdict = rd.applied ? `Applied: ${rd.ruleTitle}` : `Proposed fix: ${rd.ruleTitle}`;
  const meta = baseMeta(spec, {
    verdict, live,
    expr: rd.ruleUid
      ? `PUT /api/v1/provisioning/alert-rules/${rd.ruleUid}`
      : "Grafana Alerting provisioning API",
    dsUid: "Grafana Alerting",
  });
  meta.resultInfo = `${rd.changes.length} change${rd.changes.length === 1 ? "" : "s"}${rd.applied ? " · applied" : ""}`;

  const actions: InsightCellAction[] = [];
  if (!rd.applied) {
    actions.push({
      label: "Apply changes",
      kind: "tool",
      tool: "apply_alert_rule",
      primary: true,
      args: { uid: rd.ruleUid, rule: rd.proposed, ruleTitle: rd.ruleTitle, summary: rd.summary, changes: rd.changes },
    });
  }
  actions.push({ label: "Open in Grafana", kind: "link", icon: "external", url: `${GRAFANA_URL ?? "https://your-stack.grafana.net"}/alerting/list` });
  actions.push({ label: "Refresh", kind: "refresh" });

  return {
    renderHint: { type: "rulediff", title: spec.title ?? rd.ruleTitle ?? "Alert rule fix" },
    rulediff: rd,
    callout: rd.applied
      ? { tone: "info", title: "Applied via the provisioning API", body: `The updated rule is now live${live ? "" : " (demo — no stack configured, nothing was written)"}. New evaluations use the revised condition.` }
      : { tone: "warn", title: rd.summary ?? "Proposed rule change", body: "Review the before/after below, then apply to write it via the Grafana Alerting provisioning API." },
    actions,
    meta,
  };
}

function mockRuleDiff(spec: RenderSpec): InsightCell {
  const rd: RuleDiffPayload = {
    ruleTitle: spec.ruleTitle ?? spec.title ?? "HighSystemLoad",
    ruleUid: spec.ruleUid,
    summary: "Normalize load by core count and add a 5m grace period — stops it flapping on healthy multi-core hosts.",
    changes: [
      {
        field: "Condition",
        before: "avg(node_load1) > 4",
        after: "avg(node_load1) / count(count(node_cpu_seconds_total) by (cpu)) > 1",
        rationale: "Raw load ignores core count: an 8-core host fires at load 4 while only ~50% busy. Per-core load is the real saturation signal.",
      },
      {
        field: "For (grace period)",
        before: "0s",
        after: "5m",
        rationale: "The rule fires on momentary spikes and clears seconds later — that's the flapping. A 5m 'for' only pages on sustained pressure.",
      },
      {
        field: "No-data / error state",
        before: "Alerting",
        after: "OK",
        rationale: "A scrape gap shouldn't page on-call as a load alert.",
      },
    ],
  };
  return buildRuleDiff(spec, rd, false);
}

/** Write a proposed rule change via the provisioning API, then re-render as applied. */
export async function applyAlertRule(args: {
  uid?: string;
  rule?: Record<string, unknown>;
  ruleTitle?: string;
  summary?: string;
  changes?: RuleDiffChange[];
}): Promise<InsightCell> {
  const live = Boolean(GRAFANA_URL && GRAFANA_TOKEN);
  const spec: RenderSpec = { panel: "rulediff", title: args.ruleTitle, ruleTitle: args.ruleTitle, ruleUid: args.uid, ruleSummary: args.summary, changes: args.changes };
  const rd: RuleDiffPayload = {
    ruleTitle: args.ruleTitle ?? "Alert rule",
    ruleUid: args.uid,
    summary: args.summary,
    changes: args.changes ?? [],
    proposed: args.rule,
    applied: false,
  };

  let error = "";
  if (live && args.uid && args.rule) {
    try {
      await provisionPut(`/api/v1/provisioning/alert-rules/${args.uid}`, args.rule);
      rd.applied = true;
    } catch (e) { error = (e as Error).message; }
  } else {
    // Demo: no creds or no proposed payload — mark applied so the flow is visible.
    rd.applied = true;
  }

  const cell = buildRuleDiff(spec, rd, live);
  if (error) {
    cell.callout = { tone: "crit", title: "Apply failed", body: `${error}. Check the rule UID and that the token has alerting.provisioning:write.` };
    cell.meta.verdict = `Apply failed: ${rd.ruleTitle}`;
  }
  return cell;
}

async function provisionPut(path: string, body: unknown): Promise<unknown> {
  const res = await fetch(`${GRAFANA_URL}${path}`, {
    method: "PUT",
    headers: {
      Authorization: `Bearer ${GRAFANA_TOKEN}`,
      "Content-Type": "application/json",
      Accept: "application/json",
      "X-Disable-Provenance": "true",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`Grafana ${res.status} ${res.statusText}: ${(await res.text()).slice(0, 200)}`);
  return res.json().catch(() => ({}));
}

// --- timeline: change-correlation (deploys / config / alerts) ----------------

function buildTimeline(spec: RenderSpec, events: ChangeEvent[], opts: { live?: boolean; expr?: string; dsUid?: string }): InsightCell {
  const sorted = [...events].sort((a, b) => a.time.localeCompare(b.time));
  const times = sorted.map((e) => new Date(e.time).getTime()).filter((t) => isFinite(t));
  const now = Date.now();
  let from: number, to: number;
  if (times.length) {
    from = Math.min(...times); to = Math.max(...times);
    if (to - from < 5 * 60_000) { from -= 30 * 60_000; to += 30 * 60_000; }
    else { const pad = (to - from) * 0.08; from -= pad; to += pad; }
  } else {
    from = now - (spec.rangeHours ?? 6) * 3600_000; to = now;
  }
  const payload: TimelinePayload = { from: new Date(from).toISOString(), to: new Date(to).toISOString(), events: sorted };

  const correlated = sorted.find((e) => e.correlated);
  const verdict = correlated ? `Correlated with: ${correlated.title}` : `${sorted.length} change events in the window`;
  const meta = baseMeta(spec, {
    verdict, live: !!opts.live,
    expr: opts.expr ?? "GET /api/annotations",
    dsUid: opts.dsUid ?? "Grafana annotations",
  });
  meta.resultInfo = `${sorted.length} events`;
  return {
    renderHint: { type: "timeline", title: spec.title ?? "Change-correlation timeline" },
    timeline: payload,
    callout: correlated
      ? { tone: "warn", title: `Likely cause: ${correlated.title}`, body: correlated.detail ?? "This change lines up with the incident window — investigate or roll back before adding capacity." }
      : { tone: "info", title: `${sorted.length} changes in the window`, body: "No single change lines up with the incident. If the metric is oscillating with nothing here, it's a resource/anomaly call — not a deploy to roll back." },
    actions: [
      { label: "Open in Grafana", kind: "link", icon: "external", url: `${GRAFANA_URL ?? "https://your-stack.grafana.net"}/alerting/list` },
      { label: "Refresh", kind: "refresh" },
    ],
    meta,
  };
}

function mockTimeline(spec: RenderSpec): InsightCell {
  const now = Date.now();
  const t = (minAgo: number) => new Date(now - minAgo * 60_000).toISOString();
  const events: ChangeEvent[] = [
    { time: t(180), kind: "config", title: "Scaling policy edited", detail: "HPA max replicas 6→4 on checkout-service.", tags: ["checkout-service", "user:ana"] },
    { time: t(95), kind: "deploy", title: "checkout-service v2.4.2 deployed", detail: "Rollout finished ~3 min before latency began climbing — the strongest correlation.", tags: ["checkout-service", "v2.4.2"], correlated: true },
    { time: t(90), kind: "alert", title: "HighLatency started firing", detail: "p99 crossed the 1s SLO.", tags: ["HighLatency", "→Alerting"] },
    { time: t(30), kind: "scale", title: "api-gateway scaled 4→6", detail: "Auto-scaler added replicas under load.", tags: ["api-gateway"] },
    { time: t(5), kind: "alert", title: "HighLatency still firing", detail: "No recovery after the scale-up — points at the deploy, not capacity.", tags: ["HighLatency"] },
  ];
  return buildTimeline(spec, events, { live: false });
}

async function liveTimeline(spec: RenderSpec): Promise<InsightCell> {
  const rangeH = spec.rangeHours ?? 6;
  const fromMs = Date.now() - rangeH * 3600_000;
  const toMs = Date.now();
  const events: ChangeEvent[] = [];
  const sources: string[] = [];

  // Source 1: annotations (deploys / config / dashboard alert annotations).
  try {
    const anns: any = await promFetch(`${GRAFANA_URL}/api/annotations?from=${fromMs}&to=${toMs}&limit=100`);
    const list: any[] = Array.isArray(anns) ? anns : [];
    if (list.length) { events.push(...list.map(annToEvent)); sources.push(`${list.length} annotation${list.length > 1 ? "s" : ""}`); }
  } catch { /* optional source */ }

  // Source 2: alert state changes — firing alerts' activeAt (when they started).
  try {
    const json = await promFetch(`${GRAFANA_URL}/api/prometheus/grafana/api/v1/alerts`);
    const alerts: any[] = json?.data?.alerts ?? [];
    const fired = alerts
      .filter((a) => /alerting|firing/i.test(a.state ?? "") && a.activeAt && new Date(a.activeAt).getTime() >= fromMs)
      .map(alertToTimelineEvent);
    if (fired.length) { events.push(...dedupeEvents(fired)); sources.push(`${fired.length} firing alert${fired.length > 1 ? "s" : ""}`); }
  } catch { /* optional source */ }

  const deduped = dedupeEvents(events);
  const expr = `GET /api/annotations + /api/prometheus/grafana/api/v1/alerts · last ${rangeH}h`;
  const dsUid = "Grafana annotations + Alerting";

  if (!deduped.length) {
    const cell = buildTimeline(spec, [], { live: true, expr, dsUid });
    cell.callout = { tone: "info", title: "No change events in the window", body: `No deploys, config changes, or alert transitions in the last ${rangeH}h. If a metric is oscillating with nothing here, treat it as a resource/anomaly call, not a deploy.` };
    return cell;
  }

  markCorrelatedCluster(deduped);
  const cell = buildTimeline(spec, deduped, { live: true, expr, dsUid });
  cell.meta.resultInfo = `${deduped.length} events · ${sources.join(" + ")}`;
  return cell;
}

/** A firing alert instance -> a timeline event at the moment it started firing. */
function alertToTimelineEvent(a: any): ChangeEvent {
  const L: Record<string, string> = a.labels ?? {};
  const A: Record<string, string> = a.annotations ?? {};
  const name = L.alertname ?? "alert";
  const scope = L.instance ?? L.service ?? "";
  return {
    time: new Date(a.activeAt).toISOString(),
    title: `${name} started firing`,
    kind: "alert",
    detail: A.summary || A.description || (scope ? `on ${scope}` : "→ Alerting"),
    tags: [name, ...(L.severity ? [L.severity] : []), ...(scope ? [scope] : [])],
  };
}

/** Drop duplicate events (same kind + title within the same minute). */
function dedupeEvents(events: ChangeEvent[]): ChangeEvent[] {
  const seen = new Set<string>();
  const out: ChangeEvent[] = [];
  for (const e of events) {
    const key = `${e.kind}|${e.title}|${e.time.slice(0, 16)}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(e);
  }
  return out;
}

/**
 * Flag the earliest event of the tightest alert cluster as `correlated`: if ≥2
 * alerts fired within 3 minutes, that's likely one incident, not N problems.
 * (The agent can still override via explicit `events`.)
 */
function markCorrelatedCluster(events: ChangeEvent[]): void {
  const alerts = events.filter((e) => e.kind === "alert").sort((a, b) => a.time.localeCompare(b.time));
  if (alerts.length < 2) return;
  const WINDOW = 3 * 60_000;
  let best: ChangeEvent[] = [];
  for (let i = 0; i < alerts.length; i++) {
    const start = new Date(alerts[i].time).getTime();
    const cluster = alerts.filter((e) => { const t = new Date(e.time).getTime(); return t >= start && t - start <= WINDOW; });
    if (cluster.length > best.length) best = cluster;
  }
  if (best.length < 2) return;
  const spanMin = Math.round((new Date(best[best.length - 1].time).getTime() - new Date(best[0].time).getTime()) / 60_000) + 1;
  const names = best.map((e) => e.tags?.[0] ?? e.title).join(", ");
  best[0].correlated = true;
  best[0].detail = `${best.length} alerts fired within ~${spanMin} min — ${names}. That clustering points to one incident, not ${best.length} separate problems.`;
}

function annToEvent(a: any): ChangeEvent {
  const tags: string[] = Array.isArray(a.tags) ? a.tags : [];
  const isAlert = a.type === "alert" || a.alertId != null || a.newState != null;
  const isDeploy = tags.some((t) => /deploy|release|rollout/i.test(t));
  const kind: ChangeKind = isDeploy ? "deploy" : isAlert ? "alert" : tags.some((t) => /config|scale/i.test(t)) ? "config" : "other";
  const stateDelta = a.prevState && a.newState ? `${a.prevState} → ${a.newState}` : a.newState ? `→ ${a.newState}` : "";
  return {
    time: new Date(a.time).toISOString(),
    title: a.text || a.title || (isAlert ? `Alert ${a.newState ?? "state change"}` : "Change event"),
    kind,
    detail: [stateDelta, tags.join(", ")].filter(Boolean).join(" · ") || undefined,
    tags: tags.length ? tags : undefined,
    correlated: /alerting/i.test(a.newState ?? ""),
  };
}

// --- cost / cardinality ------------------------------------------------------

function seriesFmt(n: number): string {
  if (n >= 1e6) return `${(n / 1e6).toFixed(2)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(0)}k`;
  return String(Math.round(n));
}

function buildCost(spec: RenderSpec, cost: CostPayload, live: boolean): InsightCell {
  const drivers = [...cost.drivers].sort((a, b) => (b.series ?? b.value ?? 0) - (a.series ?? a.value ?? 0));
  const totalSeries = cost.total?.value ?? drivers.reduce((s, d) => s + (d.series ?? 0), 0);
  for (const d of drivers) if (d.pct == null && d.series != null && totalSeries) d.pct = (d.series / totalSeries) * 100;
  const top = drivers[0];
  const verdict = cost.total ? cost.total.label : top ? `${top.name} leads cardinality` : `${drivers.length} cost drivers`;
  const meta = baseMeta(spec, {
    verdict, live,
    expr: 'topk(20, count by (__name__)({__name__!=""}))',
    dsUid: spec.datasourceUid ?? PROM_DS_UID ?? "prometheus",
  });
  meta.resultInfo = `${drivers.length} drivers${cost.total ? ` · ${cost.total.label}` : ""}`;
  return {
    renderHint: { type: "cost", title: spec.title ?? "Cost & cardinality" },
    cost: { ...cost, drivers },
    // Headroom (if any) renders inside the panel; the insight below is a summary.
    callout: { tone: "info", title: verdict, body: top ? `${top.name} is the largest contributor${top.pct != null ? ` at ${top.pct.toFixed(0)}% of series` : ""}. Scaling a high-cardinality service multiplies its series — weigh the alert noise you'd silence against the cost you'd add.` : "" },
    actions: [
      { label: "Open in Grafana", kind: "link", icon: "external", url: `${GRAFANA_URL ?? "https://your-stack.grafana.net"}/a/grafana-costmanagementui-app` },
      { label: "Refresh", kind: "refresh" },
    ],
    meta,
  };
}

function mockCost(spec: RenderSpec): InsightCell {
  const cost: CostPayload = {
    total: { label: "1.24M active series", value: 1_240_000, unit: "short" },
    drivers: [
      { name: "http_request_duration_seconds_bucket", series: 428_000, note: "20 le buckets × high route/status cardinality" },
      { name: "container_network_* (per-pod)", series: 214_000, note: "per-pod, per-interface — churns on every restart" },
      { name: "go_gc_duration_seconds", series: 96_000, note: "per-instance quantiles" },
      { name: "node_cpu_seconds_total", series: 62_000, note: "per-cpu × per-mode" },
      { name: "kube_pod_labels", series: 41_000, note: "high-churn label: pod name" },
    ],
    headroom: {
      label: "Adding headroom isn't free",
      detail: "Scaling api-gateway 4→6 replicas adds ~2 instances × ~19k series ≈ +38k series (~3% more). Before adding capacity to quiet a flapping alert, check whether fixing the rule (per-core threshold + a grace period) costs nothing instead.",
    },
  };
  return buildCost(spec, cost, false);
}

async function liveCost(spec: RenderSpec): Promise<InsightCell> {
  const rows = await promInstantVector('topk(20, count by (__name__)({__name__!=""}))', spec.datasourceUid);
  if (!rows.length) throw new Error("cardinality query returned nothing");
  let total: number | null = null;
  try { total = await promInstant('count({__name__!=""})', spec.datasourceUid); } catch { /* optional */ }
  const drivers = rows
    .map((r) => ({ name: r.metric.__name__ ?? seriesName(r.metric), series: Math.round(r.value) }))
    .sort((a, b) => b.series - a.series);
  const cost: CostPayload = {
    total: total != null ? { label: `${seriesFmt(total)} active series`, value: total, unit: "short" } : undefined,
    drivers,
  };
  const cell = buildCost(spec, cost, true);
  const topN = drivers[0];
  const sumTop = drivers.reduce((s, d) => s + d.series, 0);
  cell.callout = {
    tone: "info",
    title: total != null ? `${seriesFmt(total)} active series` : `Top ${drivers.length} metrics by series`,
    body: `${topN.name} is the largest at ${seriesFmt(topN.series)} series${total ? ` (${((topN.series / total) * 100).toFixed(0)}%)` : ""}. The top ${drivers.length} metrics account for ${seriesFmt(sumTop)} series. High-cardinality metrics are where "add headroom" gets expensive — scaling multiplies their series.`,
  };
  return cell;
}

// --- live triage: real firing alerts from Grafana Alerting -------------------

// Every worklist item gets a "View trend" and "Open in Grafana" button by
// default (appended after any agent-supplied actions), so they're always there
// without the agent having to add them.
function withDefaultActions(it: WorklistItem): WorklistItem {
  const actions = [...(it.actions ?? [])];
  const hasTrend = actions.some((a) => a.icon === "trend" || /trend/i.test(a.label));
  const hasLink = actions.some((a) => a.kind === "link");
  // A primary CTA = a labelled pill (tool/ask, not an icon/trend). Ensure one exists.
  const hasPrimary = actions.some((a) => (a.kind === "tool" || a.kind === "ask") && !a.icon && !/trend/i.test(a.label));
  if (!hasPrimary) {
    actions.unshift({
      label: "Investigate",
      kind: "ask",
      text: `Investigate the "${it.title}" alert: correlate the underlying metrics, decide whether it's a resource issue that needs headroom or an anomaly that's safe to silence, and recommend the next step.`,
    });
  }
  if (!hasTrend) {
    actions.push({ label: "View trend", kind: "ask", icon: "trend", text: `Show the trend behind "${it.title}" as a timeseries insight cell.` });
  }
  if (!hasLink) {
    actions.push({ label: "Open in Grafana", kind: "link", icon: "external", url: `${GRAFANA_URL ?? "https://your-stack.grafana.net"}/alerting/list` });
  }
  return { ...it, actions };
}

interface WlOpts { title?: string; verdict?: string; live?: boolean; resultInfo?: string; callout?: InsightCell["callout"]; expr?: string; dsUid?: string; }

function tally(items: WorklistItem[]): Record<string, number> {
  const c: Record<string, number> = { critical: 0, high: 0, medium: 0, low: 0 };
  for (const it of items) c[it.priority ?? "low"] = (c[it.priority ?? "low"] ?? 0) + 1;
  return c;
}

function buildWorklist(spec: RenderSpec, items: WorklistItem[], opts: WlOpts): InsightCell {
  const c = tally(items);
  const verdict = opts.verdict ?? `${c.critical} critical, ${c.high} high, ${c.medium + c.low} lower`;
  const meta = baseMeta(spec, { verdict, live: !!opts.live, expr: opts.expr, dsUid: opts.dsUid });
  if (opts.resultInfo) meta.resultInfo = opts.resultInfo;
  return {
    renderHint: { type: "worklist", title: opts.title ?? spec.title ?? "Worklist" },
    worklist: items,
    callout: opts.callout,
    actions: [{ label: "Refresh", kind: "refresh" }],
    meta,
  };
}

function sinceAge(iso?: string): string {
  if (!iso) return "";
  const ms = Date.now() - new Date(iso).getTime();
  if (!isFinite(ms) || ms < 0) return "";
  const m = Math.floor(ms / 60000);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  return h < 24 ? `${h}h` : `${Math.floor(h / 24)}d`;
}

function alertToItem(a: any): WorklistItem {
  const L: Record<string, string> = a.labels ?? {};
  const A: Record<string, string> = a.annotations ?? {};
  const sev = (L.severity ?? "").toLowerCase();
  const priority = sev === "critical" ? "critical" : sev === "warning" ? "high" : sev === "info" ? "low" : "medium";
  const pending = /pending/i.test(a.state ?? "");
  const age = sinceAge(a.activeAt);
  return {
    title: L.alertname + (L.instance ? ` · ${L.instance}` : L.service ? ` · ${L.service}` : ""),
    priority,
    status: `${pending ? "pending" : "firing"}${age ? ` ${age}` : ""}`,
    statusTone: pending ? "warn" : priority === "critical" ? "crit" : "warn",
    why: A.summary || A.description || "",
    actions: [{ label: "Open in Grafana", kind: "link", url: `${GRAFANA_URL}/alerting/list` }],
  };
}

async function liveTriage(spec: RenderSpec): Promise<InsightCell> {
  const json = await promFetch(`${GRAFANA_URL}/api/prometheus/grafana/api/v1/alerts`);
  const alerts: any[] = json?.data?.alerts ?? [];
  // Grafana calls the firing state "Alerting".
  const active = alerts.filter((a) => /alerting|firing|pending/i.test(a.state ?? ""));
  const ALERTS_API = "GET /api/prometheus/grafana/api/v1/alerts · state=firing";
  const ALERTS_DS = "Grafana Alerting";
  if (!active.length) {
    return buildWorklist(spec, [], {
      title: spec.title ?? "On-call triage — live",
      live: true, expr: ALERTS_API, dsUid: ALERTS_DS,
      resultInfo: `${alerts.length} alert instances evaluated`,
      verdict: "No firing alerts.",
      callout: { tone: "info", title: "No firing alerts", body: "Nothing active right now — you're clear." },
    });
  }
  const items = active.map(alertToItem);
  const c = tally(items);
  return buildWorklist(spec, items, {
    title: spec.title ?? "On-call triage — live",
    live: true, expr: ALERTS_API, dsUid: ALERTS_DS,
    resultInfo: `${active.length} active of ${alerts.length} · live from Grafana Alerting`,
    verdict: `${c.critical} critical, ${c.high} high, ${c.medium + c.low} lower — live from Grafana Alerting.`,
    callout: {
      tone: c.critical ? "crit" : "warn",
      title: `${active.length} active alert${active.length > 1 ? "s" : ""} — ${c.critical} critical`,
      body: "Live from Grafana Alerting, sorted by severity. Ask the agent to correlate these and suggest next steps (the priorities and 'why' can be enriched by the model).",
    },
  });
}

function timeAxis(points: number, stepSec = 60): number[] {
  const now = Math.floor(Date.now() / 1000);
  const start = now - (points - 1) * stepSec;
  return Array.from({ length: points }, (_, i) => start + i * stepSec);
}

function mockTimeseries(spec: RenderSpec): InsightCell {
  const t = timeAxis(60);
  const p95 = t.map((_, i) => 0.18 + (i > 38 ? (i - 38) * 0.04 : 0) + Math.sin(i / 5) * 0.01);
  const p99 = p95.map((v, i) => v + 0.12 + Math.cos(i / 4) * 0.02);
  const frames: DataFrame[] = [
    {
      name: "latency",
      fields: [
        { name: "time", type: "time", values: t },
        { name: "p95", type: "number", values: p95, unit: "s" },
        { name: "p99", type: "number", values: p99, unit: "s" },
      ],
    },
  ];
  return {
    renderHint: {
      type: "timeseries",
      title: spec.title ?? `${spec.service ?? "checkout-service"} request latency`,
      unit: "s",
      thresholds: [{ value: 1.0, color: "warn", label: "SLO" }],
    },
    frames,
    callout: {
      tone: "warn",
      title: "p99 crossed the 1s SLO ~20 min ago and is still climbing",
      body: "Both p95 and p99 have been rising steadily; p99 is now above the 1s threshold band. No deploys in this window — check upstream dependencies.",
    },
    actions: [
      { label: "Investigate the spike", kind: "tool", tool: "grafana_render", args: { panel: "trace", title: "slowest trace in window" }, primary: true },
      { label: "Open in Grafana", kind: "link", url: exploreUrl(spec) },
      { label: "Refresh", kind: "refresh" },
    ],
    meta: baseMeta(spec, { verdict: "p99 latency crossed the 1s SLO and is rising; no deploy correlation." }),
  };
}

function mockStat(spec: RenderSpec): InsightCell {
  const spark = [0.2, 0.21, 0.2, 0.22, 0.3, 0.45, 0.6, 0.72, 0.79, 0.82];
  const frames: DataFrame[] = [
    { name: "p95", fields: [
      { name: "time", type: "time", values: timeAxis(spark.length) },
      { name: "value", type: "number", values: spark, unit: "s" },
    ] },
  ];
  return {
    renderHint: {
      type: "stat",
      title: spec.title ?? `${spec.service ?? "checkout-service"} p95 latency`,
      unit: "s",
      thresholds: [{ value: 0.5, color: "warn" }, { value: 1.0, color: "crit" }],
    },
    frames,
    actions: [
      { label: "Show the trend", kind: "tool", tool: "grafana_render", args: { panel: "timeseries", title: "latency trend" }, primary: true },
      { label: "Refresh", kind: "refresh" },
    ],
    meta: baseMeta(spec, { verdict: "p95 latency is 0.82s, 4x the baseline of ~0.20s." }),
  };
}

function mockBullet(spec: RenderSpec): InsightCell {
  const frames: DataFrame[] = [
    { name: "p95", fields: [{ name: "value", type: "number", values: [0.82], unit: "s" }] },
  ];
  return {
    renderHint: {
      type: "bullet",
      title: spec.title ?? `${spec.service ?? "checkout-service"} p95 latency vs SLO`,
      unit: "s",
      target: 1.0,
      max: 1.2,
      thresholds: [{ value: 0.5, color: "warn" }, { value: 1.0, color: "crit" }],
    },
    frames,
    callout: { tone: "warn", title: "p95 is 0.82s, inside the warn band and approaching the 1s SLO", body: "Below the 1s target for now, but past the 0.5s warn threshold and trending up. A compact bullet shows the measure against the SLO and the qualitative bands in one row." },
    actions: [
      { label: "Show the trend", kind: "tool", tool: "grafana_render", args: { panel: "timeseries", title: "latency trend" }, primary: true },
      { label: "Refresh", kind: "refresh" },
    ],
    meta: baseMeta(spec, { verdict: "p95 latency 0.82s vs 1s SLO — warn band, trending toward breach." }),
  };
}

function mockBar(spec: RenderSpec): InsightCell {
  const frames: DataFrame[] = [
    { name: "errors_by_endpoint", fields: [
      { name: "endpoint", type: "string", values: ["POST /checkout", "POST /payment", "GET /cart", "POST /order", "GET /catalog", "GET /health"] },
      { name: "errors", type: "number", values: [412, 388, 96, 74, 21, 2], unit: "short" },
    ] },
  ];
  return {
    renderHint: { type: "bar", title: spec.title ?? "Top endpoints by 5xx errors (last 1h)", unit: "short", sort: "desc" },
    frames,
    callout: { tone: "crit", title: "Errors concentrate on the checkout path", body: "POST /checkout and POST /payment account for ~80% of all 5xx errors, consistent with the payment-provider degradation." },
    actions: [
      { label: "Drill into POST /checkout", kind: "tool", tool: "grafana_render", args: { panel: "logs", title: "errors for POST /checkout" }, primary: true },
      { label: "Refresh", kind: "refresh" },
    ],
    meta: baseMeta(spec, { verdict: "5xx errors are dominated by the checkout and payment endpoints." }),
  };
}

function mockTable(spec: RenderSpec): InsightCell {
  const frames: DataFrame[] = [
    { name: "endpoints", fields: [
      { name: "Endpoint", type: "string", values: ["POST /checkout", "POST /payment", "GET /cart", "POST /order", "GET /catalog"] },
      { name: "Req/s", type: "number", values: [0.24, 0.19, 0.62, 0.16, 0.71], unit: "req/s" },
      { name: "p95", type: "number", values: [820, 780, 140, 210, 90], unit: "ms" },
      { name: "Error %", type: "number", values: [17.2, 15.8, 0.1, 4.3, 0.0], unit: "percent" },
    ] },
  ];
  return {
    renderHint: { type: "table", title: spec.title ?? "Endpoint health (last 1h)" },
    frames,
    actions: [
      { label: "Open in Grafana", kind: "link", url: exploreUrl(spec), primary: true },
      { label: "Refresh", kind: "refresh" },
    ],
    meta: baseMeta(spec, { verdict: "checkout and payment endpoints show elevated p95 and error rate." }),
  };
}

function mockLogs(spec: RenderSpec): InsightCell {
  const now = Date.now();
  const raw: Array<[number, LogLevel, string]> = [
    [0, "info", 'started request path=/checkout method=POST'],
    [120, "warn", "payment-provider latency high: 712ms (threshold 300ms)"],
    [240, "error", "payment-provider call failed: upstream timeout after 2 retries"],
    [255, "error", "checkout failed order_id=8842 reason=payment_unavailable"],
    [900, "info", "retry scheduled for order_id=8842 in 5s"],
    [1500, "warn", "circuit breaker half-open for payment-provider"],
    [2100, "info", "health check ok deps=3/4"],
    [2600, "error", "payment-provider call failed: upstream timeout after 2 retries"],
    [3200, "debug", "cache hit ratio=0.94 keys=1204"],
    [3800, "info", "started request path=/cart method=GET"],
  ];
  const logs = raw.map(([ms, level, line]) => ({
    time: new Date(now - (4000 - ms)).toISOString(),
    level,
    line,
    labels: { service: spec.service ?? "checkout-service", level },
  }));
  return {
    renderHint: { type: "logs", title: spec.title ?? `${spec.service ?? "checkout-service"} logs (last 5 min)` },
    logs,
    callout: { tone: "crit", title: "3 payment-provider timeout errors in the last 5 minutes", body: "All errors trace to upstream timeouts on the payment-provider call, each after 2 retries. The circuit breaker has tripped to half-open." },
    actions: [
      { label: "See the failing trace", kind: "tool", tool: "grafana_render", args: { panel: "trace", title: "failed checkout trace" }, primary: true },
      { label: "Refresh", kind: "refresh" },
    ],
    meta: baseMeta(spec, { verdict: "Logs show repeated payment-provider timeouts driving checkout failures." }),
  };
}

function mockTrace(spec: RenderSpec): InsightCell {
  const spans: TraceSpan[] = [
    { id: "1", name: "POST /checkout", service: "api-gateway", startMs: 0, durationMs: 842, status: "ok" },
    { id: "2", parentId: "1", name: "checkout", service: "checkout-service", startMs: 8, durationMs: 810, status: "ok" },
    { id: "3", parentId: "2", name: "inventory-check", service: "inventory", startMs: 20, durationMs: 40, status: "ok" },
    { id: "4", parentId: "2", name: "payment-provider call", service: "payment", startMs: 70, durationMs: 720, status: "error", rootCause: true, tags: { "http.url": "api.provider.com/v1/charge", "http.status_code": 200, "retry.count": 2 } },
    { id: "5", parentId: "2", name: "order-write", service: "orders", startMs: 800, durationMs: 28, status: "ok" },
  ];
  return {
    renderHint: { type: "trace", title: spec.title ?? "why was checkout slow? (trace a1b2c9f)" },
    trace: { traceId: "a1b2c9f", durationMs: 842, spans },
    callout: { tone: "crit", title: "Root cause: payment-provider call", body: "This single span accounts for 720ms of the 842ms trace, with 2 retries visible in its tags. Latency to this provider has been climbing for 20 minutes and 12 other traces show the same pattern. No deploys in this window." },
    actions: [
      { label: "View latency trend", kind: "tool", tool: "grafana_render", args: { panel: "timeseries", title: "payment-provider latency" }, primary: true },
      { label: "See affected traces", kind: "tool", tool: "grafana_render", args: { panel: "table", title: "traces with payment-provider spans" } },
      { label: "Refresh", kind: "refresh" },
    ],
    meta: baseMeta(spec, { verdict: "The payment-provider span is the root cause: 720ms of 842ms, 2 retries." }),
  };
}

// ---------------------------------------------------------------------------
// LIVE Grafana (timeseries + stat via Prometheus datasource proxy)
// ---------------------------------------------------------------------------

async function liveCell(spec: RenderSpec): Promise<InsightCell> {
  switch (spec.panel) {
    case "timeseries": return liveTimeseries(spec);
    case "stat": return liveStat(spec);
    case "bullet": { const c = await liveStat(spec); c.renderHint.type = "bullet"; return c; }
    case "bar": return liveBar(spec);
    case "table": return liveTable(spec);
    case "logs": return liveLogs(spec);
    default: return mockCell(spec);
  }
}

async function liveLogs(spec: RenderSpec): Promise<InsightCell> {
  const rangeH = spec.rangeHours ?? 1;
  const startISO = new Date(Date.now() - rangeH * 3600_000).toISOString();
  const endISO = new Date().toISOString();
  const dsUid = spec.datasourceUid ?? LOKI_DS_UID;
  const limit = 100;
  const url = `${GRAFANA_URL}/api/datasources/proxy/uid/${dsUid}/loki/api/v1/query_range` +
    `?query=${encodeURIComponent(spec.query!)}&start=${encodeURIComponent(startISO)}&end=${encodeURIComponent(endISO)}` +
    `&limit=${limit}&direction=backward`;
  const json = await promFetch(url); // same bearer-auth GET helper

  const streams: any[] = json?.data?.result ?? [];
  const lines: LogLine[] = [];
  for (const s of streams) {
    const labels: Record<string, string> = s.stream ?? {};
    for (const [ns, line] of s.values as [string, string][]) {
      lines.push({ time: nsToISO(ns), level: detectLevel(labels, line), line, labels });
    }
  }
  if (!lines.length) throw new Error("query returned no log lines");
  lines.sort((a, b) => (a.time < b.time ? 1 : -1)); // newest first

  const counts = { error: 0, warn: 0, info: 0, debug: 0 } as Record<string, number>;
  for (const l of lines) counts[l.level]++;

  const meta = baseMeta(spec, { verdict: `${lines.length} log lines`, live: true, expr: spec.query, dsUid });
  meta.resultInfo = `${lines.length} lines · ${streams.length} streams · last ${rangeH}h`;

  return {
    renderHint: { type: "logs", title: spec.title ?? spec.query! },
    logs: lines,
    callout: {
      tone: counts.error ? "crit" : "info",
      title: counts.error ? `${counts.error} errors in the last ${rangeH}h` : `${lines.length} log lines in the last ${rangeH}h`,
      body: `Matched ${lines.length} lines across ${streams.length} streams — ${counts.error} error, ${counts.warn} warn, ${counts.info} info, ${counts.debug} debug. Newest first; filter by level above.`,
    },
    actions: [
      { label: "Open in Grafana", kind: "link", url: exploreUrl(spec, dsUid), primary: true },
      { label: "Refresh", kind: "refresh" },
    ],
    meta,
  };
}

/** Loki nanosecond timestamp string -> ISO. */
function nsToISO(ns: string): string {
  try { return new Date(Number(BigInt(ns) / 1_000_000n)).toISOString(); }
  catch { return new Date().toISOString(); }
}

function detectLevel(labels: Record<string, string>, line: string): LogLevel {
  const tag = (labels.level ?? labels.detected_level ?? labels.severity ?? "").toLowerCase();
  if (/err|crit|fatal|panic/.test(tag)) return "error";
  if (/warn/.test(tag)) return "warn";
  if (/debug|trace/.test(tag)) return "debug";
  if (/info/.test(tag)) return "info";
  const s = line.toLowerCase();
  if (/\b(error|err|exception|fail|fatal|panic|critical)\b/.test(s)) return "error";
  if (/\bwarn(ing)?\b/.test(s)) return "warn";
  if (/\bdebug\b/.test(s)) return "debug";
  return "info";
}

interface InstantRow { metric: Record<string, string>; value: number; }

/** Run a Prometheus instant query and return every series (labels + value). */
async function promInstantVector(expr: string, dsUid?: string): Promise<InstantRow[]> {
  const uid = dsUid ?? PROM_DS_UID;
  const url = `${GRAFANA_URL}/api/datasources/proxy/uid/${uid}/api/v1/query?query=${encodeURIComponent(expr)}`;
  const json = await promFetch(url);
  const results: any[] = json?.data?.result ?? [];
  return results.map((r) => ({ metric: r.metric ?? {}, value: Number(r.value?.[1]) }));
}

async function liveTable(spec: RenderSpec): Promise<InsightCell> {
  const all = await promInstantVector(spec.query!, spec.datasourceUid);
  if (!all.length) throw new Error("query returned no series");
  const MAX = 100;
  const rows = all.slice(0, MAX);

  // One column per label key present, plus the numeric Value.
  const labelKeys = Array.from(new Set(rows.flatMap((r) => Object.keys(r.metric)))).sort();
  const fields: Field[] = [];
  if (labelKeys.length) {
    for (const k of labelKeys) fields.push({ name: k, type: "string", values: rows.map((r) => r.metric[k] ?? "") });
  } else {
    fields.push({ name: "series", type: "string", values: rows.map((_, i) => `series ${i + 1}`) });
  }
  const unit = spec.unit ?? (spec.query!.includes("seconds") ? "s" : undefined);
  fields.push({ name: "Value", type: "number", values: rows.map((r) => r.value), unit });

  const vals = rows.map((r) => r.value);
  const meta = baseMeta(spec, {
    verdict: `${all.length} series returned`,
    live: true,
    expr: spec.query,
  });
  meta.resultInfo = `${all.length} rows${all.length > MAX ? ` (showing ${MAX})` : ""} · instant query`;

  return {
    renderHint: { type: "table", title: spec.title ?? spec.query! },
    frames: [{ name: "query", fields }],
    callout: {
      tone: "info",
      title: `${all.length} series returned`,
      body: `Instant query returned ${all.length} series${all.length > MAX ? ` (showing the first ${MAX})` : ""}, values ranging ${fmtVal(Math.min(...vals), unit)}–${fmtVal(Math.max(...vals), unit)}. Sortable by any column.`,
    },
    actions: [
      { label: "Open in Grafana", kind: "link", url: exploreUrl(spec), primary: true },
      { label: "Refresh", kind: "refresh" },
    ],
    meta,
  };
}

async function liveBar(spec: RenderSpec): Promise<InsightCell> {
  const all = await promInstantVector(spec.query!, spec.datasourceUid);
  if (!all.length) throw new Error("query returned no series");
  const MAX = 25;
  const ranked = [...all].sort((a, b) => b.value - a.value).slice(0, MAX);
  const unit = spec.unit ?? (spec.query!.includes("seconds") ? "s" : undefined);

  const fields: Field[] = [
    { name: "series", type: "string", values: ranked.map((r) => short(seriesName(r.metric))) },
    { name: "value", type: "number", values: ranked.map((r) => r.value), unit },
  ];

  const meta = baseMeta(spec, { verdict: `Top ${ranked.length} of ${all.length} series`, live: true, expr: spec.query });
  meta.resultInfo = `${all.length} series${all.length > MAX ? ` (top ${MAX})` : ""} · instant query`;

  const top = ranked[0];
  return {
    renderHint: { type: "bar", title: spec.title ?? spec.query!, unit, sort: "desc" },
    frames: [{ name: "query", fields }],
    callout: {
      tone: "info",
      title: `${short(seriesName(top.metric))} leads at ${fmtVal(top.value, unit)}`,
      body: `Ranked ${all.length} series by current value${all.length > MAX ? ` (showing top ${MAX})` : ""}. Highest is ${short(seriesName(top.metric))} at ${fmtVal(top.value, unit)}; lowest shown is ${fmtVal(ranked[ranked.length - 1].value, unit)}.`,
    },
    actions: [
      { label: "Open in Grafana", kind: "link", url: exploreUrl(spec), primary: true },
      { label: "Refresh", kind: "refresh" },
    ],
    meta,
  };
}

async function liveTimeseries(spec: RenderSpec): Promise<InsightCell> {
  const rangeH = spec.rangeHours ?? 1;
  const end = Math.floor(Date.now() / 1000);
  const start = end - rangeH * 3600;
  const step = Math.max(15, Math.floor((end - start) / 300));
  const json = await promRange(spec.query!, start, end, step, spec.datasourceUid);

  const results: any[] = json?.data?.result ?? [];
  if (!results.length) throw new Error("query returned no series");

  // Union of all timestamps across series.
  const times = Array.from(new Set(results.flatMap((r) => r.values.map((v: any[]) => v[0])))).sort((a, b) => a - b);
  const fields: Field[] = [{ name: "time", type: "time", values: times }];
  for (const r of results.slice(0, 6)) {
    const map = new Map<number, number>(r.values.map((v: any[]) => [v[0], Number(v[1])]));
    fields.push({
      name: seriesName(r.metric),
      type: "number",
      values: times.map((t) => (map.has(t) ? map.get(t)! : null)),
    });
  }

  const unit = spec.unit ?? (spec.query!.includes("seconds") ? "s" : undefined);

  // Data-driven description of the primary (highest-current) series.
  const numFields = fields.filter((f) => f.type === "number");
  const ranked = numFields
    .map((f) => ({ name: f.name, stats: seriesStats(f.values as (number | null)[]) }))
    .filter((x) => x.stats)
    .sort((a, b) => (b.stats!.last) - (a.stats!.last));
  const top = ranked[0];
  let verdict = `${results.length} live series over the last ${rangeH}h.`;
  let body = "";
  if (top) {
    const s = top.stats!;
    const changePct = s.first !== 0 ? ((s.last - s.first) / Math.abs(s.first)) * 100 : 0;
    const dir = changePct >= 1 ? "up" : changePct <= -1 ? "down" : "flat";
    verdict = `${short(top.name)} is ${fmtVal(s.last, unit)} (${dir === "flat" ? "steady" : dir + " " + Math.abs(changePct).toFixed(0) + "%"})`;
    body =
      `Over the last ${rangeH}h, ${short(top.name)} ranged ${fmtVal(s.min, unit)}–${fmtVal(s.max, unit)} ` +
      `(avg ${fmtVal(s.avg, unit)}), currently ${fmtVal(s.last, unit)} — ${dir === "flat" ? "steady versus" : dir + " " + Math.abs(changePct).toFixed(0) + "% from"} the window start (${fmtVal(s.first, unit)}). ` +
      `${results.length} series returned${results.length > 1 ? "; showing the highest-current first" : ""}.`;
  }

  const meta = baseMeta(spec, { verdict, live: true, expr: spec.query });
  meta.resultInfo = `${results.length} series · ${times.length} points · step ${step}s`;

  return {
    renderHint: { type: "timeseries", title: spec.title ?? spec.query!, unit },
    frames: [{ name: "query", fields }],
    callout: { tone: "info", title: verdict, body },
    actions: [
      { label: "Open in Grafana", kind: "link", url: exploreUrl(spec), primary: true },
      { label: "Refresh", kind: "refresh" },
    ],
    meta,
  };
}

async function liveStat(spec: RenderSpec): Promise<InsightCell> {
  const v = await promInstant(spec.query!, spec.datasourceUid);
  if (v == null) throw new Error("query returned no value");
  const unit = spec.unit ?? (spec.query!.includes("seconds") ? "s" : undefined);

  const verdict = `${short(spec.title ?? spec.query!)} is ${fmtVal(v, unit)}`;
  const meta = baseMeta(spec, { verdict, live: true, expr: spec.query });
  meta.resultInfo = "instant query · 1 value";

  return {
    renderHint: { type: "stat", title: spec.title ?? spec.query!, unit },
    frames: [{ name: "stat", fields: [
      { name: "time", type: "time", values: [Math.floor(Date.now() / 1000)] },
      { name: "value", type: "number", values: [v] },
    ] }],
    callout: {
      tone: "info",
      title: verdict,
      body: `Instant value of \`${spec.query}\` is ${fmtVal(v, unit)}, evaluated just now against ${GRAFANA_URL}.`,
    },
    actions: [
      { label: "Open in Grafana", kind: "link", url: exploreUrl(spec), primary: true },
      { label: "Refresh", kind: "refresh" },
    ],
    meta,
  };
}

/** Trim a long PromQL/series name for prose. */
function short(s: string): string {
  return s.length > 48 ? s.slice(0, 47) + "…" : s;
}

function seriesName(metric: Record<string, string>): string {
  if (!metric || !Object.keys(metric).length) return "value";
  return metric.__name__ ?? Object.entries(metric).map(([k, v]) => `${k}=${v}`).join(",");
}

async function promRange(expr: string, start: number, end: number, step: number, dsUid?: string): Promise<any> {
  const uid = dsUid ?? PROM_DS_UID;
  const url = `${GRAFANA_URL}/api/datasources/proxy/uid/${uid}/api/v1/query_range?query=${encodeURIComponent(expr)}&start=${start}&end=${end}&step=${step}`;
  return promFetch(url);
}

async function promInstant(expr: string, dsUid?: string): Promise<number | null> {
  const uid = dsUid ?? PROM_DS_UID;
  const url = `${GRAFANA_URL}/api/datasources/proxy/uid/${uid}/api/v1/query?query=${encodeURIComponent(expr)}`;
  const json = await promFetch(url);
  const val = json?.data?.result?.[0]?.value?.[1];
  return val == null ? null : Number(val);
}

async function promFetch(url: string): Promise<any> {
  const res = await fetch(url, { headers: { Authorization: `Bearer ${GRAFANA_TOKEN}`, Accept: "application/json" } });
  if (!res.ok) throw new Error(`Grafana ${res.status} ${res.statusText}`);
  return res.json();
}

function exploreUrl(spec: RenderSpec, dsUidOverride?: string): string {
  const base = GRAFANA_URL ?? "https://your-stack.grafana.net";
  const uid = dsUidOverride ?? spec.datasourceUid ?? PROM_DS_UID ?? "";
  const left = { datasource: uid, queries: [{ expr: spec.query ?? "up" }], range: { from: "now-1h", to: "now" } };
  return `${base}/explore?left=${encodeURIComponent(JSON.stringify(left))}`;
}
