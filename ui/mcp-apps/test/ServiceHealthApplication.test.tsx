import type { App } from '@modelcontextprotocol/ext-apps';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ServiceHealthApplication } from '../app/ServiceHealthApplication';
import { parseServiceHealthResult } from '../app/parseServiceHealthResult';
import { makeServiceHealthFixture } from '../preview/serviceHealthFixture';

afterEach(cleanup);
function host() {
  const app = {
    connect: vi.fn(async () => {}),
    close: vi.fn(async () => {}),
    getHostContext: () => ({ theme: 'dark' }),
    openLink: vi.fn(async () => ({})),
    callServerTool: vi.fn(async () => ({ content: [], structuredContent: makeServiceHealthFixture('6h') })),
  } as unknown as App;
  vi.mocked(app.connect).mockImplementation(async () => {
    await app.ontoolresult?.({ content: [], structuredContent: makeServiceHealthFixture('1h') });
  });
  return app;
}

describe('service health host bridge', () => {
  it('receives host themes and refreshes ranges through the server', async () => {
    const app = host();
    render(<ServiceHealthApplication app={app} />);
    await waitFor(() => expect(screen.getByRole('article').getAttribute('data-color-mode')).toBe('dark'));
    act(() => app.onhostcontextchanged?.({ theme: 'light' }));
    expect(screen.getByRole('article').getAttribute('data-color-mode')).toBe('light');
    fireEvent.click(screen.getByRole('button', { name: '6 h' }));
    await waitFor(() =>
      expect(app.callServerTool).toHaveBeenCalledWith({
        name: 'render_service_health',
        arguments: expect.objectContaining({ time_range: '6h', service_name: 'checkout-service' }),
      })
    );
    await waitFor(() => expect(screen.getByRole('button', { name: '6 h' }).getAttribute('aria-pressed')).toBe('true'));
  });
  it('keeps previous results when a range refresh fails', async () => {
    const app = host();
    vi.mocked(app.callServerTool).mockRejectedValue(new Error('Forbidden'));
    render(<ServiceHealthApplication app={app} />);
    await screen.findByRole('button', { name: '6 h' });
    fireEvent.click(screen.getByRole('button', { name: '6 h' }));
    expect((await screen.findByRole('alert')).textContent).toContain('Previous results');
    expect(screen.getByRole('button', { name: 'Last 1 h' }).getAttribute('aria-pressed')).toBe('true');
  });
  it('rejects executable URLs, invalid ratios and unordered points', () => {
    const result = makeServiceHealthFixture('1h');
    expect(parseServiceHealthResult(result)).toEqual(result);
    expect(parseServiceHealthResult({ ...result, grafanaUrl: 'javascript:alert(1)' })).toBeUndefined();
    expect(parseServiceHealthResult({ ...result, grafanaUrl: 'https://user:secret@grafana.example/' })).toBeUndefined();
    expect(
      parseServiceHealthResult({
        ...result,
        metrics: { ...result.metrics, errorRatio: { ...result.metrics.errorRatio, value: 5 } },
      })
    ).toBeUndefined();
    expect(
      parseServiceHealthResult({
        ...result,
        metrics: {
          ...result.metrics,
          latencyP95: { ...result.metrics.latencyP95, points: [...result.metrics.latencyP95.points].reverse() },
        },
      })
    ).toBeUndefined();
  });
});
