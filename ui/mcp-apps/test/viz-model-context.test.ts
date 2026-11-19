import type { App } from '@modelcontextprotocol/ext-apps';
import { describe, expect, it, type Mock, vi } from 'vitest';

import { createAppHostState } from '../app/appHostState';
import type { RenderState } from '../renderer/renderer.js';
import { vizFailureContext, vizModelContext } from '../app/vizModelContext';
import { parseVizArgs, parseVizResult } from '../app/vizSpec';
import { readVizFixture } from './vizFixtures';

type Handlers = { ontoolinput?: (params: unknown) => void; ontoolresult?: (response: unknown) => void };

const fixture = (file: string) => readVizFixture('prometheus-line', file);
const args = parseVizArgs(fixture('args.json'))!;
const viz = parseVizResult(fixture('result.json'))!;
const ready = { state: 'ready' as const, notices: [] };
const noData = { state: 'no-data' as const, notices: [] };
const ok = () => ({ structuredContent: fixture('result.json'), content: [] });
const iso = (ms: number) => new Date(ms).toISOString();

describe('vizModelContext', () => {
  it('names the chart, both ranges, the state, the error and the notices (state.15)', () => {
    const text = vizModelContext(args, viz, {
      state: 'error',
      error: { code: 'all-queries-failed', message: 'All queries failed.' },
      notices: [{ code: 'chart-changed', message: 'The chart is now a barchart.' }],
    });

    expect(text).toBe(
      [
        'The Grafana App shows a timeseries chart of data source lgtm-prometheus.',
        'Time range of the arguments: now-15m to now.',
        `Time range of the data: ${iso(viz.data.range.from)} to ${iso(viz.data.range.to)}.`,
        'State: error.',
        'Error: All queries failed.',
        'Notice: The chart is now a barchart.',
      ].join('\n'),
    );
  });

  it('says that no chart shows when the Renderer failed, with both ranges and the error', () => {
    expect(vizFailureContext(args, viz, 'This host cannot load the chart.')).toBe(
      [
        'The Grafana App could not show the timeseries chart of data source lgtm-prometheus.',
        'Time range of the arguments: now-15m to now.',
        `Time range of the data: ${iso(viz.data.range.from)} to ${iso(viz.data.range.to)}.`,
        'State: error.',
        'Error: This host cannot load the chart.',
      ].join('\n'),
    );
  });

  it('gives the default range of the Source for arguments with none', () => {
    const { from: _from, to: _to, ...untimed } = args;
    expect(vizModelContext(untimed as typeof args, viz, ready)).toContain(
      'Time range of the arguments: now-1h to now.',
    );
  });
});

