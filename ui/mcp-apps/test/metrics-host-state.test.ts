import type { App } from '@modelcontextprotocol/ext-apps';
import { describe, expect, it, vi } from 'vitest';

import { createMetricsHostState } from '../app/metricsHostState';
import { matrixSingleSeries, vectorSingleUnbounded } from './metricsFixtures';

type Handlers = {
  ontoolinput?: (params: unknown) => void;
  ontoolresult?: (response: unknown) => void;
};

/** A host whose server-tool calls resolve only when the test says so. */
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
const args = { expr: 'up', datasourceUid: 'prom', queryType: 'instant', endTime: 'now' };

describe('createMetricsHostState', () => {
  it('drops a refresh that returns after the host delivered a newer result', async () => {
    const { app, resolveCall } = fakeHost();
    const host = createMetricsHostState(app);
    app.ontoolinput?.({ arguments: args });
    app.ontoolresult?.(ok(vectorSingleUnbounded));

    const refreshing = host.refresh();
    app.ontoolresult?.(ok(matrixSingleSeries));
    const newer = host.getSnapshot().result;

    resolveCall(ok(vectorSingleUnbounded));
    await refreshing;
    expect(host.getSnapshot().result).toBe(newer);
    expect(host.getSnapshot().refreshing).toBe(false);
  });

  it('clears the last action notice when a new result arrives', async () => {
    const { app, resolveCall } = fakeHost();
    const host = createMetricsHostState(app);
    app.ontoolinput?.({ arguments: args });
    app.ontoolresult?.(ok(vectorSingleUnbounded));

    const refreshing = host.refresh();
    resolveCall({ isError: true, content: [] });
    await refreshing;
    expect(host.getSnapshot().notice?.tone).toBe('error');

    const generation = host.getSnapshot().generation;
    const listener = vi.fn();
    host.subscribe(listener);
    app.ontoolresult?.(ok(matrixSingleSeries));
    expect(host.getSnapshot().notice).toBeUndefined();
    expect(host.getSnapshot().generation).toBeGreaterThan(generation);
    // One update, so the view never remounts on the previous result.
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it('drops the previous chart when the host starts a new call', () => {
    const { app } = fakeHost();
    const host = createMetricsHostState(app);
    app.ontoolinput?.({ arguments: args });
    app.ontoolresult?.(ok(vectorSingleUnbounded));
    expect(host.getSnapshot().result).toBeDefined();

    app.ontoolinput?.({ arguments: { ...args, expr: 'node_load1' } });
    expect(host.getSnapshot().result).toBeUndefined();
    expect(host.getSnapshot().status).toBe('Waiting for query results…');
  });
});
