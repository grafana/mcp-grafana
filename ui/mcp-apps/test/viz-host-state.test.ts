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
