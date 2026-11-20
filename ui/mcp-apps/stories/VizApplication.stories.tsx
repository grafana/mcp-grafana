import type { Meta, StoryObj } from '@storybook/react-vite';

import type { McpAppColorMode } from '../src/McpAppShell';
import { HostedApp, type Scenario } from './fakeHost';

type Args = { colorMode: McpAppColorMode };

// The viz-contract fixtures, bundled as JSON. test/vizFixtures.ts reads the same files with node:fs.
const files = import.meta.glob<unknown>(
  '/node_modules/@grafana/viz-contract/fixtures/*/{args,result}.json',
  { eager: true, import: 'default' },
);
const fixture = (set: string, file: 'args' | 'result') => {
  const value = files[`/node_modules/@grafana/viz-contract/fixtures/${set}/${file}.json`];
  if (value === undefined) throw new Error(`missing viz-contract fixture ${set}/${file}.json`);
  return value as Record<string, unknown>;
};

const meta: Meta<Args> = {
  title: 'App/VizApplication (query_datasource)',
  parameters: {
    docs: {
      description: {
        component:
          'The query_datasource view, drawn by the viz Renderer. Fixtures come from @grafana/viz-contract. The ' +
          'chart picker in the header switches between the alternatives in each result.',
      },
    },
  },
};
export default meta;
type Story = StoryObj<Args>;

const mode = (ctx: { globals: Record<string, unknown> }) => ctx.globals.colorMode as McpAppColorMode;
const viz = (set: string, extra: Partial<Scenario> = {}): Story => ({
  render: (_, ctx) => (
    <HostedApp
      key={`${ctx.id}-${mode(ctx)}`}
      colorMode={mode(ctx)}
      scenario={{
        toolName: 'query_datasource',
        args: fixture(set, 'args'),
        result: fixture(set, 'result'),
        ...extra,
      }}
    />
  ),
});

export const PrometheusLine = viz('prometheus-line');
export const TimeSeriesMultiFrame = viz('pick-time-series-multi-frame');
export const SingleStat = viz('pick-single-stat');
export const Categories = viz('pick-categories');
export const HeatmapCells = viz('pick-heatmap-cells');
export const LogLines = viz('pick-log-lines');
export const NumericOnly = viz('pick-numeric-only');
export const NoData = viz('no-data');
export const PartialError = viz('partial-error');
/** The host cannot read server resources, so the Renderer cannot load. */
export const RendererUnavailable = viz('prometheus-line', { serverResources: false });
