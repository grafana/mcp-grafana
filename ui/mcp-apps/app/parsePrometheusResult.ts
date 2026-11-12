/**
 * Parser for `query_prometheus`'s own tool result.
 *
 * Scoped deliberately to this one tool, mirroring `app/parseTraceResult.ts`.
 * Keeping parsing per-tool is the structural guarantee against drifting back
 * into a generic render contract with a `panel:` argument.
 *
 * Wire format note — the Go tool returns `QueryPrometheusResult{Data model.Value}`,
 * and `model.Value` does *not* marshal like the Prometheus HTTP API:
 *
 *   matrix  {"data":[{"metric":{…},"values":[[1760000000,"1.25"],…]}]}
 *   vector  {"data":[{"metric":{…},"value":[1760000000,"1"]}]}
 *   scalar  {"data":[1760000000,"42"]}
 *
 * So there is **no `resultType` field** — matrix and vector are told apart by
 * `values` (plural) versus `value` (singular). Timestamps are float **seconds**,
 * and values are **strings** (including `"NaN"`, `"+Inf"`).
 */

import { z } from 'zod';
import { parsePrometheusNumber, type MetricPoint, type MetricSeries } from '../src/viz/types';
import type { MetricsResult, ParsedMetrics } from '../src/metrics/pickViz';

/** `[timestampSeconds, "value"]` as emitted by `model.SamplePair` / `model.Sample`. */
const samplePair = z.tuple([z.number(), z.string()]);

const labels = z.record(z.string(), z.string());

const matrixEntry = z.object({ metric: labels.optional(), values: z.array(samplePair) });
const vectorEntry = z.object({ metric: labels.optional(), value: samplePair });

const promValue = z.union([
  z.array(matrixEntry),
  z.array(vectorEntry),
  samplePair, // scalar
  z.array(z.unknown()), // tolerate shapes we do not render rather than erroring
]);

const queryPrometheusResult = z.object({
  data: promValue.optional(),
  warnings: z.array(z.string()).optional(),
  hints: z.unknown().optional(),
  /** Explore deeplink, built server-side from the instance's public URL. */
  exploreUrl: z.string().optional(),
});

/** Seconds (possibly fractional) → milliseconds, for `Date` and chart axes. */
function toMillis(seconds: number): number {
  return Math.round(seconds * 1000);
}

function point([seconds, value]: [number, string]): MetricPoint {
  return [toMillis(seconds), parsePrometheusNumber(value)];
}

const EMPTY: ParsedMetrics = { resultType: 'vector', series: [], warnings: [] };

/**
 * Parse a `query_prometheus` result into the shape the viz library consumes.
 * Returns `undefined` only when the payload is not a recognisable result — the
 * caller then shows its "interactive view unavailable" fallback, exactly as the
 * trace app does.
 */
export function parsePrometheusResult(payload: unknown): ParsedMetrics | undefined {
  const parsed = queryPrometheusResult.safeParse(payload);
  if (!parsed.success) return undefined;

  const { data, warnings = [], exploreUrl } = parsed.data;
  if (data === undefined) return { ...EMPTY, warnings, exploreUrl };

  // Scalar: a bare [seconds, "value"] tuple. A string result has the same
  // shape; showing it as NaN would invent a value, so it renders as nothing.
  const scalar = samplePair.safeParse(data);
  if (scalar.success && Number.isNaN(parsePrometheusNumber(scalar.data[1])) && scalar.data[1] !== 'NaN') {
    return { ...EMPTY, resultType: 'string', warnings, exploreUrl };
  }
  if (scalar.success) {
    return {
      resultType: 'scalar',
      series: [{ labels: {}, points: [point(scalar.data)] }],
      warnings,
      exploreUrl,
    };
  }

  if (!Array.isArray(data)) return { ...EMPTY, warnings, exploreUrl };
  if (data.length === 0) return { ...EMPTY, warnings, exploreUrl };

  // Matrix: entries carry `values` (plural).
  const matrix = z.array(matrixEntry).safeParse(data);
  if (matrix.success) {
    const series: MetricSeries[] = matrix.data.map((entry) => ({
      labels: entry.metric ?? {},
      points: entry.values.map(point),
    }));
    return { resultType: 'matrix', series, warnings, exploreUrl };
  }

  // Vector: entries carry `value` (singular).
  const vector = z.array(vectorEntry).safeParse(data);
  if (vector.success) {
    const series: MetricSeries[] = vector.data.map((entry) => ({
      labels: entry.metric ?? {},
      points: [point(entry.value)],
    }));
    return { resultType: 'vector', series, warnings, exploreUrl };
  }

  // A recognised envelope carrying something we do not render (e.g. a string
  // result, or native histograms). Not an error — just nothing to draw.
  return { ...EMPTY, warnings, exploreUrl };
}

export type PayloadChannel = 'structuredContent' | 'text' | 'oversized' | 'none';

/**
 * The server's view budget (`maxMetricsViewBytes`): past it the tool sends no
 * structuredContent, and the text fallback must not draw the result anyway.
 */
export const MAX_VIEW_BYTES = 1 << 20;

/**
 * Pull the tool payload out of a result, whichever channel the host delivered.
 *
 * Hosts differ: `ui/panel-viewer` reads `content`, the trace app reads
 * `structuredContent`, and some hosts strip `structuredContent`, `_meta`, and
 * resource blocks before the iframe sees them, leaving only `text` blocks.
 * Trying the structured channel first and falling back to JSON in a text block
 * works on both without the app needing to know which host it is in.
 *
 * Also reports which channel carried the payload, so a render failure says
 * *why* rather than looking identical to "still waiting".
 */
export function extractToolPayload(response: unknown): { payload: unknown; channel: PayloadChannel } {
  const none = { payload: undefined, channel: 'none' as const };
  if (!response || typeof response !== 'object') return none;
  const result = response as {
    structuredContent?: unknown;
    content?: Array<{ type?: string; text?: string }>;
  };

  if (result.structuredContent && typeof result.structuredContent === 'object') {
    return { payload: result.structuredContent, channel: 'structuredContent' };
  }

  for (const item of result.content ?? []) {
    if (item?.type !== 'text' || typeof item.text !== 'string') continue;
    const text = item.text.trim();
    // Cheap guard so we do not attempt JSON.parse on prose.
    if (!text.startsWith('{') && !text.startsWith('[')) continue;
    if (new TextEncoder().encode(text).length > MAX_VIEW_BYTES) return { payload: undefined, channel: 'oversized' };
    try {
      return { payload: JSON.parse(text), channel: 'text' };
    } catch {
      // Not the payload block; keep looking.
    }
  }

  return none;
}
