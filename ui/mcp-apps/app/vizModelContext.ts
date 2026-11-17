import constants from '@grafana/viz-contract/registry/constants.json';

import type { RenderState } from '../renderer/renderer.js';
import type { VizArgs, VizResult } from './vizSpec';

/**
 * The model context of the viz view (state.15): one text that names the chart, the time range of the tool arguments
 * and the absolute range of the data, the state, the error message and the message of each notice. Arguments with no
 * `from` or `to` use the defaults of the Source.
 */
export function vizModelContext(args: VizArgs, viz: VizResult, state: RenderState): string {
  const from = args.from ?? constants.defaultFrom.value;
  const to = args.to ?? constants.defaultTo.value;
  const range = viz.data.range;
  const lines = [
    `The Grafana App shows a ${viz.chart} chart of data source ${args.datasourceUid}.`,
    `Time range of the arguments: ${from} to ${to}.`,
    `Time range of the data: ${new Date(range.from).toISOString()} to ${new Date(range.to).toISOString()}.`,
    `State: ${state.state}.`,
  ];
  if (state.error) lines.push(`Error: ${state.error.message}`);
  for (const notice of state.notices) lines.push(`Notice: ${notice.message}`);
  return lines.join('\n');
}
