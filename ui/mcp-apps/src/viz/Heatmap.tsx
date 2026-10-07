import type { CSSProperties } from 'react';

import { formatValue, type Unit } from './format';
import { SEQUENTIAL_PALETTE } from './theme';
import { parsePrometheusNumber, shortSeriesNames, type MetricSeries } from './types';
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
  unit?: Unit;
  decimals?: number;
  colorMode?: McpAppColorMode;
  height?: number;
  /** Rows drawn before the rest are dropped, newest buckets first. */
  maxRows?: number;
};

/** True when every series carries an `le` bound — a Prometheus histogram. */
export function isHistogramBuckets(series: MetricSeries[]): boolean {
  return (
    series.length > 1 &&
    series.every((s) => s.labels?.le !== undefined && !Number.isNaN(parsePrometheusNumber(s.labels.le)))
  );
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

/** `le="0.5"` reads as `≤ 500 ms` once the unit is applied. */
function bucketLabel(le: string, unit: Unit): string {
  const bound = parsePrometheusNumber(le);
  // The catch-all bucket keeps Prometheus' own spelling rather than "≤ ∞".
  if (!Number.isFinite(bound)) return '+Inf';
  return `≤ ${formatValue(bound, unit).formatted}`;
}

export function Heatmap({
  series,
  unit = 'none',
  decimals,
  colorMode = 'light',
  height = 280,
  maxRows = 40,
}: HeatmapProps) {
  const styles = getVizStyles();
  const buckets = isHistogramBuckets(series);
  // Histogram rows are ordered by bound and de-cumulated; anything else keeps
  // the order the datasource returned.
  const rows = (buckets ? deaccumulateBuckets(series) : series).slice(0, maxRows);
  const rowLabels = buckets
    ? rows.map((s) => bucketLabel(s.labels.le, unit))
    : shortSeriesNames(rows);
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
        position: 'top',
        formatter: (params: unknown) => {
          const { value } = params as { value: [number, number, number] };
          const [column, row, amount] = value;
          return `${timeLabels[column]} · ${rowLabels[row]}<br/>${formatValue(amount, valueUnit, decimals).formatted}`;
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
    [series, unit, decimals, maxRows, rowLabels.join('|')]
  );

  if (!cells.length) {
    return <div className={styles.tableEmpty}>Nothing to plot.</div>;
  }

  return (
    <div ref={containerRef} className={styles.chart} style={{ '--viz-height': `${height}px` } as CSSProperties} />
  );
}
