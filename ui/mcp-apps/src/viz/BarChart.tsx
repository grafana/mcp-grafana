import type { CSSProperties } from 'react';
import { formatValue, resolveThresholdColor, type Threshold, type Unit } from './format';
import { latestValue, shortSeriesNames, type MetricSeries } from './types';
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
  /** Cap on bars drawn; the footer says how many are left out. */
  limit?: number;
};

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
  const finite = series
    .map((s, i) => ({ name: names[i], value: latestValue(s) }))
    .filter((entry) => Number.isFinite(entry.value))
    .sort((a, b) => b.value - a.value);
  const ranked = finite.slice(0, limit);
  // NaN and ±Inf have no bar length; count them rather than drop them silently.
  const notPlotted = series.length - finite.length;
  const notes = [
    finite.length > ranked.length ? `Showing the top ${ranked.length} of ${finite.length} series.` : '',
    notPlotted ? `${notPlotted} series with no numeric value ${notPlotted === 1 ? 'is' : 'are'} not plotted.` : '',
  ].filter(Boolean);

  const { containerRef } = useEChart(
    (colors) => ({
      animation: false,
      tooltip: {
        // Keep it inside the chart: the iframe clips anything past its edge.
        confine: true,
        trigger: 'item',
        valueFormatter: (value: unknown) =>
          typeof value === 'number' ? formatValue(value, unit, decimals).formatted : String(value),
      },
      grid: { left: 8, right: 56, top: 8, bottom: 8, containLabel: true },
      xAxis: {
        type: 'value',
        // Same unit as the bar labels, so the axis and the values agree.
        axisLabel: { formatter: (value: number) => formatValue(value, unit).formatted, hideOverlap: true },
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
    <>
      <div ref={containerRef} className={styles.chart} style={{ '--viz-height': `${height}px` } as CSSProperties} />
      {notes.length > 0 && <div className={styles.tableFooter}>{notes.join(' ')} Use Table for every value.</div>}
    </>
  );
}
