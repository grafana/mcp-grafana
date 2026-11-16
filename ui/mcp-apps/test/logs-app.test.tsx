import type { App } from '@modelcontextprotocol/ext-apps';
import { fireEvent, render, screen, cleanup } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createAppHostState } from '../app/appHostState';
import { GrafanaApplication } from '../app/GrafanaApplication';
import { LogsView } from '../src/logs/LogsView';
import { parseLogsResult, type LogsViewResult } from '../src/logs/parseLogsResult';
import { vectorSingleUnbounded } from './metricsFixtures';
import { readVizFixture } from './vizFixtures';

afterEach(cleanup);

const args = {
  datasourceUid: 'loki',
  logql: '{app="api"}',
  startRfc3339: 'now-15m',
  endRfc3339: 'now',
  limit: 100,
  direction: 'backward',
  format: 'compact',
};
const payload: LogsViewResult = {
  kind: 'logs',
  query: args.logql,
  exploreUrl: 'https://example.org/explore',
  logs: {
    data: [
      {
        timestamp: '2026-11-13T10:00:00.000000001Z',
        line: '<script>literal</script>',
        labels: { app: 'api' },
        parsed: { level: 'error' },
        structuredMetadata: { trace_id: 'abc' },
      },
    ],
    metadata: { linesReturned: 1, resultsTruncated: true, startTime: 'now-15m', endTime: 'now' },
  },
};
const ok = (structuredContent: unknown) => ({ structuredContent, content: [] });
function fakeHost() {
  let resolveCall: (response: unknown) => void = () => {};
  const app = {
    connect: vi.fn(() => Promise.resolve()),
    getHostContext: () => undefined,
    close: vi.fn(),
    openLink: vi.fn(() => Promise.resolve({})),
    callServerTool: vi.fn(
      () =>
        new Promise((resolve) => {
          resolveCall = resolve;
        })
    ),
  } as unknown as App;
  const host = createAppHostState(app);
  app.ontoolinput?.({ arguments: { toolName: 'query_loki_logs', orgId: 42, arguments: args } });
  return { app, host, resolveCall: (response: unknown) => resolveCall(response) };
}

