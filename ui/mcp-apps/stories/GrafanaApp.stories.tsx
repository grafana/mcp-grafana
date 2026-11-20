import type { Meta, StoryObj } from '@storybook/react-vite';

import { trace } from '../preview/traceFixture';
import type { McpAppColorMode } from '../src/McpAppShell';
import { matrixMultiSeries, vectorMultiSeries } from '../test/metricsFixtures';
import { HostedApp, type Scenario } from './fakeHost';

type Args = { colorMode: McpAppColorMode };

const meta: Meta<Args> = {
  title: 'App/GrafanaApplication',
  parameters: {
    docs: {
      description: {
        component:
          'The real App (GrafanaApplication) driven by a scripted fake MCP host, the same way the Vitest tests ' +
          'drive it. Host messages go through createAppHostState, so these stories show the true waiting, error, ' +
          'cancelled and closed states, not hand-built ones. The viz stories load the committed viz Renderer ' +
          '(dist/viz-renderer.js), so charts are the real ones.',
      },
    },
  },
};
export default meta;
type Story = StoryObj<Args>;

const mode = (ctx: { globals: Record<string, unknown> }) => ctx.globals.colorMode as McpAppColorMode;
const app = (scenario: Scenario): Story => ({
  render: (_, ctx) => <HostedApp key={`${ctx.id}-${mode(ctx)}`} scenario={scenario} colorMode={mode(ctx)} />,
});

const promArgs = { expr: 'rate(node_cpu_seconds_total[5m])', datasourceUid: 'prometheus' };

// States before or instead of a view.
export const Waiting = app({ toolName: 'query_prometheus', args: promArgs });
export const QueryFailed = app({ toolName: 'query_prometheus', args: promArgs, failed: true });
export const Cancelled = app({ toolName: 'query_prometheus', args: promArgs, cancelled: true });
export const Closed = app({ toolName: 'query_prometheus', args: promArgs, closed: true });
export const Unparseable = app({ toolName: 'query_prometheus', args: promArgs, result: { unexpected: true } });

// Views.
export const PrometheusTimeSeries = app({
  toolName: 'query_prometheus',
  args: promArgs,
  result: matrixMultiSeries,
});
export const PrometheusRankedBars = app({
  toolName: 'query_prometheus',
  args: { expr: 'topk(5, http_requests_total)', datasourceUid: 'prometheus' },
  result: vectorMultiSeries,
});
export const TempoTrace = app({
  toolName: 'get_tempo_trace',
  args: { trace_id: trace.traceId, datasourceUid: trace.datasourceUid },
  result: { ...trace, spans: trace.spans.slice(0, 60) },
});
