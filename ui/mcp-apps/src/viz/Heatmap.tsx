import type { CSSProperties } from 'react';

import { formatValue, type Unit } from './format';
import { SEQUENTIAL_PALETTE } from './theme';
import { parsePrometheusNumber, seriesName, shortSeriesNames, type MetricSeries } from './types';
import type { McpAppColorMode } from '../McpAppShell';
import { useEChart } from './useEChart';
import { getVizStyles } from './Viz.styles';

/**
 * Heatmap of series against time, with value as colour.
 *
 * Answers the case a line chart cannot: a query returning dozens of series is
 * unreadable as overlaid lines, and histogram buckets are not lines at all. Rows
 * are series (or bucket bounds), columns are timestamps, colour is magnitude.
 */

export type HeatmapProps = {
  series: MetricSeries[];
  /** Unit of the values. Histogram rows are counts whatever this says. */
  unit?: Unit;
  /** Unit of histogram bucket bounds (`le`), e.g. seconds for a latency histogram. */
  boundUnit?: Unit;
  decimals?: number;
  colorMode?: McpAppColorMode;
  height?: number;
  /** Rows drawn; any beyond it are left out and the footer says how many. */
  maxRows?: number;
};

/**
 * True when the series are the buckets of *one* Prometheus histogram: each has
 * a numeric `le`, no bound repeats, and the series agree on every other label.
 *
 * Buckets split by another label (`…_bucket` per instance, without
 * `sum by (le)`) are several histograms interleaved. De-accumulating those
 * would subtract one instance's bucket from another's and invent counts.
 */
export function isHistogramBuckets(series: MetricSeries[]): boolean {
  if (series.length < 2) return false;
  const bounds = new Set<string>();
  let shared: string | undefined;
  for (const s of series) {
    const { le, ...rest } = s.labels ?? {};
    if (le === undefined || Number.isNaN(parsePrometheusNumber(le)) || bounds.has(le)) return false;
    bounds.add(le);
    const others = seriesName(rest);
    if (shared === undefined) shared = others;
    else if (others !== shared) return false;
  }
  return true;
}

/**
 * Row labels and values for a histogram.
 *
 * Prometheus buckets are **cumulative** — `le="0.5"` counts everything at or
 * below 0.5, so rendering them raw makes every upper bucket look hottest.
 * Subtracting the bucket below turns them into per-bucket counts, which is what
 * a heatmap should show.
 */
export function deaccumulateBuckets(series: MetricSeries[]): MetricSeries[] {
  const sorted = [...series].sort((a, b) => parsePrometheusNumber(a.labels.le) - parsePrometheusNumber(b.labels.le));
  return sorted.map((s, index) => {
    const below = index > 0 ? new Map(sorted[index - 1].points) : undefined;
    return {
      labels: s.labels,
      points: s.points.map(([at, value]) => {
        const previous = below?.get(at) ?? 0;
        // Counters can go backwards across a restart; never show a negative count.
        return [at, Math.max(0, value - previous)] as [number, number];
      }),
    };
  });
}

