import type { App } from '@modelcontextprotocol/ext-apps';
import { describe, expect, it, type Mock, vi } from 'vitest';

import { createAppHostState } from '../app/appHostState';
import { vectorSingleUnbounded } from './metricsFixtures';
import { readVizFixture } from './vizFixtures';

type Handlers = {
  ontoolinput?: (params: unknown) => void;
  ontoolresult?: (response: unknown) => void;
  onhostcontextchanged?: (context: unknown) => void;
};

const fixture = (file: string) => readVizFixture('prometheus-line', file);

function host(callServerTool: Mock = vi.fn()) {
  const app = {
    connect: () => Promise.resolve(),
    getHostContext: () => undefined,
    getHostCapabilities: () => ({ serverTools: {} }),
    callServerTool,
  } as unknown as App & Handlers;
  return { app, state: createAppHostState(app), callServerTool };
}

const ok = (payload: unknown) => ({ structuredContent: payload, content: [] });
const vizCall = { arguments: { toolName: 'query_datasource', arguments: fixture('args.json') } };

describe('createAppHostState with query_datasource', () => {
  it('keeps the result and the arguments of the viz spec', () => {
    const { app, state } = host();
    app.ontoolinput?.(vizCall);
    app.ontoolresult?.(ok(fixture('result.json')));
    const snapshot = state.getSnapshot();
    expect(snapshot.isError).toBe(false);
    expect(snapshot.viz?.chart).toBe('timeseries');
    expect(snapshot.vizArgs?.datasourceUid).toBe('lgtm-prometheus');
    expect(snapshot.result).toBeUndefined();
  });

  it('does not read a Prometheus payload as a viz result', () => {
    const { app, state } = host();
    app.ontoolinput?.(vizCall);
    app.ontoolresult?.(ok(vectorSingleUnbounded));
    expect(state.getSnapshot().viz).toBeUndefined();
    expect(state.getSnapshot().isError).toBe(true);
  });

  it('clears the viz view when the next call is another tool', () => {
    const { app, state } = host();
    app.ontoolinput?.(vizCall);
    app.ontoolresult?.(ok(fixture('result.json')));
    app.ontoolinput?.({ arguments: { toolName: 'query_prometheus', arguments: { expr: 'up', datasourceUid: 'prom' } } });
    expect(state.getSnapshot().viz).toBeUndefined();
    expect(state.getSnapshot().vizArgs).toBeUndefined();
    app.ontoolresult?.(ok(vectorSingleUnbounded));
    expect(state.getSnapshot().viz).toBeUndefined();
    expect(state.getSnapshot().result).toBeDefined();
  });

  it('keeps the time zone of the host context (spec.6)', () => {
    const { app, state } = host();
    app.onhostcontextchanged?.({ theme: 'dark', timeZone: 'Europe/Paris' });
    app.onhostcontextchanged?.({ theme: 'light' });
    expect(state.getSnapshot().timeZone).toBe('Europe/Paris');
    expect(state.getSnapshot().colorMode).toBe('light');
  });
});

