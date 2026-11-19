// The experimental entry point of the package: the trace and metrics views and the visualization library. These
// exports can change in a minor version. The chart components need the optional `echarts` peer dependency.
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