describe('logs app', () => {
  it('validates the display contract', () => {
    expect(parseLogsResult(payload)).toEqual(payload);
    expect(parseLogsResult({ ...payload, logs: { data: 'bad' } })).toBeUndefined();
    expect(parseLogsResult({ kind: 'patterns', patterns: [] })).toBeUndefined();
  });

  it('renders literal lines, nanosecond timestamps, severity, fields and truncation', () => {
    render(<LogsView result={payload} />);
    expect(screen.getAllByText('<script>literal</script>')[0].textContent).toBe('<script>literal</script>');
    expect(document.querySelector('script')).toBeNull();
    expect(screen.getByText('2026-11-13T10:00:00.000000001Z')).toBeTruthy();
    expect(screen.getByText('ERROR')).toBeTruthy();
    expect(screen.getByRole('status').textContent).toContain('limit');
    fireEvent.click(screen.getByTitle('Expand or collapse log details'));
    for (const title of ['Indexed labels', 'Parsed fields', 'Structured metadata']) {
      fireEvent.click(screen.getByText(title));
    }
    expect(screen.getByRole('table', { name: 'Indexed labels' })).toBeTruthy();
    expect(screen.getByRole('table', { name: 'Structured metadata' }).textContent).toContain('trace_idabc');
    expect(screen.getByRole('table', { name: 'Parsed fields' }).textContent).toContain('levelerror');
  });

  it('keeps empty messages as rows and explains empty and metric results', () => {
    const view = render(<LogsView result={{ ...payload, logs: { data: [{ labels: {} }] } }} />);
    expect(screen.getAllByTitle('Expand or collapse log details')).toHaveLength(1);
    view.rerender(<LogsView result={{ ...payload, logs: { data: [] } }} />);
    expect(screen.getByText('No logs matched this query.')).toBeTruthy();
    view.rerender(<LogsView result={{ ...payload, logs: { data: [{ labels: {}, value: 5 }] } }} />);
    expect(screen.getByText(/returned metric samples/)).toBeTruthy();
  });

  it('routes the early host result to logs and opens Grafana through the host', () => {
    const { app, host } = fakeHost();
    app.ontoolresult?.(ok(payload));
    render(<GrafanaApplication host={host} />);
    expect(screen.getByText('Grafana Logs')).toBeTruthy();
    expect(screen.getByText(/1 log line/)).toBeTruthy();
    fireEvent.click(screen.getByRole('link', { name: /Open in Grafana/ }));
    expect(app.openLink).toHaveBeenCalledWith({ url: payload.exploreUrl });
  });

  it('refreshes with every input and the outer org, and ignores superseded calls', async () => {
    const { app, host, resolveCall } = fakeHost();
    app.ontoolresult?.(ok(payload));
    const pending = host.refresh();
    await host.refresh(); // No duplicate concurrent query.
    expect(app.callServerTool).toHaveBeenCalledTimes(1);
    expect(app.callServerTool).toHaveBeenCalledWith({
      name: 'query_loki_logs',
      arguments: { ...args, orgId: 42 },
    });
    app.ontoolinput?.({
      arguments: { toolName: 'query_prometheus', arguments: { expr: 'up', datasourceUid: 'prom' } },
    });
    app.ontoolresult?.(ok(vectorSingleUnbounded));
    resolveCall(ok(payload));
    await pending;
    expect(host.getSnapshot().logs).toBeUndefined();
    expect(host.getSnapshot().result).toBeDefined();
    expect(host.getSnapshot().refreshing).toBe(false);
  });

  it('retries an initial error and replaces it with logs', async () => {
    const { app, host, resolveCall } = fakeHost();
    app.ontoolresult?.({ isError: true, content: [] });
    const view = render(<GrafanaApplication host={host} />);
    expect(screen.getByRole('alert')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    resolveCall(ok(payload));
    await vi.waitFor(() => expect(host.getSnapshot().isError).toBe(false));
    view.rerender(<GrafanaApplication host={host} />);
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getAllByText('<script>literal</script>')[0].textContent).toBe('<script>literal</script>');
  });

  it('preserves displayed logs when refresh fails, then recovers on the next refresh', async () => {
    const { app, host, resolveCall } = fakeHost();
    app.ontoolresult?.(ok(payload));
    const pending = host.refresh();
    resolveCall({ isError: true, content: [] });
    await pending;
    expect(host.getSnapshot().logs).toEqual(payload);
    expect(host.getSnapshot().notice?.tone).toBe('error');
    const retry = host.refresh();
    resolveCall(ok({ ...payload, logs: { data: [] } }));
    await retry;
    expect(host.getSnapshot().logs?.logs.data).toEqual([]);
    expect(host.getSnapshot().notice).toBeUndefined();
  });
  it('clears previous logs for unreadable results and cancellation', () => {
    const { app, host } = fakeHost();
    app.ontoolresult?.(ok(payload));
    app.ontoolresult?.(ok({ kind: 'logs', logs: 'invalid' }));
    expect(host.getSnapshot().logs).toBeUndefined();
    expect(host.getSnapshot().isError).toBe(true);
    app.ontoolresult?.(ok(payload));
    app.ontoolcancelled?.({ reason: 'cancelled' });
    expect(host.getSnapshot().logs).toBeUndefined();
    expect(host.getSnapshot().status).toContain('cancelled');
  });

  it('omits unsafe navigation URLs', () => {
    const { app, host } = fakeHost();
    app.ontoolresult?.(ok({ ...payload, exploreUrl: 'javascript:alert(1)' }));
    render(<GrafanaApplication host={host} />);
    expect(screen.queryByRole('link', { name: /Open in Grafana/ })).toBeNull();
  });

  it('shows only the three collapsed field groups in order', () => {
    render(<LogsView result={payload} />);
    fireEvent.click(screen.getByTitle('Expand or collapse log details'));
    const groups = [...document.querySelectorAll('details details')];
    expect(groups.map((group) => group.querySelector('summary')?.textContent?.trim())).toEqual([
      'Indexed labels 1',
      'Parsed fields 1',
      'Structured metadata 1',
    ]);
    expect(groups.every((group) => !group.hasAttribute('open'))).toBe(true);
    expect(screen.queryByRole('searchbox')).toBeNull();
    expect(screen.queryByText('Log line')).toBeNull();
  });
  it('clears logs when switching to Dataviz and clears Dataviz when switching back', () => {
    const { app, host } = fakeHost();
    app.ontoolresult?.(ok(payload));
    app.ontoolinput?.({ arguments: { toolName: 'query_datasource', arguments: readVizFixture('prometheus-line', 'args.json') } });
    expect(host.getSnapshot().logs).toBeUndefined();
    app.ontoolresult?.(ok(readVizFixture('prometheus-line', 'result.json')));
    expect(host.getSnapshot().viz?.chart).toBe('timeseries');
    app.ontoolinput?.({ arguments: { toolName: 'query_loki_logs', arguments: args } });
    expect(host.getSnapshot().viz).toBeUndefined();
    app.ontoolresult?.(ok(payload));
    expect(host.getSnapshot().logs).toEqual(payload);
  });

});
