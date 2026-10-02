// Backend B (real): format values through Grafana's own field-config pipeline.
//
// Uses @grafana/data's `getDisplayProcessor` — the per-field core of
// `applyFieldOverrides` — so units, decimals, thresholds, and value mappings
// come out exactly as a Grafana panel renders them (e.g. "1.46 MiB", "23.4 ms",
// threshold colors like #F2495C). Runs in the browser/iframe bundle only
// (@grafana/data doesn't import cleanly in Node). This is IC-9: in real
// mcp-grafana the same code lives in the ui/ app.
//
// Cost: ~+760 KB to the embedded bundle. That's the real price of authentic
// Grafana formatting in a standalone MCP-App bundle (mcp-grafana pays it too).

import {
  createTheme,
  getDisplayProcessor,
  FieldType,
  ThresholdsMode,
  MappingType,
} from "@grafana/data";
import type { Threshold, ValueMapping } from "./schema.js";

const theme = createTheme({
  colors: { mode: window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light" },
});

// Map our threshold/mapping color tokens onto Grafana color names.
function mapColor(c?: string): string {
  if (!c) return "text";
  return ({ warn: "orange", crit: "red", ok: "green", neutral: "text" } as Record<string, string>)[c] ?? c;
}

/** Resolve a color token (green/red/orange/tone/hex) to a concrete hex via the theme. */
export function resolveColor(token?: string): string {
  try { return theme.visualization.getColorByName(mapColor(token)); }
  catch { return theme.colors.text.primary; }
}

interface FieldCfg {
  unit?: string;
  decimals?: number;
  thresholds?: Threshold[];
  mappings?: ValueMapping[];
}

function toSteps(ths: Threshold[]) {
  const steps = ths
    .map((t) => ({ value: t.value, color: mapColor(t.color) }))
    .sort((a, b) => a.value - b.value);
  if (!steps.length || steps[0].value !== -Infinity) steps.unshift({ value: -Infinity, color: "green" });
  return steps;
}

function toMappings(ms: ValueMapping[]) {
  const out: any[] = [];
  const valueOpts: Record<string, { text?: string; color?: string }> = {};
  for (const m of ms) {
    if (m.type === "value" && m.value != null) valueOpts[String(m.value)] = { text: m.text, color: mapColor(m.color) };
    if (m.type === "range") out.push({ type: MappingType.RangeToText, options: { from: m.from, to: m.to, result: { text: m.text, color: mapColor(m.color) } } });
  }
  if (Object.keys(valueOpts).length) out.unshift({ type: MappingType.ValueToText, options: valueOpts });
  return out;
}

function buildConfig(cfg: FieldCfg): Record<string, unknown> {
  const config: Record<string, unknown> = {};
  if (cfg.unit) config.unit = cfg.unit;
  if (cfg.decimals != null) config.decimals = cfg.decimals;
  if (cfg.thresholds?.length) {
    config.thresholds = { mode: ThresholdsMode.Absolute, steps: toSteps(cfg.thresholds) };
    config.color = { mode: "thresholds" };
  }
  if (cfg.mappings?.length) config.mappings = toMappings(cfg.mappings);
  return config;
}

// Memoize one DisplayProcessor per distinct field config.
const cache = new Map<string, (v: unknown) => { text: string; prefix?: string; suffix?: string; color?: string }>();
function processor(cfg: FieldCfg) {
  const key = JSON.stringify(cfg);
  let p = cache.get(key);
  if (!p) {
    p = getDisplayProcessor({ field: { type: FieldType.number, config: buildConfig(cfg) } as any, theme }) as any;
    cache.set(key, p!);
  }
  return p!;
}

function join(d: { text: string; prefix?: string; suffix?: string }): string {
  return `${d.prefix ?? ""}${d.text}${d.suffix ?? ""}`;
}

/** Format a single value with a unit/decimals (axis, tooltip, table, bar). */
export function formatValue(value: number | null | undefined, unit?: string, decimals?: number): string {
  if (value == null || Number.isNaN(value)) return "—";
  return join(processor({ unit, decimals })(value));
}

/** Format a value with full field config, returning display text + threshold/mapping color (stat). */
export function displayValue(value: number | null | undefined, cfg: FieldCfg): { text: string; color?: string } {
  if (value == null || Number.isNaN(value)) return { text: "no data" };
  const d = processor(cfg)(value);
  return { text: join(d), color: d.color };
}
