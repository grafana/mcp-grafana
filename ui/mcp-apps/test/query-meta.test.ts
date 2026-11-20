// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { queryMeta, rangeLabel, seriesCount } from '../app/queryMeta';
import { parseVizArgs, parseVizResult, type VizArgs, type VizResult } from '../app/vizSpec';
import { readVizFixture as read } from './vizFixtures';

// prometheus-line: now-15m to now, 17 frames of one number field each, data.range 2026-09-25 16:09:37 to 16:24:37 UTC.
const args = parseVizArgs(read('prometheus-line', 'args.json'))!;
const viz = parseVizResult(read('prometheus-line', 'result.json'))!;
const withQuery = (patch: Record<string, unknown>): VizArgs => ({
  ...args,
  queries: [{ ...args.queries[0], ...patch }],
});

describe('queryMeta', () => {
  it('gives the range and the count of series', () => {
    expect(queryMeta(args, viz)).toBe('Last 15 minutes · 17 series');
  });

  it('gives the step when every query has the same intervalMs', () => {
    expect(queryMeta(withQuery({ intervalMs: 30_000 }), viz)).toBe('Last 15 minutes · Step 30s · 17 series');
    expect(queryMeta(withQuery({ intervalMs: 300_000 }), viz)).toBe('Last 15 minutes · Step 5m · 17 series');
    expect(queryMeta(withQuery({ intervalMs: 1_500 }), viz)).toBe('Last 15 minutes · Step 1500ms · 17 series');
    const differ = { ...args, queries: [{ refId: 'A', intervalMs: 30_000 }, { refId: 'B', intervalMs: 60_000 }] };
    expect(queryMeta(differ, viz)).toBe('Last 15 minutes · 17 series');
  });

  it('gives no step for an intervalMs that is not a whole number of milliseconds', () => {
    expect(queryMeta(withQuery({ intervalMs: 1_500.5 }), viz)).toBe('Last 15 minutes · 17 series');
  });

  it('leaves out an entry that is not an object', () => {
    const queries = [null, 'up', { refId: 'A', instant: true }] as unknown as VizArgs['queries'];
    expect(queryMeta({ ...args, queries }, viz)).toBe('Instant query · 17 series');
    expect(queryMeta({ ...args, queries: [null] as unknown as VizArgs['queries'] }, viz)).toBe(
      'Last 15 minutes · 17 series',
    );
  });

  it('names an instant query in place of the range and the step', () => {
    expect(queryMeta(withQuery({ instant: true, intervalMs: 30_000 }), viz)).toBe('Instant query · 17 series');
    expect(queryMeta(withQuery({ instant: true, range: true }), viz)).toBe('Last 15 minutes · 17 series');
    expect(queryMeta(withQuery({ expr: 'count_over_time({job="app"}[5m])', queryType: 'instant' }), viz)).toBe(
      'Instant query · 17 series',
    );
    expect(queryMeta(withQuery({ queryType: 'range' }), viz)).toBe('Last 15 minutes · 17 series');
  });
});

describe('rangeLabel', () => {
  it('names a range that ends now', () => {
    expect(rangeLabel({ ...args, from: 'now-1d', to: 'now' }, viz)).toBe('Last 1 day');
    expect(rangeLabel({ ...args, from: 'now-6h', to: 'now' }, viz)).toBe('Last 6 hours');
    expect(rangeLabel({ ...args, from: 'now-90s', to: 'now' }, viz)).toBe('Last 90 seconds');
  });

  it('names the defaults of the Source when the arguments have no range (spec.17)', () => {
    const { from: _from, to: _to, ...rest } = args;
    expect(rangeLabel(rest as VizArgs, viz)).toBe('Last 1 hour');
  });

  it('gives the absolute range of the data for any other range, in the time zone of the host', () => {
    expect(rangeLabel({ ...args, from: 1790352577294, to: 1790353477294 }, viz, 'UTC')).toBe(
      '2026-09-25 16:09:37 to 2026-09-25 16:24:37',
    );
    expect(rangeLabel({ ...args, from: 'now-2h', to: 'now-1h' }, viz, 'Europe/Bucharest')).toBe(
      '2026-09-25 19:09:37 to 2026-09-25 19:24:37',
    );
    expect(rangeLabel({ ...args, from: 'now-1d/d', to: 'now' }, viz, 'UTC')).toBe(
      '2026-09-25 16:09:37 to 2026-09-25 16:24:37',
    );
  });

  it('uses the time zone of the browser for an unknown time zone', () => {
    expect(rangeLabel({ ...args, from: 0, to: 1 }, viz, 'Not/AZone')).toMatch(
      /^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d to \d{4}-\d\d-\d\d \d\d:\d\d:\d\d$/,
    );
  });
});

describe('seriesCount', () => {
  const frame = (...types: string[]) => ({ schema: { fields: types.map((type) => ({ type })) } });
  const result = (results: Record<string, unknown>) => ({ ...viz, data: { ...viz.data, results } }) as VizResult;

  it('counts the number fields of all frames', () => {
    const results = { A: { frames: [frame('time', 'number', 'number')] }, B: { frames: [frame('string', 'number')] } };
    expect(seriesCount(result(results))).toBe(3);
  });

  it('skips annotation frames, such as Prometheus exemplars', () => {
    const exemplars = { schema: { meta: { dataTopic: 'annotations' }, ...frame('time', 'number').schema } };
    expect(seriesCount(result({ A: { frames: [frame('time', 'number'), exemplars] } }))).toBe(1);
  });

  it('counts nothing in results with no frames', () => {
    expect(seriesCount(result({ A: { error: 'bad query' }, B: null, C: { frames: [{}] } }))).toBe(0);
  });
});
