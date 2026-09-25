import { cleanup, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { makeServiceHealthFixture } from '../preview/serviceHealthFixture';
import { RenderServiceHealthApp } from '../src/service-health';

afterEach(cleanup);

describe('RenderServiceHealthApp', () => {
  it('renders the service status, metric values, dependencies, and real navigation targets', async () => {
    const user = userEvent.setup();
    const onNavigate = vi.fn();
    render(<RenderServiceHealthApp result={makeServiceHealthFixture()} colorMode="dark" onNavigate={onNavigate} />);

    const shell = screen.getByRole('article');
    expect(shell.getAttribute('data-color-mode')).toBe('dark');
    expect(within(shell).getByText('Healthy')).toBeTruthy();
    expect(within(shell).getByText('212 ms')).toBeTruthy();
    expect(within(shell).getByText('0.4%')).toBeTruthy();
    expect(within(shell).getByText('1.2k rps')).toBeTruthy();

    const dependencies = screen.getByRole('table', { name: 'Outbound & databases' });
    expect(within(dependencies).getByRole('rowheader', { name: 'payments-api' })).toBeTruthy();
    expect(within(dependencies).getByText('p95 38 ms')).toBeTruthy();

    await user.click(screen.getByRole('link', { name: 'Open full dashboard' }));
    await user.click(screen.getByRole('link', { name: 'View SLOs' }));
    expect(onNavigate).toHaveBeenNthCalledWith(1, 'https://grafana.example.test/d/service-overview/checkout-service');
    expect(onNavigate).toHaveBeenNthCalledWith(
      2,
      'https://grafana.example.test/a/grafana-slo-app/service/checkout-service'
    );
  });

  it('awaits a range change, disables duplicate changes, and keeps the existing result visible', async () => {
    const user = userEvent.setup();
    let resolveChange: (() => void) | undefined;
    const onTimeRangeChange = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveChange = resolve;
        })
    );
    render(
      <RenderServiceHealthApp
        colorMode="light"
        result={makeServiceHealthFixture()}
        onTimeRangeChange={onTimeRangeChange}
      />
    );

    await user.click(screen.getByRole('button', { name: '6 h' }));

    expect(onTimeRangeChange).toHaveBeenCalledWith('6h');
    expect(screen.getByText('212 ms')).toBeTruthy();
    expect(screen.getByRole('button', { name: '6 h' }).getAttribute('aria-busy')).toBe('true');
    expect(screen.getByRole('button', { name: '24 h' })).toHaveProperty('disabled', true);

    resolveChange?.();
  });

  it('shows unknown and unavailable data without manufacturing a healthy result', () => {
    const result = makeServiceHealthFixture();
    result.status = 'unknown';
    result.summary = 'Health could not be determined from the available signals.';
    result.description = null;
    result.metrics.latencyP95 = {
      value: null,
      unit: 'ms',
      points: [],
      unavailableReason: 'Latency data is unavailable.',
    };
    result.dependencies = [];
    result.warnings = ['The alert source did not return a result.'];

    render(<RenderServiceHealthApp colorMode="light" result={result} />);

    expect(screen.getByText('Unknown')).toBeTruthy();
    expect(screen.getByText('Latency data is unavailable.')).toBeTruthy();
    expect(screen.getByText('No outbound services or databases were returned.')).toBeTruthy();
    expect(screen.getByRole('complementary', { name: 'Data notes' }).textContent).toContain(
      'The alert source did not return a result.'
    );
  });
});
