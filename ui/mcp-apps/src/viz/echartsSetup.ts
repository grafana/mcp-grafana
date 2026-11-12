/**
 * Single place where ECharts features are registered.
 *
 * Always import from `echarts/core` and register explicitly — `import * as
 * echarts from 'echarts'` pulls every chart type and renderer (~1 MB extra),
 * and an app bundle ships as a `ui://` protocol resource, so raw size travels.
 *
 * ECharts' core dominates the cost, so each additional chart type after the
 * first is comparatively cheap. Views that need no chart engine — Stat, Bullet,
 * Table — are plain DOM and stay out of this bundle entirely.
 */

import * as echarts from 'echarts/core';
import { BarChart, HeatmapChart, LineChart } from 'echarts/charts';
import {
  BrushComponent,
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  MarkLineComponent,
  TooltipComponent,
  VisualMapComponent,
} from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';

echarts.use([
  LineChart,
  BarChart,
  HeatmapChart,
  GridComponent,
  TooltipComponent,
  DataZoomComponent,
  LegendComponent,
  MarkLineComponent,
  // Brush drives "select a window and ask the agent"; dataZoom alone cannot,
  // because a drag there pans or zooms rather than reporting a range.
  BrushComponent,
  // The heatmap's colour scale.
  VisualMapComponent,
  CanvasRenderer,
]);

export { echarts };
export type EChartsOption = Parameters<echarts.ECharts['setOption']>[0];
