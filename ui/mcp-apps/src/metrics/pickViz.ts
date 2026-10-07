/**
 * Visualization selection for the metrics app.
 *
 * The viz type is derived from the *shape of the datasource response* — never
 * from a model-supplied argument. `query_prometheus` declares the app, the host
 * renders that tool's own result, and nothing the model writes decides what is
 * drawn. A user-facing toggle inside the app is fine; a `panel:` tool argument
 * would not be.
 */

import type { Unit } from '../viz/format';
import { isHistogramBuckets } from '../viz/Heatmap';
import type { MetricSeries } from '../viz/types';

export type VizKind = 'timeseries' | 'bar' | 'bullet' | 'stat' | 'heatmap' | 'table';

export interface MetricsResult {
  /**
   * Derived structurally — `model.Value` marshals without a `resultType` field,
   * so the parser infers this from the payload shape.
   */
  resultType: 'matrix' | 'vector' | 'scalar' | 'string';
  series: MetricSeries[];
}

/** A parsed tool result: the renderable series plus anything the tool warned about. */
export interface ParsedMetrics extends MetricsResult {
  warnings: string[];
  /** Explore deeplink for this query, when the server could resolve one. */
  exploreUrl?: string;
}

export interface VizChoice {
  kind: VizKind;
  /** Why this was chosen — shown in the app's debug line and asserted in tests. */
  reason: string;
  /**
   * Other viz types legitimate for this data, offered to the user. A user
   * switching render is a UI control; the *default* stays derived.
   *
   * `table` is always present: it can represent any response shape, and is the
   * fallback when a chart hides detail the reader wants.
   */
  alternatives: VizKind[];
}

/**
 * Units with an inherent 0–100% range. A bullet's scale needs `min`/`max` to
 * mean anything and Prometheus supplies no bounds, so it is only an honest
 * default when the unit itself implies them — otherwise `go_goroutines = 1403`
 * renders a bar against an invented maximum.
 */
export function isBoundedUnit(unit: Unit | undefined): boolean {
  return unit === 'percent' || unit === 'percentunit';
}

/**
 * Choose the default viz for a response.
 *
 * `unit` decides only whether a gauge arc would be meaningful — pass
 * `inferUnit(commonMetricName(series))`.
 */
/**
 * Series count past which overlaid lines stop being readable and a heatmap is
 * the better default. Chosen for a chat-width panel, where even a dozen lines
 * crowd; the time series view stays one click away.
 */
const SPAGHETTI_THRESHOLD = 20;

/** Alternatives always end with Table, which suits every response shape. */
function withTable(...kinds: VizKind[]): VizKind[] {
  return [...kinds, 'table'];
}

export function pickViz(result: MetricsResult, unit?: Unit): VizChoice {
  const series = result.series ?? [];
  const seriesCount = series.length;
  const maxPoints = series.reduce((most, s) => Math.max(most, s.points.length), 0);

  if (seriesCount === 0 || maxPoints === 0) {
    return { kind: 'timeseries', reason: 'empty result — render the time series empty state', alternatives: withTable() };
  }

  // Cumulative histogram buckets are not lines: each series counts everything
  // below its bound, so overlaying them hides the distribution a heatmap shows.
  if (maxPoints > 1 && isHistogramBuckets(series)) {
    return {
      kind: 'heatmap',
      reason: `${seriesCount} cumulative histogram buckets over time`,
      alternatives: withTable('timeseries'),
    };
  }

  // Too many series to overlay: colour-by-magnitude beats a dozen crossing lines.
  if (maxPoints > 1 && seriesCount >= SPAGHETTI_THRESHOLD) {
    return {
      kind: 'heatmap',
      reason: `${seriesCount} series over time — too many to overlay`,
      alternatives: withTable('timeseries'),
    };
  }

  // A range query over time is a time series regardless of series count. With a
  // single series, a stat with a sparkline reads the same data equally well.
  if (maxPoints > 1) {
    return {
      kind: 'timeseries',
      reason: `${seriesCount} series × up to ${maxPoints} points over time`,
      alternatives: seriesCount === 1 ? withTable('stat') : withTable('heatmap'),
    };
  }

  // One point per series: an instant query, or a range collapsed to one step.
  const bounded = isBoundedUnit(unit);

  if (seriesCount === 1) {
    return bounded
      ? { kind: 'bullet', reason: `single value in a bounded unit (${unit})`, alternatives: withTable('stat') }
      : {
          kind: 'stat',
          reason: `single value in an unbounded unit (${unit ?? 'unknown'}) — no meaningful scale`,
          alternatives: withTable('bar'),
        };
  }

  // Several bounded values compare better as stacked bullets against a shared
  // scale than as ranked bars, which imply the largest value is the maximum.
  if (bounded) {
    return {
      kind: 'bullet',
      reason: `${seriesCount} values in a bounded unit (${unit}) — compared against a shared scale`,
      alternatives: withTable('bar', 'stat'),
    };
  }

  return {
    kind: 'bar',
    reason: `${seriesCount} series with one value each — ranked comparison`,
    alternatives: withTable('stat'),
  };
}
