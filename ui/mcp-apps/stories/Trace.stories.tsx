import type { Meta, StoryObj } from '@storybook/react-vite';

import { trace } from '../preview/traceFixture';
import type { McpAppColorMode } from '../src/McpAppShell';
import { RenderTraceApp } from '../src/trace/RenderTraceApp';
import type { TraceViewResult } from '../src/trace/types';

type Args = { colorMode: McpAppColorMode };

const meta: Meta<Args> = {
  title: 'Trace/RenderTraceApp',
  parameters: {
    docs: {
      description: {
        component:
          'The get_tempo_trace view: a virtualized waterfall with span search, an errors-only filter, a focused ' +
          'ancestor path and a span inspector. Data comes from preview/traceFixture.ts.',
      },
    },
  },
};
export default meta;
type Story = StoryObj<Args>;

const mode = (ctx: { globals: Record<string, unknown> }) => ctx.globals.colorMode as McpAppColorMode;
const story = (result: TraceViewResult): Story => ({
  render: (_, ctx) => <RenderTraceApp result={result} colorMode={mode(ctx)} onOpenInGrafana={() => {}} />,
});

// The fixture has 12,000 spans, to exercise virtualization. Trim to the first spans for the everyday story.
const small: TraceViewResult = { ...trace, spans: trace.spans.slice(0, 60) };

export const Waterfall = story(small);
export const LargeTrace = story(trace);
export const WithFocusedSpan = story({ ...small, focusSpanId: small.spans[3].id });
export const EmptyTrace = story({ ...trace, spans: [] });
