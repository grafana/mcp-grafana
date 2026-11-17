import constants from '@grafana/viz-contract/registry/constants.json';

import type { RenderState } from '../renderer/renderer.js';
import type { VizArgs, VizResult } from './vizSpec';

/**
 * The model context of the viz view (state.15): one text that names the chart, the time range of the tool arguments
 * and the absolute range of the data, the state, the error message and the message of each notice. Arguments with no
 * `from` or `to` use the defaults of the Source.
 */
export function vizModelContext(args: VizArgs, viz: VizResult, state: RenderState): string {
  const lines = [
    `The Grafana App shows a ${viz.chart} chart of data source ${args.datasourceUid}.`,
    ...ranges(args, viz),
    `State: ${state.state}.`,
  ];
  if (state.error) lines.push(`Error: ${state.error.message}`);
  for (const notice of state.notices) lines.push(`Notice: ${notice.message}`);
  return lines.join('\n');
}

/**
 * The model context when the App shows no chart: the Renderer did not load, or its render threw. The Renderer then
 * gives no state, so the App gives the error state with its own message (state.14).
 */
export function vizFailureContext(args: VizArgs, viz: VizResult, message: string): string {
  return [
    `The Grafana App could not show the ${viz.chart} chart of data source ${args.datasourceUid}.`,
    ...ranges(args, viz),
    'State: error.',
    `Error: ${message}`,
  ].join('\n');
}

/** The time range of the tool arguments and the absolute range of the data (state.15). */
function ranges(args: VizArgs, viz: VizResult): string[] {
  const from = args.from ?? constants.defaultFrom.value;
  const to = args.to ?? constants.defaultTo.value;
  const range = viz.data.range;
  return [
    `Time range of the arguments: ${from} to ${to}.`,
    `Time range of the data: ${new Date(range.from).toISOString()} to ${new Date(range.to).toISOString()}.`,
  ];
}
