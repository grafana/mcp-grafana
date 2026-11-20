import type { Meta, StoryObj } from '@storybook/react-vite';
import type { ComponentType } from 'react';

import type { McpAppColorMode } from '../../src/McpAppShell';
import { DeltaView } from './DeltaTable';
import {
  AlertsPreview,
  BlocksPreview,
  ButtonsPreview,
  ControlsPreview,
  QueryPreview,
  ShellPreview,
  SummaryPreview,
} from './previews';

const meta: Meta = {
  title: 'Design delta/Components',
  parameters: {
    docs: {
      description: {
        component:
          'One page per design component: the component as it is in code today beside a table of what separates it ' +
          'from the design canvas. The data is in deltas.ts.',
      },
    },
  },
};
export default meta;

const mode = (ctx: { globals: Record<string, unknown> }) => ctx.globals.colorMode as McpAppColorMode;
const page =
  (id: string, Preview: ComponentType<{ colorMode: McpAppColorMode }>, name: string): StoryObj => ({
    name,
    render: (_, ctx) => (
      <DeltaView id={id}>
        <Preview colorMode={mode(ctx)} />
      </DeltaView>
    ),
  });

export const Shell = page('shell', ShellPreview, 'App shell');
export const Summary = page('summary', SummaryPreview, 'Response summary');
export const Blocks = page('blocks', BlocksPreview, 'Content blocks');
export const Alerts = page('alerts', AlertsPreview, 'Alerts');
export const Buttons = page('buttons', ButtonsPreview, 'Action buttons');
export const Query = page('query', QueryPreview, 'Query');
export const Controls = page('controls', ControlsPreview, 'Visualization controls');