/** Escape text for ECharts' HTML tooltip. Row names come from label values. */
function escapeHTML(text: string): string {
  return text.replace(/[&<>"']/g, (ch) => `&#${ch.charCodeAt(0)};`);
}

/**
 * Labels for de-accumulated bucket rows, which are sorted by bound. The axis
 * shows each row's upper bound, as Grafana's heatmap does; the tooltip names
 * the range the row actually counts, `(previous, le]`.
 */
function bucketLabels(rows: MetricSeries[], unit: Unit): { axis: string[]; range: string[] } {
  const bounds = rows.map((s) => parsePrometheusNumber(s.labels.le));
  const show = (bound: number) => formatValue(bound, unit).formatted;
  return {
    // The catch-all bucket keeps Prometheus' own spelling rather than "∞".
    axis: bounds.map((bound) => (Number.isFinite(bound) ? show(bound) : '+Inf')),
    range: bounds.map((bound, index) => {
      const previous = bounds[index - 1];
      if (previous === undefined) return Number.isFinite(bound) ? `≤ ${show(bound)}` : 'all observations';
      if (!Number.isFinite(bound)) return `> ${show(previous)}`;
      return `${show(previous)} – ${show(bound)}`;
    }),
  };
}

export function Heatmap({
  series,
  unit = 'none',
  boundUnit = 'none',
  decimals,
  colorMode = 'light',
  height = 280,
  maxRows = 40,
}: HeatmapProps) {
  const styles = getVizStyles();
  const buckets = isHistogramBuckets(series);
  // Histogram rows are ordered by bound and de-cumulated; anything else keeps
  // the order the datasource returned.
  const allRows = buckets ? deaccumulateBuckets(series) : series;
  const rows = allRows.slice(0, maxRows);
  const labels = buckets ? bucketLabels(rows, boundUnit) : undefined;
  const rowLabels = labels ? labels.axis : shortSeriesNames(rows);
  const rowTooltips = labels ? labels.range : rowLabels;
  // A bucket count is a count, whatever the bucket bound is measured in.
  const valueUnit: Unit = buckets ? 'short' : unit;

  const stamps = [...new Set(rows.flatMap((s) => s.points.map(([at]) => at)))].sort((a, b) => a - b);
  const columnIndex = new Map(stamps.map((at, index) => [at, index]));

  const cells: Array<[number, number, number]> = [];
  let min = Number.POSITIVE_INFINITY;
  let max = Number.NEGATIVE_INFINITY;
  rows.forEach((s, rowIndex) => {
    for (const [at, value] of s.points) {
      const column = columnIndex.get(at);
      if (column === undefined || !Number.isFinite(value)) continue;
      cells.push([column, rowIndex, value]);
      if (value < min) min = value;
      if (value > max) max = value;
    }
  });
  if (!Number.isFinite(min)) min = 0;
  if (!Number.isFinite(max)) max = 0;

  const timeLabels = stamps.map((at) =>
    new Date(at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  );

  const { containerRef } = useEChart(
    (colors) => ({
      animation: false,
      tooltip: {
        // Keep it inside the chart: the iframe clips anything past its edge.
        confine: true,
        position: 'top',
        // ECharts inserts a formatter's string as HTML, and row names come from
        // Prometheus label values, so every part is escaped.
        formatter: (params: unknown) => {
          const { value } = params as { value: [number, number, number] };
          const [column, row, amount] = value;
          const title = `${timeLabels[column]} · ${rowTooltips[row]}`;
          return `${escapeHTML(title)}<br/>${escapeHTML(formatValue(amount, valueUnit, decimals).formatted)}`;
        },
      },
      grid: { left: 8, right: 8, top: 8, bottom: 48, containLabel: true },
      xAxis: {
        type: 'category',
        data: timeLabels,
        // Dense columns are the norm; drop labels rather than overprint them.
        axisLabel: { hideOverlap: true },
        splitArea: { show: false },
      },
      yAxis: {
        type: 'category',
        data: rowLabels,
        axisLabel: { width: 150, overflow: 'truncate' },
        splitArea: { show: false },
      },
      visualMap: {
        type: 'continuous',
        min,
        max: max > min ? max : min + 1,
        calculable: false,
        orient: 'horizontal',
        left: 'center',
        bottom: 0,
        itemHeight: 80,
        textStyle: { color: colors.muted, fontSize: 10 },
        inRange: { color: [...SEQUENTIAL_PALETTE] },
        // A gradient with no numbers cannot be read; label both ends.
        text: [
          formatValue(max > min ? max : min + 1, valueUnit).formatted,
          formatValue(min, valueUnit).formatted,
        ],
        formatter: (value: number) => formatValue(value, valueUnit).formatted,
      },
      series: [
        {
          type: 'heatmap' as const,
          data: cells,
          // Borderless cells: gaps would read as missing data.
          itemStyle: { borderWidth: 0 },
          emphasis: { itemStyle: { borderColor: colors.foreground, borderWidth: 1 } },
          progressive: 2000,
        },
      ],
    }),
    colorMode,
    [series, unit, boundUnit, decimals, maxRows, rowLabels.join('|')]
  );

  if (!cells.length) {
    return <div className={styles.tableEmpty}>Nothing to plot.</div>;
  }

  return (
    <>
      <div ref={containerRef} className={styles.chart} style={{ '--viz-height': `${height}px` } as CSSProperties} />
      {allRows.length > rows.length && (
        <div className={styles.tableFooter}>
          Showing the first {rows.length} of {allRows.length} {buckets ? 'buckets' : 'series'}. Use Table for the rest.
        </div>
      )}
    </>
  );
}
