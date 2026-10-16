export { McpAppSection } from './McpAppSection';
export type { McpAppSectionProps } from './McpAppSection';
export { McpAppShell } from './McpAppShell';
export type {
  McpAppAction,
  McpAppColorMode,
  McpAppFeedback,
  McpAppOpenInGrafana,
  McpAppShellProps,
  McpAppSummary,
  McpAppTip,
} from './McpAppShell';

export { RenderTraceApp } from './trace/RenderTraceApp';
export type { RenderTraceAppProps } from './trace/RenderTraceApp';
export type { TraceViewResult, TraceSpan, TraceEvent } from './trace/types';

export { RenderMetricsApp } from './metrics/RenderMetricsApp';
export type { RenderMetricsAppProps } from './metrics/RenderMetricsApp';
export { isBoundedUnit, pickViz } from './metrics/pickViz';
export { VizPicker, vizLabel } from './metrics/VizPicker';
export type { MetricsResult, ParsedMetrics, VizChoice, VizKind } from './metrics/pickViz';

// Visualization library — shared across apps.
export { BarChart } from './viz/BarChart';
export { Bullet, BulletGroup, thresholdBands } from './viz/Bullet';
export { Stat, StatGroup } from './viz/Stat';
export { Heatmap, deaccumulateBuckets, isHistogramBuckets } from './viz/Heatmap';
export { Table } from './viz/Table';
export { TimeSeriesChart } from './viz/TimeSeriesChart';
export { display, formatValue, inferUnit, resolveThresholdColor } from './viz/format';
export type { DisplayValue, Threshold, Unit } from './viz/format';
export { sparklinePath } from './viz/sparkline';
export {
  grafanaEChartsTheme,
  readThemeColors,
  SEQUENTIAL_PALETTE,
  SERIES_PALETTE,
  seriesColor,
  trackColor,
  withAlpha,
} from './viz/theme';
export type { ThemeColors } from './viz/theme';
export { commonMetricName, latestValue, seriesName, shortSeriesNames } from './viz/types';
export type { MetricPoint, MetricSeries } from './viz/types';
