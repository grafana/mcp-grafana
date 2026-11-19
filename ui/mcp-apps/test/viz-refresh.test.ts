import { describe, expect, it, vi } from 'vitest';

import { chartChanged, runVizRefresh, toolError, withType } from '../app/vizRefresh';
import type { VizArgs } from '../app/vizSpec';
import { readVizFixture } from './vizFixtures';

const args: VizArgs = { ...readVizFixture('prometheus-line', 'args.json'), type: 'timeseries' };
const result = readVizFixture('prometheus-line', 'result.json');
const ok = (payload: unknown = result) => ({ structuredContent: payload, content: [] });
const failed = (text: string) => ({ isError: true, content: [{ type: 'text' as const, text }] });
const refresh = { id: 'r1', cause: 'refresh' } as const;
const never = () => false;

describe('runVizRefresh', () => {
  it('sends the last arguments again for Refresh (refresh.9)', async () => {
    const call = vi.fn(async () => ok());
    const outcome = await runVizRefresh(call, args, 'timeseries', refresh, never);

    expect(call).toHaveBeenCalledWith(args);
    expect(outcome).toMatchObject({ args, notices: [] });
    expect('viz' in outcome && outcome.viz.chart).toBe('timeseries');
  });

  it('sends the picked range and keeps it as the last arguments (refresh.9)', async () => {
    const call = vi.fn(async () => ok());
    const outcome = await runVizRefresh(
      call,
      args,
      'timeseries',
      { id: 'r2', cause: 'timeRange', from: 'now-6h', to: 'now' },
      never,
    );

    expect(call).toHaveBeenCalledWith({ ...args, from: 'now-6h', to: 'now' });
    expect(outcome).toMatchObject({ args: { from: 'now-6h', to: 'now' } });
  });

  it('calls again with no type after type-mismatch, and says the chart changed (refresh.10)', async () => {
    const call = vi
      .fn()
      .mockResolvedValueOnce(failed('type-mismatch: timeseries needs a time field. Charts that fit: barchart.'))
      .mockResolvedValueOnce(ok({ ...result, chart: 'barchart' }));
    const outcome = await runVizRefresh(call, args, 'timeseries', refresh, never);

    const { type: _type, ...untyped } = args;
    expect(call).toHaveBeenLastCalledWith(untyped);
    expect(outcome).toMatchObject({ args: untyped, notices: [chartChanged('timeseries', 'barchart')], mismatch: true });
  });

  it('adds no notice when the picked chart is the old one', async () => {
    const call = vi.fn().mockResolvedValueOnce(failed('type-mismatch: …')).mockResolvedValueOnce(ok());
    expect(await runVizRefresh(call, args, 'timeseries', refresh, never)).toMatchObject({ notices: [] });
  });

  it('adds no notice when the Source picks another chart with no type', async () => {
    const { type: _type, ...untyped } = args;
    const call = vi.fn(async () => ok({ ...result, chart: 'barchart' }));
    expect(await runVizRefresh(call, untyped as VizArgs, 'timeseries', refresh, never)).toMatchObject({ notices: [] });
  });

  it('does not call again when a newer request came', async () => {
    const call = vi.fn(async () => failed('type-mismatch: …'));
    await runVizRefresh(call, args, 'timeseries', refresh, () => true);
    expect(call).toHaveBeenCalledTimes(1);
  });

  it('gives the message of another tool error (refresh.11)', async () => {
    const call = vi.fn(async () => failed('too-large: The result is larger than 1 MiB.'));
    expect(await runVizRefresh(call, args, 'timeseries', refresh, never)).toEqual({
      error: 'The result is larger than 1 MiB.',
    });
  });

  it('gives an error for a result that is not a viz result', async () => {
    const call = vi.fn(async () => ok({ data: [] }));
    expect(await runVizRefresh(call, args, 'timeseries', refresh, never)).toEqual({
      error: 'The App cannot read the new result.',
    });
  });
});

describe('withType', () => {
  it('sets or removes the type, and keeps the other arguments', () => {
    expect(withType(args, 'barchart')).toEqual({ ...args, type: 'barchart' });
    expect('type' in withType(args, undefined)).toBe(false);
    expect(withType(args, undefined).queries).toBe(args.queries);
  });
});

describe('toolError', () => {
  it('splits the code from the message (tool.25)', () => {
    expect(toolError(failed('no-data: Nothing.'))).toEqual({ code: 'no-data', message: 'Nothing.' });
    expect(toolError(failed('the data source is down'))).toEqual({ message: 'the data source is down' });
  });
});

describe('chartChanged', () => {
  it('fills the message of registry/notices.json', () => {
    expect(chartChanged('timeseries', 'barchart')).toEqual({
      code: 'chart-changed',
      message: 'The timeseries chart does not fit the new data. The chart is now a barchart.',
    });
  });
});
