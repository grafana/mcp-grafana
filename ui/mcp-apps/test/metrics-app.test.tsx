import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { parsePrometheusResult } from '../app/parsePrometheusResult';
import { RenderMetricsApp } from '../src/metrics/RenderMetricsApp';
import { vectorSingleUnbounded } from './metricsFixtures';

afterEach(cleanup);

// A single unbounded value renders as a stat, which needs no chart engine.
const result = (exploreUrl?: string) => ({ ...parsePrometheusResult(vectorSingleUnbounded)!, exploreUrl });

describe('RenderMetricsApp', () => {
  it('offers the derived view and its alternatives, and lets the user switch', () => {
    render(<RenderMetricsApp result={result()} colorMode="light" />);

    const picker = screen.getByRole('group', { name: 'Visualization' });
    const stat = screen.getByRole('button', { name: 'Stat' });
    expect(picker.contains(stat)).toBe(true);
    expect(stat.getAttribute('aria-pressed')).toBe('true');

    fireEvent.click(screen.getByRole('button', { name: 'Table' }));
    expect(screen.getByRole('button', { name: 'Table' }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByRole('table')).toBeTruthy();
  });

  it('keeps the diagnostic line out of the default view', () => {
    const { container, rerender } = render(<RenderMetricsApp result={result()} colorMode="light" />);
    expect(container.textContent).not.toContain('unit=');

    rerender(<RenderMetricsApp result={result()} colorMode="light" debug />);
    expect(container.textContent).toContain('unit=');
  });

  it('hands Open in Grafana to the host, and drops unsafe links', () => {
    const onOpenInGrafana = vi.fn();
    const url = 'https://example.grafana.net/explore?panes=%7B%7D';
    render(<RenderMetricsApp result={result(url)} colorMode="light" onOpenInGrafana={onOpenInGrafana} />);

    fireEvent.click(screen.getByRole('link', { name: /Open in Grafana/ }));
    expect(onOpenInGrafana).toHaveBeenCalledWith({ url });

    cleanup();
    render(<RenderMetricsApp result={result('javascript:alert(1)')} colorMode="light" />);
    expect(screen.queryByRole('link', { name: /Open in Grafana/ })).toBeNull();
  });

  it('shows a refresh failure as an error', () => {
    render(
      <RenderMetricsApp
        result={result()}
        colorMode="light"
        onRefresh={() => {}}
        notice={{ tone: 'error', message: 'The refresh query failed.' }}
      />
    );
    expect(screen.getByRole('alert').textContent).toContain('The refresh query failed.');
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeTruthy();
  });
});
