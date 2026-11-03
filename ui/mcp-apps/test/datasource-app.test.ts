import type { App } from '@modelcontextprotocol/ext-apps';
import { describe, expect, it, vi } from 'vitest';

import { createAppHostState } from '../app/appHostState';
import sources from '../app/appSources.json';
import { parseDatasourceResult, vizSpecFrom, type DatasourcePayload } from '../app/datasourceRenderer';

type Handlers = {
  ontoolinput?: (params: unknown) => void;
  ontoolresult?: (response: unknown) => void;
};

function fakeHost() {
  let resolveCall: (response: unknown) => void = () => {};
  const app = {
    connect: () => Promise.resolve(),
    getHostContext: () => undefined,
    callServerTool: vi.fn(() => new Promise((resolve) => (resolveCall = resolve))),
  } as unknown as App & Handlers;
  return { app, resolveCall: (response: unknown) => resolveCall(response) };
}

const ok = (payload: unknown) => ({ structuredContent: payload, content: [] });

const query = {
  refId: 'A',
  datasource: { uid: 'prom' },
  expr: 'sum by (handler) (rate(x[5m]))',
  legendFormat: '{{handler}}',
  maxDataPoints: 1000,
  intervalMs: 1000,
};

function payload(overrides: Partial<DatasourcePayload> = {}): DatasourcePayload {
  return {
    request: { from: 'now-1h', to: 'now', queries: [query] },
    range: { from: 1700000000000, to: 1700003600000 },
    capturedAt: 1700003600000,
    results: { A: { frames: [] } },
    panel: { type: 'grafana-echartscartesian-panel', options: { seriesType: 'line' } },
    pick: { rule: 'time-series', type: 'grafana-echartscartesian-panel' },
    ...overrides,
  };
}

const args = { datasourceUid: 'prom', queries: [{ expr: query.expr, legendFormat: query.legendFormat }], from: 'now-1h', to: 'now' };

describe('parseDatasourceResult', () => {
  it('accepts the query_datasource payload', () => {
    expect(parseDatasourceResult(payload())).toEqual(payload());
  });

  it.each([
    ['not an object', 'x'],
    ['no queries', { ...payload(), request: { from: 'now-1h', to: 'now' } }],
    ['relative range', { ...payload(), range: { from: 'now-1h', to: 'now' } }],
    ['no panel type', { ...payload(), panel: {} }],
    ['a Prometheus result', { data: [] }],
  ])('rejects %s', (_, value) => {
    expect(parseDatasourceResult(value)).toBeUndefined();
  });
});

describe('vizSpecFrom', () => {
  it('builds a savable panel from the request and the picked type', () => {
    const spec = vizSpecFrom(payload(), 'dark') as Record<string, any>;
    expect(spec.specVersion).toBe(1);
    expect(spec.theme).toBe('dark');
    expect(spec.panel.type).toBe('grafana-echartscartesian-panel');
    expect(spec.panel.options).toEqual({ seriesType: 'line' });
    expect(spec.panel.datasource).toEqual({ uid: 'prom' });
    // Targets are the queries without the request-only resolution fields.
    expect(spec.panel.targets).toEqual([
      { refId: 'A', datasource: { uid: 'prom' }, expr: query.expr, legendFormat: '{{handler}}' },
    ]);
    expect(spec.panel.maxDataPoints).toBe(1000);
    expect(spec.request).toEqual({ from: '1700000000000', to: '1700003600000', queries: [query] });
    expect(spec.data.range).toEqual({ from: 1700000000000, to: 1700003600000 });
  });
});

describe('createAppHostState with query_datasource', () => {
  it('routes the result to the datasource view', () => {
    const { app } = fakeHost();
    const host = createAppHostState(app);
    app.ontoolinput?.({ arguments: { toolName: sources.datasource, arguments: args } });
    app.ontoolresult?.(ok(payload()));
    expect(host.getSnapshot().datasource).toEqual(payload());
    expect(host.getSnapshot().result).toBeUndefined();
    expect(host.getSnapshot().isError).toBe(false);
  });

  it('reports a payload it cannot read', () => {
    const { app } = fakeHost();
    const host = createAppHostState(app);
    app.ontoolinput?.({ arguments: { toolName: sources.datasource, arguments: args } });
    app.ontoolresult?.(ok({ data: [] }));
    expect(host.getSnapshot().datasource).toBeUndefined();
    expect(host.getSnapshot().isError).toBe(true);
  });

  it('refreshes through render_grafana_app and keeps a new range for the next refresh', async () => {
    const { app, resolveCall } = fakeHost();
    const host = createAppHostState(app);
    app.ontoolinput?.({ arguments: { toolName: sources.datasource, arguments: args, orgId: 3 } });
    app.ontoolresult?.(ok(payload()));

    const shifted = host.refreshDatasource({ from: '1699996400000', to: '1700000000000' });
    expect(app.callServerTool).toHaveBeenCalledWith({
      name: 'render_grafana_app',
      arguments: {
        toolName: sources.datasource,
        arguments: { ...args, from: '1699996400000', to: '1700000000000' },
        orgId: 3,
      },
    });
    resolveCall(ok(payload({ range: { from: 1699996400000, to: 1700000000000 } })));
    expect(await shifted).toBe(true);
    expect(host.getSnapshot().datasource?.range.from).toBe(1699996400000);

    const refreshed = host.refreshDatasource({});
    expect(app.callServerTool).toHaveBeenLastCalledWith({
      name: 'render_grafana_app',
      arguments: {
        toolName: sources.datasource,
        arguments: { ...args, from: '1699996400000', to: '1700000000000' },
        orgId: 3,
      },
    });
    resolveCall(ok(payload()));
    expect(await refreshed).toBe(true);
    expect(host.getSnapshot().refreshing).toBe(false);
  });

  it('reports a failed refresh and keeps the chart', async () => {
    const { app, resolveCall } = fakeHost();
    const host = createAppHostState(app);
    app.ontoolinput?.({ arguments: { toolName: sources.datasource, arguments: args } });
    app.ontoolresult?.(ok(payload()));
    const before = host.getSnapshot().datasource;

    const refreshing = host.refreshDatasource({});
    resolveCall({ isError: true, content: [] });
    expect(await refreshing).toBe(false);
    expect(host.getSnapshot().datasource).toBe(before);
  });

  it('does not refresh another source through the datasource path', async () => {
    const { app } = fakeHost();
    const host = createAppHostState(app);
    app.ontoolinput?.({ arguments: { toolName: sources.metrics, arguments: { expr: 'up', datasourceUid: 'prom' } } });
    expect(await host.refreshDatasource({})).toBe(false);
    expect(app.callServerTool).not.toHaveBeenCalled();
  });
});
