import type { McpAppColorMode } from '../McpAppShell';
/**
 * Grafana ECharts theme, derived from the shared shell's CSS custom properties.
 *
 * The shell (`src/design.tsx`) already themes chrome via `--mcp-*` variables
 * switched on `[data-color-mode]`. Charts read the *computed* values of those
 * same variables so there is one theme source for chrome and charts, and
 * light/dark keeps working through `app.onhostcontextchanged` with no second
 * code path.
 *
 * The one thing the shell does not define is a categorical series palette —
 * chrome never needed one. This module adds it.
 */

/**
 * Grafana's classic series palette. Charts in an MCP app are read in the same
 * sitting as charts in Grafana, so series colours should agree.
 */
export const SERIES_PALETTE = [
  '#7EB26D',
  '#EAB839',
  '#6ED0E0',
  '#EF843C',
  '#E24D42',
  '#1F78C1',
  '#BA43A9',
  '#705DA0',
  '#508642',
  '#CCA300',
  '#447EBC',
  '#C15C17',
  '#890F02',
  '#0A437C',
  '#6D1F62',
  '#584477',
] as const;

/** Fallbacks for when a variable is unset (preview harnesses, tests, jsdom). */
const FALLBACK = {
  light: { foreground: '#24272b', muted: '#86898c', border: '#e4e7e7', background: '#ffffff', inset: '#f4f5f5' },
  dark: { foreground: '#fafafa', muted: '#8e8fa1', border: '#2c2f3e', background: '#1b1d20', inset: '#24272b' },
} as const;



export type ThemeColors = {
  foreground: string;
  muted: string;
  border: string;
  background: string;
  inset: string;
};

const FONT_UI = 'Inter, system-ui, sans-serif';
const FONT_MONO = 'ui-monospace, SFMono-Regular, monospace';

/**
 * Read the shell's variables off an element. Returns fallbacks for any that are
 * unset so a chart never renders with empty colours.
 */
export function readThemeColors(root: Element | null, mode: McpAppColorMode = 'light'): ThemeColors {
  const defaults = FALLBACK[mode];
  if (!root || typeof getComputedStyle !== 'function') return { ...defaults };
  const styles = getComputedStyle(root);
  const read = (name: string, fallback: string) => styles.getPropertyValue(name).trim() || fallback;
  return {
    foreground: read('--mcp-foreground', defaults.foreground),
    muted: read('--mcp-muted-foreground', defaults.muted),
    border: read('--mcp-border', defaults.border),
    background: read('--mcp-background', defaults.background),
    inset: read('--mcp-inset', defaults.inset),
  };
}

/**
 * Build an ECharts theme object. Register with
 * `echarts.registerTheme('grafana', grafanaEChartsTheme(colors))`, or pass the
 * result straight to `echarts.init(el, theme)`.
 */
export function grafanaEChartsTheme(colors: ThemeColors) {
  const axis = {
    axisLine: { show: true, lineStyle: { color: colors.border } },
    axisTick: { show: false },
    axisLabel: { color: colors.muted, fontFamily: FONT_MONO, fontSize: 11 },
    splitLine: { show: true, lineStyle: { color: colors.border, type: 'solid' as const } },
    splitArea: { show: false },
  };

  return {
    color: [...SERIES_PALETTE],
    backgroundColor: 'transparent',
    textStyle: { fontFamily: FONT_UI, color: colors.foreground, fontSize: 12 },
    // Grafana draws 1.5px series lines with no point markers until hover.
    line: { lineStyle: { width: 1.5 }, symbol: 'none', symbolSize: 6, smooth: false },
    bar: { itemStyle: { borderRadius: [2, 2, 0, 0] } },
    grid: { left: 8, right: 8, top: 8, bottom: 8, containLabel: true, borderColor: colors.border },
    categoryAxis: { ...axis, splitLine: { show: false } },
    valueAxis: axis,
    timeAxis: axis,
    logAxis: axis,
    legend: {
      textStyle: { color: colors.foreground, fontFamily: FONT_UI, fontSize: 12 },
      icon: 'roundRect',
      itemWidth: 12,
      itemHeight: 3,
    },
    tooltip: {
      backgroundColor: colors.background,
      borderColor: colors.border,
      borderWidth: 1,
      textStyle: { color: colors.foreground, fontFamily: FONT_MONO, fontSize: 12 },
      axisPointer: { lineStyle: { color: colors.muted }, crossStyle: { color: colors.muted } },
    },
    dataZoom: [
      {
        type: 'inside',
        // Drag-to-select a window rather than zooming on scroll; the selection
        // is what gets handed back to the agent.
        zoomOnMouseWheel: false,
        moveOnMouseWheel: false,
        moveOnMouseMove: false,
      },
    ],
  };
}

/**
 * Sequential ramp for continuous encodings such as a heatmap's density.
 *
 * The categorical palette cannot express magnitude — adjacent hues read as
 * different *kinds*, not different amounts. This runs light to saturated
 * through Grafana's brand orange, so a dense cell reads as hot.
 */
export const SEQUENTIAL_PALETTE = ['#f4f5f5', '#ffe2c0', '#ffc47b', '#ffa64d', '#ed861d', '#ad5100'] as const;

/**
 * Translucent version of a `#rgb`/`#rrggbb` colour.
 *
 * Used for surfaces that must stay visible in both colour modes without
 * belonging to either token: a gauge's unfilled track, for instance, is
 * invisible if drawn in `--mcp-inset` (same as the card) and reads as navy if
 * drawn in `--mcp-border` (a hairline colour, wrong at 14px). Foreground at low
 * alpha is neutral by construction in light and dark.
 *
 * Returns the input unchanged when it is not a hex colour, so a `var()` or
 * named colour degrades rather than breaking.
 */
export function withAlpha(color: string, alpha: number): string {
  const hex = color.trim();
  const match = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(hex);
  if (!match) return color;
  const digits = match[1];
  const full = digits.length === 3 ? digits.replace(/./g, (d) => d + d) : digits;
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(full.slice(i, i + 2), 16));
  const clamped = Math.min(1, Math.max(0, alpha));
  return `rgba(${r}, ${g}, ${b}, ${clamped})`;
}

/** Subtle track colour for meters and gauges, neutral in both colour modes. */
export function trackColor(colors: ThemeColors): string {
  return withAlpha(colors.foreground, 0.14);
}

/** Stable colour for a series index, wrapping the palette. */
export function seriesColor(index: number): string {
  return SERIES_PALETTE[index % SERIES_PALETTE.length];
}
