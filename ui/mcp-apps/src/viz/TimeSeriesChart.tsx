import { useEffect, type CSSProperties } from 'react';

import { axisDecimals, formatValue, type Unit } from './format';
import { shortSeriesNames, type MetricSeries } from './types';
import type { McpAppColorMode } from '../McpAppShell';
import { useEChart } from './useEChart';
import { getVizStyles } from './Viz.styles';

export type TimeSeriesChartProps = {
  series: MetricSeries[];
  unit?: Unit;
  decimals?: number;
  colorMode?: McpAppColorMode;
  height?: number;
  /**
   * Arms the brush cursor. Controlled by the consumer, which owns the control
   * row — an always-on brush would swallow hover and cost the tooltip.
   */
  selecting?: boolean;
  /**
   * Called when the user brushes out a time window. The consumer decides what
   * that means — the chart stays presentation-only and knows nothing about MCP.
   */
  onSelectRange?: (fromMs: number, toMs: number) => void;
};

export function TimeSeriesChart({
  series,
  unit = 'none',
  decimals,
  colorMode = 'light',
  height = 240,
  selecting = false,
  onSelectRange,
}: TimeSeriesChartProps) {
  const styles = getVizStyles();
  // Legend and tooltip show only what distinguishes each series.
  const names = shortSeriesNames(series);
  const yDecimals = decimals ?? axisDecimals(series.flatMap((s) => s.points.map(([, v]) => v)), unit);

  const { containerRef, chartRef } = useEChart(
    () => ({
      animation: false,
      tooltip: {
        trigger: 'axis',
        valueFormatter: (value: unknown) =>
          typeof value === 'number' ? formatValue(value, unit, decimals).formatted : String(value),
      },
      legend: series.length > 1 ? { type: 'scroll', bottom: 0, itemGap: 16 } : undefined,
      // containLabel keeps the top tick label inside the grid, so little top
      // padding is needed.
      grid: { left: 8, right: 12, top: 4, bottom: series.length > 1 ? 32 : 8, containLabel: true },
      // Narrow panels are the norm in a chat; drop labels rather than
      // overprint them.
      xAxis: { type: 'time', axisLabel: { hideOverlap: true } },
      // Fit the axis to the data, as Grafana does, rather than always
      // including zero; otherwise small variations draw as a flat line.
      yAxis: {
        type: 'value',
        scale: true,
        axisLabel: { formatter: (value: number) => formatValue(value, unit, yDecimals).formatted },
      },
      // Horizontal-only selection: the question is always "what happened
      // during this window", never "in this value band".
      brush: onSelectRange
        ? { toolbox: [], xAxisIndex: 0, brushMode: 'single', throttleType: 'debounce', throttleDelay: 100 }
        : undefined,
      series: series.map((s, i) => ({
        type: 'line' as const,
        name: names[i],
        showSymbol: false,
        data: s.points,
      })),
    }),
    colorMode,
    [series, unit, decimals, yDecimals, names.join('|'), Boolean(onSelectRange)]
  );

  // Arm or disarm the brush cursor, and report the window once drawn.
  useEffect(() => {
    const chart = chartRef.current;
    if (!chart || !onSelectRange) return;

    const handleBrushEnd = (params: unknown) => {
      const areas = (params as { areas?: Array<{ coordRange?: number[] }> }).areas ?? [];
      const range = areas[0]?.coordRange;
      if (!range || range.length !== 2) return;
      const [from, to] = range;
      if (from === to) return;
      // Clear the painted area before reporting; the consumer disarms.
      chart.dispatchAction({ type: 'brush', areas: [] });
      onSelectRange(Math.round(Math.min(from, to)), Math.round(Math.max(from, to)));
    };

    chart.on('brushEnd', handleBrushEnd);
    chart.dispatchAction({
      type: 'takeGlobalCursor',
      key: 'brush',
      brushOption: selecting ? { brushType: 'lineX', brushMode: 'single' } : {},
    });

    return () => {
      chart.off('brushEnd', handleBrushEnd);
    };
  }, [chartRef, onSelectRange, selecting, series]);

  return (
    <div ref={containerRef} className={styles.chart} style={{ '--viz-height': `${height}px` } as CSSProperties} />
  );
}