describe('model context of the viz view (state.14)', () => {
  function shown(
    updateModelContext: Mock = vi.fn(async () => ({})),
    capabilities: object = { updateModelContext: {} },
  ) {
    const app = {
      connect: () => Promise.resolve(),
      getHostContext: () => undefined,
      getHostCapabilities: () => ({ serverTools: {}, ...capabilities }),
      callServerTool: vi.fn(async () => ok()),
      updateModelContext,
    } as unknown as App & Handlers;
    const state = createAppHostState(app);
    app.ontoolinput?.({ arguments: { toolName: 'query_datasource', arguments: fixture('args.json') } });
    app.ontoolresult?.(ok());
    const sent = () => updateModelContext.mock.calls.map(([params]) => params.content[0].text as string);
    // The generation of the view the Renderer drew in.
    const report = (renderState: RenderState) => state.reportVizState(renderState, state.getSnapshot().generation);
    return { app, state, updateModelContext, sent, report };
  }

  it('sends nothing for a first draw that is ready with no notices (state.16)', () => {
    const { updateModelContext, report } = shown();
    report(ready);
    expect(updateModelContext).not.toHaveBeenCalled();
  });

  it('sends a state that is not ready, and notices', () => {
    const { sent, report } = shown();
    report(noData);
    report({ state: 'ready', notices: [{ code: 'x', message: 'Some series are not shown.' }] });
    expect(sent()).toHaveLength(2);
    expect(sent()[0]).toContain('State: no-data.');
    expect(sent()[1]).toContain('Notice: Some series are not shown.');
  });

  it('replaces a sent context when the state changes back to ready (state.17)', () => {
    const { sent, report } = shown();
    report(noData);
    report(ready);
    expect(sent().at(-1)).toContain('State: ready.');
  });

  it('sends the new range after a time range refresh, with the last state', async () => {
    const { state, sent, report } = shown();
    report(ready);
    await state.refreshViz({ id: 'r1', cause: 'timeRange', from: 'now-6h', to: 'now' });
    expect(sent()).toEqual([expect.stringContaining('Time range of the arguments: now-6h to now.')]);
  });

  it('sends the chart that the user picked, with the last state', () => {
    const { state, sent, report } = shown();
    report(ready);
    state.pickVizChart('barchart');
    expect(sent()).toEqual([expect.stringContaining('The Grafana App shows a barchart chart')]);
    report(noData);
    expect(sent().at(-1)).toContain('shows a barchart chart');
  });

  it('sends a chart picked before the first state of the Renderer', () => {
    const { state, sent, report } = shown();
    state.pickVizChart('barchart');
    expect(sent()).toEqual([]);
    report(ready);
    expect(sent()).toEqual([expect.stringContaining('shows a barchart chart')]);
  });

  it('sends nothing when a chart picked before the first state is the chart of the result again', () => {
    const { state, updateModelContext, report } = shown();
    state.pickVizChart('barchart');
    state.pickVizChart('timeseries');
    report(ready);
    expect(updateModelContext).not.toHaveBeenCalled();
  });

  it('sends nothing after a refresh with the cause refresh', async () => {
    const { state, updateModelContext, report } = shown();
    report(ready);
    await state.refreshViz({ id: 'r1', cause: 'refresh' });
    expect(updateModelContext).not.toHaveBeenCalled();
  });

  it('clears a sent context for a new tool call, then starts again', () => {
    const { app, updateModelContext, report } = shown();
    report(noData);
    app.ontoolinput?.({ arguments: { toolName: 'query_datasource', arguments: fixture('args.json') } });
    app.ontoolresult?.(ok());
    report(ready);
    expect(updateModelContext).toHaveBeenCalledTimes(2);
    expect(updateModelContext).toHaveBeenLastCalledWith({ content: [] });
  });

  it('clears a sent context when the next call is not a viz call', () => {
    const { app, updateModelContext, report } = shown();
    report(noData);
    app.ontoolinput?.({ arguments: { toolName: 'query_prometheus', arguments: { expr: 'up' } } });
    expect(updateModelContext).toHaveBeenLastCalledWith({ content: [] });
  });

  it('clears nothing for a new tool call when no context was sent', () => {
    const { app, updateModelContext, report } = shown();
    report(ready);
    app.ontoolinput?.({ arguments: { toolName: 'query_datasource', arguments: fixture('args.json') } });
    app.ontoolresult?.(ok());
    expect(updateModelContext).not.toHaveBeenCalled();
  });

  it('drops a state of the Renderer of an older call', () => {
    const { app, state, updateModelContext } = shown();
    const old = state.getSnapshot().generation;
    app.ontoolresult?.(ok());
    state.reportVizState(noData, old);
    expect(updateModelContext).not.toHaveBeenCalled();
  });

  it('keeps a time range refresh that worked when its context cannot be built', async () => {
    const { app, state, updateModelContext, report } = shown();
    report(noData);
    updateModelContext.mockClear();
    const { range: _range, ...data } = fixture('result.json').data;
    vi.mocked(app.callServerTool).mockResolvedValue({
      structuredContent: { ...fixture('result.json'), data },
      content: [],
    });
    const failed = await state.refreshViz({ id: 'r1', cause: 'timeRange', from: 'now-6h', to: 'now' });
    expect(failed).toBeUndefined();
    expect(state.getSnapshot().vizArgs?.from).toBe('now-6h');
    expect(updateModelContext).not.toHaveBeenCalled();
  });

  it('sends nothing to a host without updateModelContext', () => {
    const { state, updateModelContext, report } = shown(undefined, {});
    report(noData);
    state.reportVizFailure('This host cannot load the chart.', state.getSnapshot().generation);
    expect(updateModelContext).not.toHaveBeenCalled();
  });

  it('sends a failed load or render, which no Renderer state follows', () => {
    const { state, sent } = shown();
    state.reportVizFailure('This host cannot load the chart.', state.getSnapshot().generation);
    expect(sent()).toEqual([expect.stringContaining('Error: This host cannot load the chart.')]);
  });

  it('drops a failure of the Renderer of an older call', () => {
    const { app, state, updateModelContext } = shown();
    const old = state.getSnapshot().generation;
    app.ontoolresult?.(ok());
    state.reportVizFailure('This host cannot load the chart.', old);
    expect(updateModelContext).not.toHaveBeenCalled();
  });
});
