import type { App } from '@modelcontextprotocol/ext-apps';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

import { createAppHostState } from '../app/appHostState';
import { vectorSingleUnbounded } from './metricsFixtures';

type Handlers = {
  ontoolinput?: (params: unknown) => void;
  ontoolresult?: (response: unknown) => void;
  onhostcontextchanged?: (context: unknown) => void;
};

const fixture = (file: string) =>
  JSON.parse(readFileSync(join(__dirname, 'viz-contract', 'prometheus-line', file), 'utf8'));

function host() {
  const app = { connect: () => Promise.resolve(), getHostContext: () => undefined } as unknown as App & Handlers;
  return { app, state: createAppHostState(app) };
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