describe('refreshViz', () => {
  function shown(callServerTool: Mock = vi.fn(async () => ok(fixture('result.json')))) {
    const h = host(callServerTool);
    h.app.ontoolinput?.(vizCall);
    h.app.ontoolresult?.(ok(fixture('result.json')));
    return h;
  }

  it('calls render_grafana_app with query_datasource and the last arguments (refresh.9)', async () => {
    const { state, callServerTool } = shown();
    const before = state.getSnapshot();

    expect(await state.refreshViz({ id: 'r1', cause: 'timeRange', from: 'now-6h', to: 'now' })).toBeUndefined();
    expect(callServerTool).toHaveBeenCalledWith({
      name: 'render_grafana_app',
      arguments: { toolName: 'query_datasource', arguments: { ...fixture('args.json'), from: 'now-6h', to: 'now' } },
    });
    const after = state.getSnapshot();
    expect(after.viz).not.toBe(before.viz);
    expect(after.vizArgs).toMatchObject({ from: 'now-6h', to: 'now' });
    expect(after.generation).toBe(before.generation);
  });

  it('sends the orgId of the tool input outside the arguments', async () => {
    const { app, state, callServerTool } = shown();
    app.ontoolinput?.({ arguments: { ...vizCall.arguments, orgId: 3 } });
    app.ontoolresult?.(ok(fixture('result.json')));
    await state.refreshViz({ id: 'r1', cause: 'refresh' });
    expect(callServerTool.mock.calls.at(-1)?.[0]).toMatchObject({ arguments: { orgId: 3 } });
  });

  it('gives the error and keeps the chart when the call fails (refresh.11)', async () => {
    const { state } = shown(vi.fn(async () => ({ isError: true, content: [{ type: 'text', text: 'boom' }] })));
    const before = state.getSnapshot();
    expect(await state.refreshViz({ id: 'r1', cause: 'refresh' })).toEqual({ error: 'boom' });
    expect(state.getSnapshot().viz).toBe(before.viz);
    expect(state.getSnapshot().isError).toBe(false);
  });

  it('gives the error when the host declines the call', async () => {
    const { state } = shown(vi.fn(async () => Promise.reject(new Error('The user declined.'))));
    expect(await state.refreshViz({ id: 'r1', cause: 'refresh' })).toEqual({ error: 'The user declined.' });
  });

  it('draws the last request only (refresh.14)', async () => {
    let finishFirst: (value: unknown) => void = () => {};
    const callServerTool = vi
      .fn()
      .mockImplementationOnce(() => new Promise((resolve) => (finishFirst = resolve)))
      .mockImplementationOnce(async () => ok({ ...fixture('result.json'), chart: 'barchart' }));
    const { state } = shown(callServerTool);

    const first = state.refreshViz({ id: 'r1', cause: 'refresh' });
    await state.refreshViz({ id: 'r2', cause: 'refresh' });
    finishFirst(ok(fixture('result.json')));

    expect(await first).toBeUndefined();
    expect(state.getSnapshot().viz?.chart).toBe('barchart');
  });

  it('drops a refresh that ends after a new tool call', async () => {
    let finish: (value: unknown) => void = () => {};
    const { app, state } = shown(vi.fn(() => new Promise((resolve) => (finish = resolve))));
    const pending = state.refreshViz({ id: 'r1', cause: 'refresh' });
    app.ontoolinput?.(vizCall);
    finish({ isError: true, content: [{ type: 'text', text: 'boom' }] });

    expect(await pending).toBeUndefined();
    expect(state.getSnapshot().viz).toBeUndefined();
  });
});

