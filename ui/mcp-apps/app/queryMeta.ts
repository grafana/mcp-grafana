import constants from '@grafana/viz-contract/registry/constants.json';

import type { VizArgs, VizResult } from './vizSpec';

const UNITS: Record<string, [one: string, many: string]> = {
  s: ['second', 'seconds'],
  m: ['minute', 'minutes'],
  h: ['hour', 'hours'],
  d: ['day', 'days'],
  w: ['week', 'weeks'],
  M: ['month', 'months'],
  y: ['year', 'years'],
};

/** The largest unit that divides a step, so a step reads as `Step 30s`, not `Step 30000ms`. */
const STEP_UNITS: Array<[suffix: string, ms: number]> = [
  ['d', 86_400_000],
  ['h', 3_600_000],
  ['m', 60_000],
  ['s', 1_000],
  ['ms', 1],
];

/**
 * The meta line of the query block: the time range, the step when the model set one, and the count of series.
 * An instant query shows `Instant query` in place of the range and the step.
 */
export function queryMeta(args: VizArgs, viz: VizResult, timeZone?: string): string {
  const queries = objects(args.queries);
  const instant = queries.length > 0 && queries.every(isInstant);
  const step = instant ? undefined : stepLabel(queries);
  const series = seriesCount(viz);
  return [
    instant ? 'Instant query' : rangeLabel(args, viz, timeZone),
    ...(step ? [`Step ${step}`] : []),
    `${series} series`,
  ].join(' · ');
}

/**
 * `Last 6 hours` for a range from `now-6h` to `now`. Any other range, for example a pick in the time picker, gives the
 * absolute range of the data (tool.14). Arguments with no `from` or `to` use the defaults of the Source (spec.17).
 */
export function rangeLabel(args: VizArgs, viz: VizResult, timeZone?: string): string {
  const from = args.from ?? constants.defaultFrom.value;
  const to = args.to ?? constants.defaultTo.value;
  const match = to === 'now' && typeof from === 'string' ? /^now-(\d+)([smhdwMy])$/.exec(from) : null;
  if (match) {
    const count = Number(match[1]);
    const [one, many] = UNITS[match[2]];
    return `Last ${count} ${count === 1 ? one : many}`;
  }
  return `${formatTime(viz.data.range.from, timeZone)} to ${formatTime(viz.data.range.to, timeZone)}`;
}

/** The `intervalMs` that every query has, as `30s` or `5m`; none when the queries have none or differ. */
function stepLabel(queries: Array<Record<string, unknown>>): string | undefined {
  const [first, ...rest] = queries.map((query) => query.intervalMs);
  // A fraction of a millisecond has no unit that divides it.
  if (typeof first !== 'number' || !Number.isInteger(first) || first <= 0 || rest.some((ms) => ms !== first)) {
    return undefined;
  }
  const [suffix, ms] = STEP_UNITS.find(([, unit]) => first % unit === 0)!;
  return `${first / ms}${suffix}`;
}

/** An instant query: Prometheus sets `instant` with no `range`, Loki sets `queryType`. */
const isInstant = (query: Record<string, unknown>) =>
  (query.instant === true && query.range !== true) || query.queryType === 'instant';

/** The queries that are objects: the arguments are the model's, and the view does not check them (see queryTexts). */
const objects = (queries: unknown[]): Array<Record<string, unknown>> =>
  queries.filter(
    (query): query is Record<string, unknown> => typeof query === 'object' && query !== null && !Array.isArray(query),
  );

/** The series of the result: the number fields of all frames, as the Renderer counts them (state.19). */
export function seriesCount(viz: VizResult): number {
  let count = 0;
  for (const result of Object.values(viz.data.results)) {
    const frames = (result as { frames?: unknown } | null)?.frames;
    if (!Array.isArray(frames)) continue;
    for (const frame of frames) {
      const fields = (frame as { schema?: { fields?: unknown } } | null)?.schema?.fields;
      if (Array.isArray(fields)) count += fields.filter((field) => field?.type === 'number').length;
    }
  }
  return count;
}

/** `2026-11-18 14:05:00`, in the time zone of the host, as the time picker of the Renderer shows a range. */
function formatTime(ms: number, timeZone?: string): string {
  let format: Intl.DateTimeFormat;
  try {
    format = dateFormat(timeZone);
  } catch {
    // An unknown time zone throws; the browser's zone is the Renderer's fallback too (spec.6).
    format = dateFormat(undefined);
  }
  const part = Object.fromEntries(format.formatToParts(ms).map(({ type, value }) => [type, value]));
  return `${part.year}-${part.month}-${part.day} ${part.hour}:${part.minute}:${part.second}`;
}

const dateFormat = (timeZone?: string) =>
  new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  });
