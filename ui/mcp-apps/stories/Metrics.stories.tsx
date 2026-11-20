import type { Meta, StoryObj } from '@storybook/react-vite';

import { parsePrometheusResult } from '../app/parsePrometheusResult';
import type { McpAppColorMode } from '../src/McpAppShell';
import { RenderMetricsApp } from '../src/metrics/RenderMetricsApp';
import {
  RATIO_THRESHOLDS,
  emptyResult,
  matrixMultiSeries,
  matrixSingleSeries,
  scalarResult,
  vectorMultiSeries,
  vectorSingleBounded,
  vectorSingleUnbounded,
} from '../test/metricsFixtures';

type Args = { colorMode: McpAppColorMode };

const meta: Meta<Args> = {
  title: 'Metrics/RenderMetricsApp',
  parameters: {
    docs: {
      description: {
        component:
          'The query_prometheus view. The shape of the response picks the chart, and the picker offers the others. ' +
          'Data comes from test/metricsFixtures.ts, in the exact shape the server returns.',
      },
    },
  },
};
export default meta;
type Story = StoryObj<Args>;

const parse = (payload: unknown) => {
  const parsed = parsePrometheusResult(payload);
  if (!parsed) throw new Error('fixture did not parse');
  return parsed;
};
const mode = (ctx: { globals: Record<string, unknown> }) => ctx.globals.colorMode as McpAppColorMode;

/** Deterministic latency histogram buckets (cumulative, with `le` labels) for the heatmap view. */
const bucketsPayload = {
  data: ['0.05', '0.1', '0.25', '0.5', '1', '2.5', '+Inf'].map((le, b) => ({
    metric: { __name__: 'http_request_duration_seconds_bucket', le },
    values: Array.from({ length: 40 }, (_, i): [number, string] => [
      1760000000 + i * 60,
      String(Math.round((b + 1) * 40 + i * (b + 2) * 3 + Math.sin(i / 3) * 10)),
    ]),
  })),
};

/** 24 series, enough for the heatmap view. */
const manySeriesPayload = {
  data: Array.from({ length: 24 }, (_, s) => ({
    metric: { __name__: 'container_cpu_usage_seconds_total', pod: `app-${String(s).padStart(2, '0')}` },
    values: Array.from({ length: 40 }, (_, i): [number, string] => [
      1760000000 + i * 60,
      String(Number((0.2 + s * 0.03 + Math.sin(i / 4 + s) * 0.1).toFixed(4))),
    ]),
  })),
};

const story = (payload: unknown, extra: Partial<React.ComponentProps<typeof RenderMetricsApp>> = {}): Story => ({
  render: (_, ctx) => (
    <RenderMetricsApp
      result={parse(payload)}
      colorMode={mode(ctx)}
      onRefresh={() => {}}
      onOpenInGrafana={() => {}}
      onSelectRange={() => {}}
      {...extra}
    />
  ),
});

export const TimeSeries = story(matrixMultiSeries, { expr: 'rate(node_cpu_seconds_total[5m])' });
export const TimeSeriesSingle = story(matrixSingleSeries, { expr: 'go_memstats_alloc_bytes' });
export const HeatmapManySeries = story(manySeriesPayload, { expr: 'rate(container_cpu_usage_seconds_total[5m])' });
export const HeatmapHistogramBuckets = story(bucketsPayload, {
  expr: 'http_request_duration_seconds_bucket',
});
export const RankedBars = story(vectorMultiSeries, { expr: 'topk(5, http_requests_total)' });
export const Stat = story(vectorSingleUnbounded, { expr: 'go_goroutines' });
export const BulletWithThresholds = story(vectorSingleBounded, {
  expr: 'node_memory_utilisation_ratio',
  thresholds: RATIO_THRESHOLDS,
});
export const Scalar = story(scalarResult, { expr: 'scalar(42)' });
export const EmptyResult = story(emptyResult, { expr: 'missing_metric_total' });
export const Refreshing = story(matrixMultiSeries, { expr: 'rate(node_cpu_seconds_total[5m])', refreshing: true });
export const WithErrorNotice = story(matrixMultiSeries, {
  expr: 'rate(node_cpu_seconds_total[5m])',
  notice: { tone: 'error', message: 'Refresh failed: the datasource did not respond.' },
});