describe('pickVizChart', () => {
  const mismatch = { isError: true, content: [{ type: 'text', text: 'type-mismatch: barchart needs categories.' }] };

  function shown(callServerTool: Mock = vi.fn(async () => ok(fixture('result.json')))) {
    const h = host(callServerTool);
    h.app.ontoolinput?.(vizCall);
    h.app.ontoolresult?.(ok(fixture('result.json')));
    return h;
  }

  it('keeps an alternative of the result, and ignores a chart it does not have (spec.2)', () => {
    const { state } = shown();
    state.pickVizChart('nodeGraph');
    expect(state.getSnapshot().vizChart).toBeUndefined();
    state.pickVizChart('barchart');
    expect(state.getSnapshot().vizChart).toBe('barchart');
    expect(state.getSnapshot().vizArgs).toEqual(fixture('args.json'));
  });

  it('refreshes with the picked chart as type, apart from the last arguments (refresh.8, refresh.9)', async () => {
    const { state, callServerTool } = shown(vi.fn(async () => ok({ ...fixture('result.json'), chart: 'barchart' })));
    state.pickVizChart('barchart');
    expect(await state.refreshViz({ id: 'r1', cause: 'refresh' })).toBeUndefined();
    expect(callServerTool.mock.calls[0][0].arguments.arguments).toEqual({ ...fixture('args.json'), type: 'barchart' });
    expect(state.getSnapshot().vizArgs).toEqual(fixture('args.json'));
    expect(state.getSnapshot().vizChart).toBe('barchart');
  });

  it('keeps the type of the model in the last arguments', async () => {
    const { app, state } = shown(vi.fn(async () => ok({ ...fixture('result.json'), chart: 'barchart' })));
    const typed = { ...fixture('args.json'), type: 'timeseries' };
    app.ontoolinput?.({ arguments: { toolName: 'query_datasource', arguments: typed } });
    app.ontoolresult?.(ok(fixture('result.json')));
    state.pickVizChart('barchart');
    await state.refreshViz({ id: 'r1', cause: 'timeRange', from: 'now-6h', to: 'now' });
    expect(state.getSnapshot().vizArgs).toEqual({ ...typed, from: 'now-6h', to: 'now' });
  });

  it('drops the pick when the Source picks the chart after type-mismatch (refresh.10)', async () => {
    const callServerTool = vi.fn().mockResolvedValueOnce(mismatch).mockResolvedValueOnce(ok(fixture('result.json')));
    const { state } = shown(callServerTool);
    state.pickVizChart('barchart');
    await state.refreshViz({ id: 'r1', cause: 'refresh' });
    const snapshot = state.getSnapshot();
    expect(callServerTool.mock.calls[1][0].arguments.arguments).toEqual(fixture('args.json'));
    expect(snapshot.vizChart).toBeUndefined();
    expect(snapshot.vizNotices).toEqual([expect.objectContaining({ code: 'chart-changed' })]);
    expect(snapshot.vizNotices?.[0].message).toContain('barchart');
  });

  it('clears the notices of the last refresh (spec.8)', async () => {
    const callServerTool = vi.fn().mockResolvedValueOnce(mismatch).mockResolvedValueOnce(ok(fixture('result.json')));
    const { state } = shown(callServerTool);
    state.pickVizChart('barchart');
    await state.refreshViz({ id: 'r1', cause: 'refresh' });
    state.pickVizChart('histogram');
    expect(state.getSnapshot().vizNotices).toBeUndefined();
  });

  it('keeps a pick made while a refresh ran', async () => {
    let finish: (value: unknown) => void = () => {};
    const { state } = shown(vi.fn(() => new Promise((resolve) => (finish = resolve))));
    const pending = state.refreshViz({ id: 'r1', cause: 'refresh' });
    state.pickVizChart('histogram');
    finish(ok(fixture('result.json')));
    await pending;
    expect(state.getSnapshot().vizChart).toBe('histogram');
  });

  it('drops a pick made while a refresh ran when the new result does not have it (refresh.9)', async () => {
    let finish: (value: unknown) => void = () => {};
    const { state, callServerTool } = shown(vi.fn(() => new Promise((resolve) => (finish = resolve))));
    const pending = state.refreshViz({ id: 'r1', cause: 'refresh' });
    state.pickVizChart('histogram');
    finish(ok({ ...fixture('result.json'), alternatives: [] }));
    await pending;
    expect(state.getSnapshot().vizChart).toBeUndefined();
    callServerTool.mockResolvedValueOnce(ok(fixture('result.json')));
    await state.refreshViz({ id: 'r2', cause: 'refresh' });
    expect(callServerTool.mock.calls[1][0].arguments.arguments).toEqual(fixture('args.json'));
  });

  it('starts with no pick for a new tool call', () => {
    const { app, state } = shown();
    state.pickVizChart('barchart');
    app.ontoolinput?.(vizCall);
    app.ontoolresult?.(ok(fixture('result.json')));
    expect(state.getSnapshot().vizChart).toBeUndefined();
  });
});
