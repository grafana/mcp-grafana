import { useEffect, useRef, useState } from 'react';
import { echarts, type EChartsOption } from './echartsSetup';
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
 *
 * `containerRef` is a callback ref, so the chart also follows the container
 * itself: a component that shows an empty state instead of the chart div, and
 * later gets data, still gets a chart.
 *
 * `chart` is state, not a ref, so effects that bind to the instance (event
 * handlers, actions) re-run whenever a new one is created — on mount, after an
 * empty state, and after a colour-mode change re-creates it.
 */
export function useEChart(
  buildOption: (colors: ThemeColors) => EChartsOption,
  colorMode: McpAppColorMode,
  deps: readonly unknown[] = []
) {
  const [container, containerRef] = useState<HTMLDivElement | null>(null);
  const [chart, setChart] = useState<echarts.ECharts | null>(null);
  // Keep the latest builder without making it a dependency — callers pass
  // inline closures, which would otherwise re-init on every render.
  const builderRef = useRef(buildOption);
  builderRef.current = buildOption;

  useEffect(() => {
    if (!container) return;

    const colors = readThemeColors(container, colorMode);
    const instance = echarts.init(container, grafanaEChartsTheme(colors), { renderer: 'canvas' });
    instance.setOption(builderRef.current(colors));
    setChart(instance);

    const observer = new ResizeObserver(() => instance.resize());
    observer.observe(container);

    return () => {
      observer.disconnect();
      instance.dispose();
      setChart(null);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [colorMode, container]);

  useEffect(() => {
    if (!chart || !container) return;
    const colors = readThemeColors(container, colorMode);
    // `notMerge` so removed series do not linger when the query changes.
    chart.setOption(builderRef.current(colors), { notMerge: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chart, ...deps]);

  return { containerRef, chart };
}
