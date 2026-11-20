import type { Meta, StoryObj } from '@storybook/react-vite';

import { McpAppShell } from '../src/McpAppShell';
import type { McpAppColorMode } from '../src/McpAppShell';

type Args = { colorMode: McpAppColorMode };

const meta: Meta<Args> = {
  title: 'Shell/McpAppShell',
  parameters: {
    docs: {
      description: {
        component:
          'The frame around every render_grafana_app view: summary, actions, feedback banner and content. ' +
          'Use the toolbar to switch color mode.',
      },
    },
  },
};
export default meta;
type Story = StoryObj<Args>;

const body = (
  <p style={{ margin: 0 }}>The view for the tool result goes here (a chart, a trace waterfall or the viz Renderer).</p>
);
const mode = (ctx: { globals: Record<string, unknown> }) => ctx.globals.colorMode as McpAppColorMode;

export const SummaryOnly: Story = {
  render: (_, ctx) => (
    <McpAppShell
      product="Grafana"
      colorMode={mode(ctx)}
      summary={{ label: 'Data source query', title: 'sum(rate(http_requests_total[5m]))', description: 'Time series · prometheus' }}
    >
      {body}
    </McpAppShell>
  ),
};

export const WithActions: Story = {
  render: (_, ctx) => (
    <McpAppShell
      product="Grafana"
      colorMode={mode(ctx)}
      summary={{ label: 'Data source query', title: 'up', description: 'Time series · prometheus' }}
      primaryAction={{ label: 'Refresh', onClick: () => {} }}
      secondaryAction={{ label: 'Copy link', onClick: () => {} }}
      openInGrafana={{ href: 'https://example.grafana.net/explore', onClick: (e) => e.preventDefault() }}
      tip={{ message: 'Drag across the chart to ask the agent about a time window.' }}
    >
      {body}
    </McpAppShell>
  ),
};

export const Pending: Story = {
  render: (_, ctx) => (
    <McpAppShell
      product="Grafana"
      colorMode={mode(ctx)}
      summary={{ label: 'Data source query', title: 'up', description: 'Refreshing…' }}
      primaryAction={{ label: 'Refresh', onClick: () => {}, pending: true }}
    >
      {body}
    </McpAppShell>
  ),
};

export const ErrorFeedback: Story = {
  render: (_, ctx) => (
    <McpAppShell
      product="Grafana"
      colorMode={mode(ctx)}
      summary={{ label: 'Data source query', title: 'up' }}
      feedback={{ tone: 'error', message: 'The query failed. Check the query inputs and datasource access.' }}
    >
      {body}
    </McpAppShell>
  ),
};

export const SuccessFeedback: Story = {
  render: (_, ctx) => (
    <McpAppShell
      product="Grafana"
      colorMode={mode(ctx)}
      summary={{ label: 'Trace', title: '4bf92f35…' }}
      feedback={{ tone: 'success', message: 'Link copied.' }}
    >
      {body}
    </McpAppShell>
  ),
};

export const WideCompact: Story = {
  render: (_, ctx) => (
    <McpAppShell
      product="Grafana"
      layout="wide"
      density="compact"
      colorMode={mode(ctx)}
      summary={{ label: 'Data source query', title: 'up' }}
    >
      {body}
    </McpAppShell>
  ),
};
