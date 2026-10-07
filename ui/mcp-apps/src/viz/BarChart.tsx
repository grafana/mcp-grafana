import type { CSSProperties } from 'react';
import { formatValue, resolveThresholdColor, type Threshold, type Unit } from './format';
import { shortSeriesNames, type MetricSeries } from './types';
import type { McpAppColorMode } from '../McpAppShell';
import { useEChart } from './useEChart';
import { getVizStyles } from './Viz.styles';

export type BarChartProps = {
  /** One value per series — an instant query. */
  series: MetricSeries[];
  unit?: Unit;
  decimals?: number;
  thresholds?: Threshold[];
  colorMode?: McpAppColorMode;
  height?: number;
  /** Cap on bars drawn; the rest are summarised by the caller. */
  limit?: number;
};

/** Last value of a series, or NaN when it has no points. */
function latest(series: MetricSeries): number {
  const point = series.points[series.points.length - 1];
  return point ? point[1] : Number.NaN;
}

export function BarChart({
  series,
  unit = 'none',
  decimals,
  thresholds,
  colorMode = 'light',
  height = 240,
  limit = 25,
}: BarChartProps) {
  const styles = getVizStyles();
  // Ranked descending. Horizontal bars because Prometheus series names are long
  // and would otherwise be unreadable rotated labels.
  const names = shortSeriesNames(series);
  const ranked = series
    .map((s, i) => ({ name: names[i], value: latest(s) }))
    .filter((entry) => Number.isFinite(entry.value))
    .sort((a, b) => b.value - a.value)
    .slice(0, limit);

  const { containerRef } = useEChart(
    (colors) => ({
      animation: false,
      tooltip: {
        trigger: 'item',
        valueFormatter: (value: unknown) =>
          typeof value === 'number' ? formatValue(value, unit, decimals).formatted : String(value),
      },
      grid: { left: 8, right: 56, top: 8, bottom: 8, containLabel: true },
      xAxis: {
        type: 'value',
        // Tick labels drop the unit word — the bar labels already carry it, and
        // repeating `req/s` on every tick makes them collide.
        axisLabel: { formatter: (value: number) => formatValue(value, 'short').formatted },
      },
      yAxis: {
        type: 'category',
        // ECharts draws category axes bottom-up; reverse so rank 1 is on top.
        data: ranked.map((entry) => entry.name).reverse(),
        axisLabel: { width: 180, overflow: 'truncate' },
      },
      series: [
        {
          type: 'bar' as const,
          data: ranked
            .map((entry) => ({
              value: entry.value,
              itemStyle: thresholds ? { color: resolveThresholdColor(entry.value, thresholds) } : undefined,
            }))
            .reverse(),
          label: {
            show: true,
            position: 'right' as const,
            // Series labels do not inherit `textStyle`, so an unset colour is
            // dark-on-dark in dark mode.
            color: colors.foreground,
            formatter: ({ value }: { value: number }) => formatValue(value, unit, decimals).formatted,
          },
        },
      ],
    }),
    colorMode,
    [series, unit, decimals, thresholds, limit, names.join('|')]
  );

  return (
    <div ref={containerRef} className={styles.chart} style={{ '--viz-height': `${height}px` } as CSSProperties} />
  );
}
