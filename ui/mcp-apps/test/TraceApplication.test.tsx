import type { App } from '@modelcontextprotocol/ext-apps';
import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TraceApplication } from '../app/TraceApplication';
import { parseTraceResult } from '../app/parseTraceResult';

afterEach(cleanup);
const result = {
  traceId: 'abc',
  datasourceUid: 'tempo',
  grafanaUrl: 'https://grafana.example/explore',
  spans: [
    {
      id: '1',
      name: 'GET /',
      serviceName: 'frontend',
      startTimeMs: 0,
      durationMs: 20,
      status: 'ok',
      attributes: {},
      events: [],
    },
  ],
};
function host() {
  const app = {
    connect: vi.fn(async () => {}),
    close: vi.fn(async () => {}),
    getHostContext: () => ({ theme: 'dark' }),
    openLink: vi.fn(async () => ({})),
  } as unknown as App;
  return app;
}
describe('trace host bridge', () => {
  it('registers result handlers before connecting and reacts to host themes', async () => {
    const app = host();
    vi.mocked(app.connect).mockImplementation(async () => {
      expect(app.ontoolresult).toBeTypeOf('function');
      await app.ontoolresult?.({ content: [], structuredContent: result });
    });
    const { unmount } = render(<TraceApplication app={app} />);
    await waitFor(() => expect(screen.getByRole('article').getAttribute('data-color-mode')).toBe('dark'));
    act(() => app.onhostcontextchanged?.({ theme: 'light' }));
    expect(screen.getByRole('article').getAttribute('data-color-mode')).toBe('light');
    unmount();
    expect(app.close).toHaveBeenCalledOnce();
  });
  it('shows a useful host failure instead of an endless loading state', async () => {
    const app = host();
    vi.mocked(app.connect).mockRejectedValue(new Error('connection refused'));
    render(<TraceApplication app={app} />);
    expect((await screen.findByRole('alert')).textContent).toContain('Could not connect');
  });
  it('renders a trace without a Grafana link and hides navigation and sharing', async () => {
    const app = host();
    vi.mocked(app.connect).mockImplementation(async () => {
      await app.ontoolresult?.({ content: [], structuredContent: { ...result, grafanaUrl: '' } });
    });
    render(<TraceApplication app={app} />);
    expect(await screen.findByRole('heading', { level: 1, name: 'GET /' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Open in Grafana' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Share' })).toBeNull();
  });
  it('rejects malformed spans and executable navigation URLs', () => {
    expect(parseTraceResult(result)).toEqual(result);
    expect(parseTraceResult({ ...result, grafanaUrl: '' })).toEqual({ ...result, grafanaUrl: '' });
    expect(parseTraceResult({ ...result, grafanaUrl: 'javascript:alert(1)' })).toBeUndefined();
    expect(parseTraceResult({ ...result, grafanaUrl: 'https://user:secret@grafana.example/explore' })).toBeUndefined();
    expect(parseTraceResult({ ...result, spans: [{ ...result.spans[0], durationMs: NaN }] })).toBeUndefined();
    expect(parseTraceResult({ ...result, spans: [result.spans[0], result.spans[0]] })).toBeUndefined();
  });
});
