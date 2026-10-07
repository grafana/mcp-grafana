import { useEffect, useRef } from 'react';
import { echarts, registerEChartsFeatures, type EChartsOption } from './echartsSetup';
import { grafanaEChartsTheme, readThemeColors, type ThemeColors } from './theme'
import type { McpAppColorMode } from '../McpAppShell';

/**
 * Mount an ECharts instance into a div and keep it in step with the option,
 * the host theme, and the container size.
 *
 * The theme is read from the shell's computed `--mcp-*` custom properties on
 * each colour-mode change, so chrome and charts share one source. ECharts
 * cannot re-theme an existing instance, so a mode flip disposes and re-inits —
 * which is why `colorMode` is a dependency and the option is applied after.
 */
export function useEChart(
  buildOption: (colors: ThemeColors) => EChartsOption,
  colorMode: McpAppColorMode,
  deps: readonly unknown[] = []
) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<echarts.ECharts | null>(null);
  // Keep the latest builder without making it a dependency — callers pass
  // inline closures, which would otherwise re-init on every render.
  const builderRef = useRef(buildOption);
  builderRef.current = buildOption;

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    // Must run before init: the registration cannot be a module side effect in
    // this package — see echartsSetup.ts.
    registerEChartsFeatures();

    const colors = readThemeColors(container, colorMode);
    const chart = echarts.init(container, grafanaEChartsTheme(colors), { renderer: 'canvas' });
    chartRef.current = chart;
    chart.setOption(builderRef.current(colors));

    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(container);

    return () => {
      observer.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [colorMode]);

  useEffect(() => {
    const chart = chartRef.current;
    const container = containerRef.current;
    if (!chart || !container) return;
    const colors = readThemeColors(container, colorMode);
    // `notMerge` so removed series do not linger when the query changes.
    chart.setOption(builderRef.current(colors), { notMerge: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [colorMode, ...deps]);

  return { containerRef, chartRef };
}
