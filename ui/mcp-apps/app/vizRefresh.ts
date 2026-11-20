import registry from '@grafana/viz-contract/registry/notices.json';
import type { App } from '@modelcontextprotocol/ext-apps';

import type { RefreshRequest } from '../renderer/renderer.js';
import { extractToolPayload } from './parsePrometheusResult';
import { parseVizResult, type VizArgs, type VizNotice, type VizResult } from './vizSpec';

type CallResult = Awaited<ReturnType<App['callServerTool']>>;

/**
 * What a refresh gives: the new last arguments, the result and the notices of its viz spec, or the error text.
 * `mismatch`: the Source chose the chart after type-mismatch (refresh.10).
 */
export type VizRefreshOutcome =
  | { args: VizArgs; viz: VizResult; notices: VizNotice[]; mismatch: boolean }
  | { error: string };

/** `args` with `type` set, or removed when it is undefined. */
export function withType(args: VizArgs, type: string | undefined): VizArgs {
  const { type: _type, ...rest } = args;
  return type === undefined ? rest : { ...rest, type };
}

/** The code and the message of a tool error (tool.25): `code: message`. A text with no code is all message. */
export function toolError(result: CallResult): { code?: string; message: string } {
  const text = result.content?.find((c) => c.type === 'text')?.text ?? '';
  const match = /^([a-z][a-z-]*): ([\s\S]*)$/.exec(text);
  return match ? { code: match[1], message: match[2] } : { message: text };
}

/** The chart-changed notice of registry/notices.json, for a refresh that drew another chart (refresh.10). */
export function chartChanged(old: string, chart: string): VizNotice {
  const pattern = registry.notices.find((n) => n.code === 'chart-changed')?.message ?? '';
  return { code: 'chart-changed', message: pattern.replace('<old>', old).replace('<new>', chart) };
}

/**
 * Runs query_datasource again for a RefreshRequest (refresh.9 to refresh.12). `args` are the last arguments, and
 * `chart` the chart on screen. A time range pick gives new `from` and `to`. A `type` that no longer fits the data
 * gives the type-mismatch error; then the Source picks the chart, and the viz spec gets chart-changed when the chart
 * changed. `stale` stops before the second call when a newer request or tool call came. A call that throws rejects.
 */
export async function runVizRefresh(
  call: (args: VizArgs) => Promise<CallResult>,
  args: VizArgs,
  chart: string,
  request: RefreshRequest,
  stale: () => boolean,
): Promise<VizRefreshOutcome> {
  let next: VizArgs = request.cause === 'timeRange' ? { ...args, from: request.from, to: request.to } : args;
  let response = await call(next);
  const mismatch = !!response.isError && !!next.type && toolError(response).code === 'type-mismatch';
  if (mismatch && !stale()) {
    next = withType(next, undefined);
    response = await call(next);
  }
  if (response.isError) return { error: toolError(response).message };
  const viz = parseVizResult(extractToolPayload(response).payload);
  if (!viz) return { error: 'The App cannot read the new result.' };
  const notices = mismatch && viz.chart !== chart ? [chartChanged(chart, viz.chart)] : [];
  return { args: next, viz, notices, mismatch };
}
